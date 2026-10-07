// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"boar/internal/store"
	"boar/internal/term"
)

// echoesMenu lists FidoNet echo areas the BBS has tossed mail for.
func (s *session) echoesMenu() error {
	for {
		s.setActivity("Echomail")
		areas, err := s.srv.store.EchoAreas(s.user.ID)
		if err != nil {
			return err
		}
		s.header("FidoNet Echoes")
		heading := fmt.Sprintf("%4s  %s %s %s", "#", term.Pad("Area", 22), term.Pad("Msgs", 12), "Last")
		var rows []string
		totalNew := 0
		for i, a := range areas {
			totalNew += a.New
			count := fmt.Sprintf("%d", a.Posts)
			if a.New > 0 {
				count += fmt.Sprintf(" %s(%d new)", colAlert, a.New)
			}
			last := "—"
			if !a.LastPost.IsZero() {
				last = shortDate(a.LastPost)
			}
			rows = append(rows, fmt.Sprintf("%s%4d  %s%s %s %s%s",
				colValue, i+1,
				colHandle, safe(term.Pad(a.Tag, 22)),
				fit(colLabel+count, 12),
				colDim, safe(last)))
		}
		empty := "No echomail yet. Linked areas fill in as mail arrives from FidoNet."
		if err := s.table(plural(len(areas), "echo"), heading, rows, empty); err != nil {
			return err
		}

		ans, err := s.prompt(fmt.Sprintf("\n |07Area # |08(|15N|08 = read all %d new, Enter = back) %s» |15", totalNew, colBorder), 4)
		if err != nil || ans == "" {
			return err
		}
		if strings.EqualFold(ans, "n") {
			if err := s.echoNewScan(areas); err != nil {
				return err
			}
			continue
		}
		n, convErr := strconv.Atoi(ans)
		if convErr != nil || n < 1 || n > len(areas) {
			s.printf("%sNo such area.\n", colAlert)
			if err := s.pause(); err != nil {
				return err
			}
			continue
		}
		if err := s.echoAreaView(areas[n-1].EchoArea); err != nil {
			return err
		}
	}
}

func (s *session) echoAreaView(a store.EchoArea) error {
	for {
		s.setActivity("Reading " + a.Tag)
		msgs, err := s.srv.store.EchoMessages(a.ID)
		if err != nil {
			return err
		}
		lastRead, err := s.srv.store.EchoLastRead(s.user.ID, a.ID)
		if err != nil {
			return err
		}
		s.header(a.Tag)
		rows := s.echoListing(msgs, lastRead)
		if err := s.table(a.Tag, rows[0], rows[1:], "No messages in this area yet."); err != nil {
			return err
		}
		ans, err := s.prompt(fmt.Sprintf("\n |07Read # |08(|15N|08 = next unread, Enter = back) %s» |15", colBorder), 4)
		if err != nil || ans == "" {
			return err
		}
		switch strings.ToUpper(ans) {
		case "N":
			err = s.readEchoes(a, unreadEchoes(msgs, lastRead), 0)
		default:
			n, convErr := strconv.Atoi(ans)
			if convErr != nil || n < 1 || n > len(msgs) {
				s.printf("%sNo such message.\n", colAlert)
				err = s.pause()
			} else {
				err = s.readEchoes(a, msgs, n-1)
			}
		}
		if err != nil {
			return err
		}
	}
}

func unreadEchoes(msgs []store.EchoMessage, lastRead int64) []store.EchoMessage {
	var out []store.EchoMessage
	for _, m := range msgs {
		if m.ID > lastRead {
			out = append(out, m)
		}
	}
	// Listing is newest-first; read unread oldest to newest.
	slices.Reverse(out)
	return out
}

func (s *session) echoListing(msgs []store.EchoMessage, lastRead int64) []string {
	subjW := max(s.inner(s.width())-36, 10)
	rows := []string{fmt.Sprintf("%s%4s    %s %s Date", colDim, "#", term.Pad("From", 18), term.Pad("Subject", subjW))}
	for i, m := range msgs {
		mark, subjColor := "    ", colLabel
		if m.ID > lastRead {
			mark, subjColor = " "+colAlert+"*"+colLabel+"  ", colBright
		}
		from := m.FromName
		rows = append(rows, fmt.Sprintf("%s%4d%s%s%s %s%s %s%s",
			colValue, i+1, mark,
			colHandle, safe(term.Pad(from, 18)),
			subjColor, safe(term.Pad(m.Subject, subjW)),
			colInfo, shortDate(m.PostedAt)))
	}
	return rows
}

func (s *session) echoNewScan(areas []store.EchoAreaSummary) error {
	found := false
	for _, a := range areas {
		if a.New == 0 {
			continue
		}
		msgs, err := s.srv.store.EchoMessages(a.ID)
		if err != nil {
			return err
		}
		lastRead, err := s.srv.store.EchoLastRead(s.user.ID, a.ID)
		if err != nil {
			return err
		}
		unread := unreadEchoes(msgs, lastRead)
		if len(unread) == 0 {
			continue
		}
		found = true
		quit := false
		err = s.readEchoesWith(a.EchoArea, unread, 0, func() { quit = true })
		if err != nil || quit {
			return err
		}
	}
	if !found {
		s.printf("%sNothing new in the echoes.\n", colDim)
		return s.pause()
	}
	return nil
}

func (s *session) readEchoes(a store.EchoArea, msgs []store.EchoMessage, idx int) error {
	if len(msgs) == 0 {
		s.printf("%sNothing unread here.\n", colDim)
		return s.pause()
	}
	return s.readEchoesWith(a, msgs, idx, func() {})
}

func (s *session) readEchoesWith(a store.EchoArea, msgs []store.EchoMessage, idx int, onQuit func()) error {
	for idx >= 0 && idx < len(msgs) {
		m := msgs[idx]
		if err := s.srv.store.MarkEchoRead(s.user.ID, a.ID, m.ID); err != nil {
			s.srv.log.Warn("mark echo read failed", "area", a.ID, "err", err)
		}
		title := fmt.Sprintf("%s · %d of %d", a.Tag, idx+1, len(msgs))
		from := m.FromName
		if m.FromAddr != "" {
			from += " (" + m.FromAddr + ")"
		}
		if err := s.showLetter(letter{title: title, body: m.Body, fields: []field{
			{"From", from, colBright},
			{"To", m.ToName, colLabel},
			{"Date", longDate(m.PostedAt), colLabel},
			{"Subj", m.Subject, colKey},
		}}); err != nil {
			return err
		}
		s.flushNotices()
		words := []string{"Next", "Prev", "Quit"}
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
		case 'Q':
			onQuit()
			return nil
		}
	}
	return nil
}
