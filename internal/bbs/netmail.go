// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"boar/internal/ftn"
	"boar/internal/store"
	"boar/internal/term"
)

const (
	netmailQueueTries = 10
	netmailQueueWait  = 500 * time.Millisecond
	netmailProgram    = "Boar BBS"
)

// netmailMenu lists the FidoNet netmail the caller may read: everything for
// a sysop, otherwise what was addressed to their handle and what they sent.
func (s *session) netmailMenu() error {
	for {
		s.setActivity("Netmail")
		msgs, err := s.srv.store.NetmailFor(s.user)
		if err != nil {
			return err
		}
		s.header("FidoNet Netmail")
		if err := s.table(plural(len(msgs), "netmail"), s.netmailHeading(), s.netmailRows(msgs),
			"No netmail. Netmail sent to this node's FidoNet addresses lands here."); err != nil {
			return err
		}
		hint := "Enter = back"
		if s.canSendNetmail() {
			hint = "|15W|08 = write, " + hint
		}
		ans, err := s.prompt(fmt.Sprintf("\n |07Read # |08(%s) %s» |15", hint, colBorder), 4)
		if err != nil || ans == "" {
			return err
		}
		if strings.EqualFold(ans, "w") && s.canSendNetmail() {
			if err := s.writeNetmail(netmailDraft{}); err != nil {
				return err
			}
			continue
		}
		n, convErr := strconv.Atoi(ans)
		if convErr != nil || n < 1 || n > len(msgs) {
			s.printf("%sNo such message.\n", colAlert)
			if err := s.pause(); err != nil {
				return err
			}
			continue
		}
		if err := s.readNetmail(msgs, n-1); err != nil {
			return err
		}
	}
}

// canSendNetmail: sysops only. Netmail leaves the BBS under the node's
// address, and the sysop answers for everything sent from it.
func (s *session) canSendNetmail() bool { return s.user.Sysop && s.srv.cfg.FTN.enabled() }

func (s *session) netmailHeading() string {
	return fmt.Sprintf("%s%4s    %s %s %s Date", colDim, "#", term.Pad("From / To", 20), term.Pad("Address", 14),
		term.Pad("Subject", max(s.inner(s.width())-52, 10)))
}

func (s *session) netmailRows(msgs []store.Netmail) []string {
	subjW := max(s.inner(s.width())-52, 10)
	rows := make([]string, 0, len(msgs))
	for i, m := range msgs {
		mark, subjColor := "    ", colLabel
		who, addr := m.FromName, m.FromAddr
		if m.Outgoing {
			mark = " " + colDim + "»" + "  "
			who, addr = m.ToName, m.ToAddr
		} else if m.ReadAt.IsZero() {
			mark, subjColor = " "+colAlert+"*"+colLabel+"  ", colBright
		}
		rows = append(rows, fmt.Sprintf("%s%4d%s%s%s %s%s %s%s %s%s",
			colValue, i+1, mark,
			colHandle, safe(term.Pad(who, 20)),
			colInfo, safe(term.Pad(addr, 14)),
			subjColor, safe(term.Pad(m.Subject, subjW)),
			colInfo, shortDate(m.PostedAt)))
	}
	return rows
}

func (s *session) readNetmail(msgs []store.Netmail, idx int) error {
	for idx >= 0 && idx < len(msgs) {
		m := msgs[idx]
		if !m.Outgoing && m.ReadAt.IsZero() {
			if err := s.srv.store.MarkNetmailRead(m.ID); err != nil {
				s.srv.log.Warn("mark netmail read failed", "id", m.ID, "err", err)
			}
		}
		title := fmt.Sprintf("Netmail · %d of %d", idx+1, len(msgs))
		if err := s.showLetter(letter{title: title, body: m.Body, fields: []field{
			{"From", m.FromName + " (" + m.FromAddr + ")", colBright},
			{"To", m.ToName + " (" + m.ToAddr + ")", colLabel},
			{"Date", longDate(m.PostedAt), colLabel},
			{"Subj", m.Subject, colKey},
		}}); err != nil {
			return err
		}
		words := []string{"Next", "Prev"}
		if s.canSendNetmail() && !m.Outgoing {
			words = append(words, "Reply")
		}
		words = append(words, "Quit")
		s.print("\n" + s.actions(words...) + " " + colBorder + "» |15")
		k, err := s.choose(keysOf(words...) + "\r")
		if err != nil {
			return err
		}
		switch k {
		case 'N', keyEnter:
			idx++
		case 'P':
			idx = max(idx-1, 0)
		case 'R':
			if err := s.replyNetmail(m); err != nil {
				return err
			}
		case 'Q':
			return nil
		}
	}
	return nil
}

type netmailDraft struct {
	toName, subject, replyTo string
	to, from                 ftn.Addr // zero to ask, and to pick
	quote                    []string
}

func (s *session) replyNetmail(m store.Netmail) error {
	to, err := ftn.ParseAddr(m.FromAddr)
	if err != nil {
		s.printf("%sThis netmail has no address to reply to.\n", colAlert)
		return s.pause()
	}
	d := netmailDraft{toName: m.FromName, to: to, subject: prefixed("Re: ", m.Subject), replyTo: m.MsgID}
	// Answer from the address it was written to.
	if own, err := ftn.ParseAddr(m.ToAddr); err == nil && ftn.Contains(s.srv.cfg.FTN.Addresses, own) {
		d.from = own
	}
	quote, err := s.yesNo("Quote the original message?", true)
	if err != nil {
		return err
	}
	if quote {
		d.quote = quoteLines(m.FromName, m.PostedAt, m.Body, editorWidth(s.width())-2)
	}
	return s.writeNetmail(d)
}

