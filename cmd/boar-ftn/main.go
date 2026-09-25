// SPDX-License-Identifier: GPL-3.0-or-later

// Command boar-ftn is the sysop's FidoNet toolbox, for the time before the
// BBS does all of it itself.
//
//	boar-ftn netmail -from 2:410/9999 -to 2:41/0 -to-name "Petros Argyrakis" \
//	    -subject "Node application" < letter.txt
//	boar-ftn show /var/spool/ftn/in/*.pkt
//	boar-ftn notify -to sysop@example.com FILE
//
// netmail queues one message in binkd's outbound (crash by default, so binkd
// calls straight away). show prints packets, for checking what arrived.
// notify emails the sysop what arrived; binkd runs it for every received
// packet or mail bundle (see deploy/binkd.cfg). It never moves or deletes the
// file: the BBS imports it later.
package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	"boar/internal/ftn"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "netmail":
		err = netmail(os.Args[2:])
	case "show":
		err = show(os.Args[2:])
	case "notify":
		err = notify(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "boar-ftn:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: boar-ftn netmail [flags] < body.txt\n       boar-ftn show FILE ...\n       boar-ftn notify -to ADDRESS FILE")
	os.Exit(2)
}

func netmail(args []string) error {
	fs := flag.NewFlagSet("netmail", flag.ExitOnError)
	outbound := fs.String("outbound", "/var/spool/ftn/out", "BSO outbound directory for the default zone")
	zone := fs.Int("zone", 2, "default zone (the one that lives in -outbound itself)")
	from := fs.String("from", "", "our address, e.g. 2:410/9999")
	to := fs.String("to", "", "destination address")
	fromName := fs.String("from-name", "Mikhail Matveev", "sender name")
	toName := fs.String("to-name", "Sysop", "recipient name")
	subject := fs.String("subject", "", "subject (up to 71 bytes)")
	flavour := fs.String("flavour", "crash", "crash, normal, hold or direct")
	charset := fs.String("charset", "CP437", "CP437, CP866, LATIN-1 or UTF-8")
	password := fs.String("password", "", "packet password agreed with the destination, if any")
	fs.Parse(args)

	fromAddr, err := ftn.ParseAddr(*from)
	if err != nil {
		return fmt.Errorf("-from: %w", err)
	}
	toAddr, err := ftn.ParseAddr(*to)
	if err != nil {
		return fmt.Errorf("-to: %w", err)
	}
	cs, ok := ftn.ParseCHRS(*charset)
	if !ok {
		return fmt.Errorf("-charset %q: unknown", *charset)
	}
	fl := map[string]ftn.Flavour{"crash": ftn.Crash, "normal": ftn.Normal, "hold": ftn.Hold, "direct": ftn.Direct}[*flavour]
	if fl == 0 {
		return fmt.Errorf("-flavour %q: unknown", *flavour)
	}
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	text := strings.TrimRight(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	if !ftn.Fits(text+*subject+*toName+*fromName, cs) {
		return fmt.Errorf("the text has characters %s cannot hold; use -charset UTF-8", cs)
	}

	now := time.Now()
	m := ftn.Message{
		From: *fromName, To: *toName, Subject: *subject, Date: now,
		Orig: fromAddr, Dest: toAddr, Attr: ftn.AttrPrivate | ftn.AttrLocal | ftn.AttrKillSent,
		MsgID: fmt.Sprintf("%s %08x", fromAddr, uint32(now.Unix())), Charset: cs, TZUTC: ftn.TZUTC(now),
		Body: text, Kludges: []ftn.Kludge{{Name: "PID", Value: "boar-ftn"}},
	}
	if fl == ftn.Crash {
		m.Attr |= ftn.AttrCrash
	}
	header := ftn.Packet{From: fromAddr.Boss(), To: toAddr.Boss(), Password: *password, Created: now}
	if fromAddr.IsPoint() {
		header.From = fromAddr
	}
	out := ftn.Outbound{Root: *outbound, DefaultZone: *zone}
	if err := out.Queue(toAddr.Boss(), fl, header, []ftn.PackedMessage{m.Pack()}); err != nil {
		return err
	}
	fmt.Printf("queued for %s in %s\n", toAddr, out.PacketPath(toAddr.Boss(), fl))
	return nil
}

func show(files []string) error {
	for _, name := range files {
		text, err := describe(name)
		if err != nil {
			return err
		}
		fmt.Print(text)
	}
	return nil
}

// describe renders a packet, or every packet inside a mail bundle.
func describe(name string) (string, error) {
	packets, err := readPackets(name)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	var b strings.Builder
	for _, np := range packets {
		p := np.pkt
		fmt.Fprintf(&b, "== %s: %s -> %s, %s, %d message(s)\n", np.name, p.From, p.To, p.Created.Format(time.RFC3339), len(p.Messages))
		for _, pm := range p.Messages {
			m := ftn.ParseMessage(pm, p.From, p.To, ftn.CP437)
			where := "netmail " + m.Orig.String() + " -> " + m.Dest.String()
			if m.Area != "" {
				where = "echo " + m.Area + " from " + m.Orig.String()
			}
			fmt.Fprintf(&b, "-- %s | %s -> %s | %s | %s | %s\n%s\n\n", where, m.From, m.To, m.Subject, m.Charset, m.Date.Format("2006-01-02 15:04"), m.Body)
		}
	}
	return b.String(), nil
}

type namedPacket struct {
	name string
	pkt  *ftn.Packet
}

// readPackets opens a .pkt, or a ZIP mail bundle (.su0, .mo1 ...) of them.
func readPackets(name string) ([]namedPacket, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		p, err := ftn.ReadPacket(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		return []namedPacket{{filepath.Base(name), p}}, nil
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var out []namedPacket
	for _, f := range zr.File {
		if !strings.EqualFold(filepath.Ext(f.Name), ".pkt") || f.UncompressedSize64 > maxPacketBytes {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return out, err
		}
		p, err := ftn.ReadPacket(io.LimitReader(rc, maxPacketBytes))
		rc.Close()
		if err != nil {
			return out, fmt.Errorf("%s: %w", f.Name, err)
		}
		out = append(out, namedPacket{filepath.Base(name) + ":" + f.Name, p})
	}
	return out, nil
}

const maxPacketBytes = 16 << 20

// notify emails the sysop what arrived. It is best effort by design: binkd
// runs it after a file is safely in the inbound, so a failure here loses
// only the notice, never the mail.
func notify(args []string) error {
	fs := flag.NewFlagSet("notify", flag.ExitOnError)
	to := fs.String("to", "", "address to email")
	from := fs.String("from", "bbs@boar.rh1.tech", "sender address")
	relay := fs.String("smtp", "127.0.0.1:25", "SMTP relay on this machine")
	fs.Parse(args)
	if *to == "" || fs.NArg() != 1 {
		return errors.New("usage: boar-ftn notify -to ADDRESS FILE")
	}
	name := fs.Arg(0)
	text, err := describe(name)
	subject := "FidoNet mail arrived: " + filepath.Base(name)
	if err != nil {
		text = fmt.Sprintf("A file arrived that could not be read as FidoNet mail:\n%s\n\n%v\n", name, err)
		subject = "FidoNet file arrived (unreadable): " + filepath.Base(name)
	}
	if first := firstSubject(text); first != "" {
		subject = "FidoNet: " + first
	}
	msg := "From: Boar BBS FidoNet <" + *from + ">\r\n" +
		"To: " + *to + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		strings.ReplaceAll(text, "\n", "\r\n") +
		"\r\n-- \r\nThe file is still in the inbound: " + name + "\r\n"
	return sendLocal(*relay, *from, *to, []byte(msg))
}

// sendLocal hands a message to an SMTP relay on this machine. Like the BBS's
// own mailer it does not use STARTTLS there: the mail never leaves the host on
// that hop, and the relay's certificate is not issued for 127.0.0.1. Any
// other relay is refused rather than used without TLS.
func sendLocal(relay, from, to string, msg []byte) error {
	host, _, err := net.SplitHostPort(relay)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("-smtp %s: only a relay on this machine is supported", relay)
	}
	c, err := smtp.Dial(relay)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// firstSubject picks "From -> To | Subject" of the first message for the
// email subject line.
func firstSubject(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "-- ") {
			parts := strings.Split(line, " | ")
			if len(parts) >= 3 {
				return parts[1] + ": " + parts[2]
			}
		}
	}
	return ""
}
