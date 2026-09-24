package bbs

import (
	"fmt"
	"strconv"
	"strings"

	"boar/internal/store"
	"boar/internal/term"
)

const (
	maxPageLen        = 70
	wallAtLogin       = 8
	wallFull          = 15
	onelinerAuthorPad = 14
)

// pageSomeone sends a one-line message to someone who is online right now.
func (s *session) pageSomeone() error {
	s.setActivity("Paging")
	s.header("Page Someone")
	s.print("\n" + s.whoTable() + "\n")
	target, err := s.prompt("|07Page who |08(handle or node #, Enter = cancel)|07: |15", store.MaxHandleLen)
	if err != nil || target == "" {
		return err
	}
	info, ok := s.findOnline(target)
	if !ok {
		s.printf("%s%s isn't online.\n", colAlert, safe(target))
		return s.pause()
	}
	if info.UserID == s.user.ID {
		s.printf("%sTalking to yourself? Try the oneliner wall.\n", colDim)
		return s.pause()
	}
	blocked, err := s.srv.store.IsBlocked(info.UserID, s.user.ID)
	if err != nil {
		return err
	}
	if blocked && !s.user.Sysop {
		s.printf("%s%s isn't accepting pages from you.\n", colAlert, safe(info.Handle))
		return s.pause()
	}
	if !s.user.Sysop && s.srv.limit.pages.blocked(userKey(s.user.ID)) {
		s.printf("%sEasy on the pager. Try again in a minute.\n", colAlert)
		return s.pause()
	}
	msg, err := s.prompt("|07Message|08: |15", maxPageLen)
	if err != nil || msg == "" {
		return err
	}
	s.srv.limit.pages.hit(userKey(s.user.ID))
	s.srv.nodes.notify(info.UserID, fmt.Sprintf("Page from %s: %s", s.user.Handle, term.StripControl(msg)))
	s.printf("%sPaged %s. They'll see it at their next prompt, or right away in chat.\n", colOK, safe(info.Handle))
	return s.pause()
}

// findOnline matches a handle or node number against the online callers.
func (s *session) findOnline(target string) (NodeInfo, bool) {
	nodeID, numErr := strconv.Atoi(target)
	for _, n := range s.srv.nodes.online() {
		if n.UserID == 0 {
			continue
		}
		if (numErr == nil && n.ID == nodeID) || strings.EqualFold(n.Handle, target) {
			return n, true
		}
	}
	return NodeInfo{}, false
}

// news lists the sysop's bulletins, newest first.
func (s *session) news() error {
	for {
		s.setActivity("Reading news")
		bulletins, err := s.srv.store.Bulletins()
		if err != nil {
			return err
		}
		s.header("News")
		if len(bulletins) == 0 {
			s.printf("\n  %sNo news is good news.\n\n", colDim)
			return s.pause()
		}
		s.print("\n")
		for i, b := range bulletins {
			s.printf("%s%4d  %s%s %s%s\n", colValue, i+1, colInfo, term.Pad(shortDate(b.PostedAt), 7), colBright, safe(b.Title))
		}
		n, ok, err := s.pickNumber(len(bulletins), "Read bulletin")
		if err != nil || !ok {
			return err
		}
		b := bulletins[n-1]
		if err := s.showLetter(letter{title: "News", body: b.Body, fields: []field{
			{"From", s.handleOf(b.AuthorID), colBright},
			{"Date", longDate(b.PostedAt), colLabel},
			{"Subj", b.Title, colKey},
		}}); err != nil {
			return err
		}
		if err := s.pause(); err != nil {
			return err
		}
	}
}

// onelinerWall shows the wall and offers to add a line. At login it is
// shorter and defaults to not adding.
func (s *session) onelinerWall(atLogin bool) error {
	s.setActivity("Oneliner wall")
	n := wallFull
	if atLogin {
		n = wallAtLogin
	}
	lines, err := s.srv.store.Oneliners(n)
	if err != nil {
		return err
	}
	s.header("Oneliner Wall")
	s.print("\n")
	if len(lines) == 0 {
		s.printf("  %sThe wall is empty. Leave the first mark!\n", colDim)
	}
	for _, o := range lines {
		author := o.Author
		if author == "" {
			author = "(gone)"
		}
		s.printf(" %s%s %s%s %s\n", colHandle, safe(term.Pad(author, onelinerAuthorPad)), colDim, "│", colLabel+safe(o.Text))
	}
	s.print("\n")
	if !s.user.Validated {
		return s.pause() // writing on the wall waits for approval
	}
	add, err := s.yesNo("Add a oneliner?", !atLogin)
	if err != nil || !add {
		return err
	}
	if !s.user.Sysop && s.srv.limit.oneliners.blocked(userKey(s.user.ID)) {
		s.printf("%sYou've written on the wall enough for now.\n", colAlert)
		return s.pause()
	}
	text, err := s.prompt(fmt.Sprintf("%s> %s", colDim, colBright), store.MaxOnelinerLen)
	if err != nil || text == "" {
		return err
	}
	if _, err := s.srv.store.AddOneliner(s.user.ID, text); err != nil {
		return s.reportError("add oneliner", err)
	}
	s.srv.limit.oneliners.hit(userKey(s.user.ID))
	return nil
}
