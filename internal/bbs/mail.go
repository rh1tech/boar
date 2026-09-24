package bbs

import (
	"fmt"
	"slices"
	"strconv"

	"boar/internal/store"
	"boar/internal/term"
)

const listHeadRows = 5

// mailView says how a message list is labelled.
type mailView int

const (
	viewInbox  mailView = iota // everything is from someone
	viewOutbox                 // everything is to someone; show read receipts
	viewMixed                  // search results and threads: « from, » to
)

func (s *session) handleOf(id int64) string {
	if u, err := s.srv.store.UserByID(id); err == nil {
		return u.Handle
	}
	return "(gone)"
}

func (s *session) mailMenu() error {
	for {
		s.setActivity("Mailbox")
		inbox, err := s.srv.store.Inbox(s.user.ID)
		if err != nil {
			return err
		}
		unread := countUnread(inbox)
		s.header("Mailbox")
		s.print("\n")
		newNote := colDim + "(none)"
		if unread > 0 {
			newNote = fmt.Sprintf("%s(%d new)", colAlert, unread)
		}
		s.menu("Mail", []menuItem{
			{'N', "Read new mail", newNote},
			{'I', "Inbox", fmt.Sprintf("%s(%s)", colDim, plural(len(inbox), "message"))},
			{'S', "Send a message", ""},
			{'O', "Outbox", colDim + "(sent mail, read receipts)"},
			{'F', "Find", colDim + "(search your mail)"},
			{'Q', "Back to main menu", ""},
		})

		k, err := s.menuPrompt("Mail", "NISOFQ")
		if err != nil {
			return err
		}
		switch k {
		case 'N':
			err = s.readNewMail()
		case 'I':
			err = s.browseMail("Inbox", viewInbox, func() ([]store.Message, error) { return s.srv.store.Inbox(s.user.ID) })
		case 'O':
			err = s.browseMail("Outbox", viewOutbox, func() ([]store.Message, error) { return s.srv.store.Outbox(s.user.ID) })
		case 'S':
			err = s.compose(draft{})
		case 'F':
			err = s.searchMail()
		case 'Q':
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func countUnread(msgs []store.Message) int {
	n := 0
	for _, m := range msgs {
		if !m.IsRead() {
			n++
		}
	}
	return n
}

func (s *session) readNewMail() error {
	inbox, err := s.srv.store.Inbox(s.user.ID)
	if err != nil {
		return err
	}
	unread := slices.DeleteFunc(inbox, store.Message.IsRead)
	if len(unread) == 0 {
		s.printf("\n%sNo new mail.\n", colDim)
		return s.pause()
	}
	return s.readMail("New mail", unread, 0)
}

func (s *session) searchMail() error {
	q, err := s.prompt("\n|07Search for |08(words, subject or handle)|07: |15", store.MaxSubjectLen)
	if err != nil || q == "" {
		return err
	}
	found, err := s.srv.store.SearchMail(s.user.ID, q)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		s.printf("%sNo messages match %q.\n", colDim, safe(q))
		return s.pause()
	}
	return s.browseMail("Search: "+q, viewMixed, func() ([]store.Message, error) {
		return s.srv.store.SearchMail(s.user.ID, q)
	})
}

// browseMail lists messages and lets the caller pick one. load is called
// again after each read so deletions show up.
func (s *session) browseMail(title string, view mailView, load func() ([]store.Message, error)) error {
	for {
		s.setActivity("Reading mail")
		msgs, err := load()
		if err != nil {
			return err
		}
		s.header(title)
		if len(msgs) == 0 {
			if err := s.table("Messages", "", nil, "Nothing here yet."); err != nil {
				return err
			}
			return s.pause()
		}
		rows := s.mailListing(view, msgs)
		if err := s.table(plural(len(msgs), "message"), rows[0], rows[1:], ""); err != nil {
			return err
		}
		if view == viewOutbox {
			s.printf(" %s√%s read by the recipient\n", colOK, colDim)
		}
		n, ok, err := s.pickNumber(len(msgs), "Read message")
		if err != nil || !ok {
			return err
		}
		if err := s.readMail(title, msgs, n-1); err != nil {
			return err
		}
	}
}

// pickNumber asks for 1..max. ok is false when the caller just presses Enter.
func (s *session) pickNumber(maxN int, what string) (int, bool, error) {
	for {
		ans, err := s.prompt(fmt.Sprintf("\n |07%s |08(1-%d, Enter = back) %s» |15", what, maxN, colBorder), 4)
		if err != nil || ans == "" {
			return 0, false, err
		}
		n, convErr := strconv.Atoi(ans)
		if convErr == nil && n >= 1 && n <= maxN {
			return n, true, nil
		}
		s.printf("%sNo such number.\n", colAlert)
	}
}

func (s *session) mailListing(view mailView, msgs []store.Message) []string {
	who := "From"
	switch view {
	case viewOutbox:
		who = "To"
	case viewMixed:
		who = "With"
	}
	subjW := max(s.inner(s.width())-32, 10)
	rows := []string{fmt.Sprintf("%s%4s    %s %s Date", colDim, "#", term.Pad(who, 16), term.Pad("Subject", subjW))}
	for i, m := range msgs {
		sent := m.FromID == s.user.ID && m.ToID != s.user.ID
		other, dir := m.FromID, "« "
		if sent {
			other, dir = m.ToID, "» "
		}
		if view != viewMixed {
			dir = ""
		}
		mark, subjColor := "    ", colLabel
		switch {
		case !sent && !m.IsRead():
			mark, subjColor = " "+colAlert+"*"+colLabel+"  ", colBright
		case sent && view == viewOutbox && m.IsRead():
			mark = " " + colOK + "√" + colLabel + "  "
		}
		rows = append(rows, fmt.Sprintf("%s%4d%s%s%s %s%s %s%s",
			colValue, i+1, mark,
			colHandle, safe(term.Pad(dir+s.handleOf(other), 16)),
			subjColor, safe(term.Pad(m.Subject, subjW)),
			colInfo, shortDate(m.SentAt)))
	}
	return rows
}

// readMail shows msgs[idx] and lets the caller page through the list.
func (s *session) readMail(title string, msgs []store.Message, idx int) error {
	for idx >= 0 && idx < len(msgs) {
		m := msgs[idx]
		received := m.ToID == s.user.ID
		if received && !m.IsRead() {
			if err := s.srv.store.MarkRead(m.ID, s.user.ID); err != nil {
				s.srv.log.Warn("mark read failed", "msg", m.ID, "err", err)
			}
		}
		if err := s.showLetter(s.mailLetter(fmt.Sprintf("%s · %d of %d", title, idx+1, len(msgs)), m)); err != nil {
			return err
		}
		s.flushNotices()
		words := []string{"Forward", "Thread", "Next", "Prev", "Delete", "Quit"}
		if received {
			words = append([]string{"Reply"}, words...)
		}
		if s.srv.emailEnabled() {
			words = append(words[:len(words)-1], "Email me", "Quit")
		}
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
			err = s.reply(m)
		case 'F':
			err = s.forward(m)
		case 'T':
			err = s.readThread(m)
		case 'E':
			err = s.emailMeCopy(m)
		case 'D':
			var deleted bool
			if deleted, err = s.deleteMail(m); deleted {
				msgs = slices.Concat(msgs[:idx], msgs[idx+1:])
				idx = min(idx, len(msgs)-1)
			}
		case 'Q':
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *session) mailLetter(title string, m store.Message) letter {
	l := letter{title: title, body: m.Body, fields: []field{
		{"From", s.handleOf(m.FromID), colBright},
		{"To", s.handleOf(m.ToID), colBright},
		{"Date", longDate(m.SentAt), colLabel},
		{"Subj", m.Subject, colKey},
	}}
	if m.FromID == s.user.ID && m.ToID != s.user.ID {
		read := "not yet"
		if m.IsRead() {
			read = longDate(m.ReadAt)
		}
		l.fields = append(l.fields, field{"Read", read, colOK})
	}
	return l
}

func (s *session) readThread(m store.Message) error {
	thread, err := s.srv.store.Thread(m.ID, s.user.ID)
	if err != nil {
		return s.reportError("load thread", err)
	}
	if len(thread) < 2 {
		s.printf("%sThis message is the whole conversation.\n", colDim)
		return s.pause()
	}
	at := slices.IndexFunc(thread, func(t store.Message) bool { return t.ID == m.ID })
	return s.readMail("Thread", thread, max(at, 0))
}

func (s *session) deleteMail(m store.Message) (bool, error) {
	ok, err := s.yesNo("Delete this message?", false)
	if err != nil || !ok {
		return false, err
	}
	if err := s.srv.store.DeleteMessage(m.ID, s.user.ID); err != nil {
		return false, s.reportError("delete message", err)
	}
	return true, nil
}
