// SPDX-License-Identifier: GPL-3.0-or-later

// Command boar-ftn is the sysop's FidoNet toolbox, for the time before the
// BBS does all of it itself.
//
//	boar-ftn netmail -from 2:410/9999 -to 2:41/0 -to-name "Petros Argyrakis" \
//	    -subject "Node application" < letter.txt
//	boar-ftn show /var/spool/ftn/in/*.pkt
//
// netmail queues one message in binkd's outbound (crash by default, so binkd
// calls straight away). show prints packets, for checking what arrived.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
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
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "boar-ftn:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: boar-ftn netmail [flags] < body.txt\n       boar-ftn show FILE.pkt ...")
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
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		p, err := ftn.ReadPacket(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Printf("== %s: %s -> %s, %s, %d message(s)\n", name, p.From, p.To, p.Created.Format(time.RFC3339), len(p.Messages))
		for _, pm := range p.Messages {
			m := ftn.ParseMessage(pm, p.From, p.To, ftn.CP437)
			where := "netmail " + m.Orig.String() + " -> " + m.Dest.String()
			if m.Area != "" {
				where = "echo " + m.Area + " from " + m.Orig.String()
			}
			fmt.Printf("-- %s | %s -> %s | %s | %s | %s\n%s\n", where, m.From, m.To, m.Subject, m.Charset, m.Date.Format("2006-01-02 15:04"), m.Body)
		}
	}
	return nil
}
