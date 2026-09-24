// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"fmt"
	"strings"
	"time"

	"boar/internal/store"
	"boar/internal/term"
)

const (
	maxQuoteLines    = 20
	maxRecipients    = 10
	everyoneKeyword  = "all"
	recipientPrompt  = " |03To      |08(handles, comma separated · |15?|08 = list · Enter = cancel)|08: |15"
	maxRecipientLine = 200
)

// draft is a letter being written. to may be empty; the caller is asked.
type draft struct {
	to      []store.User
	subject string
	replyTo int64
	quote   []string
}

func (s *session) compose(d draft) error {
	s.setActivity("Writing mail")
	s.header("New Message")
	s.print("\n")
	to := d.to
	if len(to) == 0 {
		picked, err := s.pickRecipients()
		if err != nil || len(picked) == 0 {
			return err
		}
		to = picked
	} else {
		s.printf(" %sTo      %s: %s%s\n", colInfo, colDim, colBright, safe(handles(to)))
	}
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
		s.printf("\n%sMessage discarded.\n", colDim)
		return s.pause()
	}
	s.deliver(to, subject, body, d.replyTo)
	return s.pause()
}

// deliver sends one copy per recipient and reports how it went.
func (s *session) deliver(to []store.User, subject, body string, replyTo int64) {
	var sent []string
	for _, u := range to {
		if !s.user.Sysop {
			if s.srv.limit.mail.blocked(userKey(s.user.ID)) {
				s.printf("\n%sYou've sent a lot of mail lately. The rest was not sent; try again later.\n", colAlert)
				break
			}
			s.srv.limit.mail.hit(userKey(s.user.ID))
		}
		msg, err := s.srv.store.SendMessage(s.user.ID, u.ID, subject, body, replyTo)
		if err != nil {
			if !userFacing(err) {
				s.srv.log.Error("send message", "err", err, "from", s.user.Handle, "to", u.Handle)
			}
			s.printf("\n%s%s: %s", colAlert, safe(u.Handle), safe(failureReason(err)))
			continue
		}
		sent = append(sent, u.Handle)
		online := s.srv.nodes.notify(u.ID, fmt.Sprintf("New mail from %s: %s", s.user.Handle, msg.Subject)) > 0
		s.srv.emailNewMail(u, s.user.Handle, msg, online)
	}
	s.srv.log.Info("mail sent", "from", s.user.Handle, "recipients", len(sent))
	if len(sent) == 1 {
		s.printf("\n%sMessage sent to %s%s%s.\n", colOK, colBright, safe(sent[0]), colOK)
	} else if len(sent) > 1 {
		s.printf("\n%sMessage sent to %d callers.\n", colOK, len(sent))
	}
}

func failureReason(err error) string {
	if userFacing(err) {
		return err.Error()
	}
	return "could not be delivered."
}

func handles(users []store.User) string {
	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, u.Handle)
	}
	return strings.Join(names, ", ")
}

// pickRecipients asks for one or more handles. Sysops may type ALL.
func (s *session) pickRecipients() ([]store.User, error) {
	for {
		line, err := s.prompt(recipientPrompt, maxRecipientLine)
		if err != nil || line == "" {
			return nil, err
		}
		if line == "?" {
			if err := s.compactUserList(); err != nil {
				return nil, err
			}
			continue
		}
		users, problem, err := s.resolveRecipients(line)
		if err != nil {
			return nil, err
		}
		if problem == "" {
			return users, nil
		}
		s.printf("%s%s\n", colAlert, safe(problem))
	}
}

// resolveRecipients parses "a, b, c". problem explains what is wrong, if
// anything, in words fit for the caller.
func (s *session) resolveRecipients(line string) ([]store.User, string, error) {
	if strings.EqualFold(strings.TrimSpace(line), everyoneKeyword) {
		if !s.user.Sysop {
			return nil, "Only sysops can mail everyone.", nil
		}
		all, err := s.srv.store.Users()
		if err != nil {
			return nil, "", err
		}
		others := make([]store.User, 0, len(all))
		for _, u := range all {
			if u.ID != s.user.ID {
				others = append(others, u)
			}
		}
		return others, "", nil
	}
	var users []store.User
	var unknown []string
	seen := map[int64]bool{}
	for _, h := range strings.Split(line, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		u, err := s.srv.store.UserByHandle(h)
		if err != nil {
			unknown = append(unknown, h)
			continue
		}
		if !seen[u.ID] {
			seen[u.ID] = true
			users = append(users, u)
		}
	}
	switch {
	case len(unknown) > 0:
		return nil, "Nobody here goes by " + strings.Join(unknown, ", ") + ".", nil
	case !s.user.Validated && !allSysops(users):
		return nil, "Until a sysop approves your account, you can only mail the sysop.", nil
	case len(users) == 0:
		return nil, "Who should get it?", nil
	case len(users) > maxRecipients && !s.user.Sysop:
		return nil, fmt.Sprintf("At most %d recipients per message.", maxRecipients), nil
	}
	return users, "", nil
}

// askSubject returns def if the caller just presses Enter.
func (s *session) askSubject(def string) (string, error) {
	label := " |03Subject |08: |15"
	if def != "" {
		label = fmt.Sprintf(" |03Subject |08[|07%s|08]: |15", safe(def))
	}
	subj, err := s.prompt(label, store.MaxSubjectLen)
	if subj == "" {
		subj = def
	}
	return subj, err
}

func (s *session) reply(m store.Message) error {
	from, err := s.srv.store.UserByID(m.FromID)
	if err != nil {
		s.printf("%sThat caller no longer exists.\n", colAlert)
		return s.pause()
	}
	quote, err := s.yesNo("Quote the original message?", true)
	if err != nil {
		return err
	}
	var lines []string
	if quote {
		lines = quoteLines(from.Handle, m.SentAt, m.Body, editorWidth(s.width())-2)
	}
	return s.compose(draft{to: []store.User{from}, subject: prefixed("Re: ", m.Subject), replyTo: m.ID, quote: lines})
}

func (s *session) forward(m store.Message) error {
	lines := []string{
		"----- Forwarded message -----",
		"From: " + s.handleOf(m.FromID),
		"To:   " + s.handleOf(m.ToID),
		"Date: " + longDate(m.SentAt),
		"Subj: " + m.Subject,
		"",
	}
	lines = append(lines, term.Wrap(m.Body, editorWidth(s.width()))...)
	lines = append(lines, "-----------------------------", "")
	return s.compose(draft{subject: prefixed("Fwd: ", m.Subject), quote: lines})
}

// prefixed adds "Re: " or "Fwd: " once.
func prefixed(prefix, subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), strings.ToLower(prefix)) {
		return subject
	}
	return term.Truncate(prefix+subject, store.MaxSubjectLen)
}

func quoteLines(author string, when time.Time, body string, width int) []string {
	lines := []string{fmt.Sprintf("On %s, %s wrote:", shortDate(when), author)}
	for i, ln := range term.Wrap(body, width) {
		if i == maxQuoteLines {
			lines = append(lines, "> [...]")
			break
		}
		lines = append(lines, "> "+ln)
	}
	return append(lines, "")
}

func allSysops(users []store.User) bool {
	for _, u := range users {
		if !u.Sysop {
			return false
		}
	}
	return true
}