func (s *session) writeNetmail(d netmailDraft) error {
	s.setActivity("Writing netmail")
	s.header("New Netmail")
	s.print("\n")
	var err error
	if d.to.Zone == 0 {
		if d.toName, err = s.prompt(" |03To name |08(Enter = Sysop): |15", store.MaxNetmailNameLen); err != nil {
			return err
		}
		if d.toName == "" {
			d.toName = "Sysop"
		}
		line, err := s.prompt(" |03Address |08(e.g. 2:5030/731, Enter = cancel): |15", store.MaxNetmailAddrLen)
		if err != nil || line == "" {
			return err
		}
		if d.to, err = ftn.ParseAddr(line); err != nil {
			s.printf("%sThat is not a FidoNet address.\n", colAlert)
			return s.pause()
		}
	} else {
		s.printf(" %sTo      %s: %s%s (%s)\n", colInfo, colDim, colBright, safe(d.toName), d.to)
	}
	hop, ok := s.srv.cfg.FTN.Routes.Hop(d.to)
	if !ok {
		s.printf("%sThere is no route to %s from this node.\n", colAlert, d.to)
		return s.pause()
	}
	from := d.from
	if from.Zone == 0 {
		from = originFor(s.srv.cfg.FTN.Addresses, d.to)
	}
	s.printf(" %sFrom    %s: %s%s %s(via %s)\n", colInfo, colDim, colLabel, from, colDim, hop)
	subject, err := s.askSubject(d.subject)
	if err != nil {
		return err
	}
	if subject == "" {
		s.printf("%sCancelled.\n", colDim)
		return s.pause()
	}
	body, ok, err := s.editor(d.quote)
	if err != nil {
		return err
	}
	if !ok {
		s.printf("\n%sNetmail discarded.\n", colDim)
		return s.pause()
	}
	n := store.Netmail{
		FromName: s.netmailName(), FromAddr: from.String(),
		ToName: d.toName, ToAddr: d.to.String(),
		Subject: subject, Body: body, ReplyTo: d.replyTo, AuthorID: s.user.ID,
	}
	if err := s.sendNetmail(n, from, d.to, hop); err != nil {
		s.srv.log.Warn("netmail not queued", "to", d.to, "err", err)
		s.printf("\n%sThe netmail could not be queued: %s\n", colAlert, safe(err.Error()))
		return s.pause()
	}
	s.printf("\n%sQueued for %s, via %s.\n", colOK, d.to, hop)
	return s.pause()
}

// netmailName is how the caller signs netmail.
func (s *session) netmailName() string {
	if s.user.Sysop && s.srv.cfg.FTN.SysopName != "" {
		return s.srv.cfg.FTN.SysopName
	}
	return s.user.Handle
}

// originFor picks which of our addresses netmail to dest comes from: one in
// dest's own net, if we have one (2:410/51 when writing to Net 410), else
// the main address.
func originFor(own []ftn.Addr, dest ftn.Addr) ftn.Addr {
	for _, a := range own {
		if a.Zone == dest.Zone && a.Net == dest.Net && !a.IsPoint() {
			return a
		}
	}
	return own[0]
}

// sendNetmail packs n, queues it for hop crash (binkd calls at once), and
// keeps the sent copy.
func (s *session) sendNetmail(n store.Netmail, from, to, hop ftn.Addr) error {
	cfg := s.srv.cfg.FTN
	now := time.Now()
	n.PostedAt = now
	n.MsgID = fmt.Sprintf("%s %08x", from, netmailSerial())
	cs := ftn.CP437
	text := n.Body + n.Subject + n.ToName + n.FromName
	switch {
	case ftn.Fits(text, ftn.CP437):
	case ftn.Fits(text, ftn.CP866):
		cs = ftn.CP866
	default:
		cs = ftn.UTF8
	}
	m := ftn.Message{
		From: n.FromName, To: n.ToName, Subject: n.Subject, Date: now,
		Orig: from, Dest: to, Attr: ftn.AttrPrivate | ftn.AttrLocal | ftn.AttrKillSent | ftn.AttrCrash,
		MsgID: n.MsgID, Reply: n.ReplyTo, Charset: cs, TZUTC: ftn.TZUTC(now),
		Body: n.Body, Kludges: []ftn.Kludge{{Name: "PID", Value: netmailProgram}},
	}
	header := ftn.Packet{From: cfg.Addresses[0], To: hop, Created: now}
	var err error
	for i := 0; i < netmailQueueTries; i++ {
		if err = cfg.Outbound.Queue(hop, ftn.Crash, header, []ftn.PackedMessage{m.Pack()}); !errors.Is(err, ftn.ErrBusy) {
			break
		}
		time.Sleep(netmailQueueWait) // binkd is sending to that node right now
	}
	if err != nil {
		return err
	}
	_, err = s.srv.store.RecordSentNetmail(n)
	return err
}

// netmailSerial is the MSGID serial: random, so two messages in one second
// never share an ID.
func netmailSerial() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint32(b[:])
}
