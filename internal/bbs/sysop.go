package bbs

import (
	"fmt"
	"strconv"

	"boar/internal/store"
	"boar/internal/term"
)

const (
	eventLogSize      = 200
	sysopOnelinerList = 30
	maxBroadcastLen   = 70
)

// requireSysop reloads the account and reports whether it is still a sysop,
// so a demotion takes effect mid-session.
func (s *session) requireSysop() (bool, error) {
	if err := s.refreshUser(); err != nil {
		return false, err
	}
	if !s.user.Sysop {
		s.printf("%sYou are no longer a sysop.\n", colAlert)
		return false, s.pause()
	}
	return true, nil
}

// audit records a sysop action in the event log.
func (s *session) audit(format string, args ...any) {
	detail := fmt.Sprintf(format, args...)
	s.srv.event(store.EventSysop, s.user, s.ip, detail)
	s.srv.log.Info("sysop action", "sysop", s.user.Handle, "action", detail)
}

func (s *session) sysopMenu() error {
	for {
		if ok, err := s.requireSysop(); err != nil || !ok {
			return err
		}
		s.setActivity("Sysop menu")
		s.header("Sysop Menu")
		s.menuColumns("Callers", []menuItem{
			{'V', "New callers", s.pendingNote()},
			{'U', "Manage a user", ""},
			{'K', "Kick a node", fmt.Sprintf("%s(%d online)", colDim, len(s.srv.nodes.online()))},
			{'B', "Broadcast", colDim + "(to everyone)"},
			{'E', "Event log", ""},
		}, "Content", []menuItem{
			{'M', "Message boards", ""},
			{'N', "News bulletins", ""},
			{'O', "Oneliner wall", ""},
			{'A', "Custom art", colDim + "(preview)"},
		})
		s.print("\n" + s.keyBar([]menuItem{{'Q', "Back to main menu", ""}}) + "\n")
		k, err := s.menuPrompt("Sysop", "VUKBEMNOAQ")
		if err != nil {
			return err
		}
		switch k {
		case 'V':
			err = s.sysopApprovals()
		case 'U':
			err = s.sysopPickUser()
		case 'K':
			err = s.sysopKick()
		case 'B':
			err = s.sysopBroadcast()
		case 'E':
			err = s.sysopEventLog()
		case 'M':
			err = s.sysopBoards()
		case 'N':
			err = s.sysopNews()
		case 'O':
			err = s.sysopOneliners()
		case 'A':
			err = s.sysopArt()
		case 'Q':
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (s *session) sysopKick() error {
	s.header("Kick a Node")
	if err := s.showWho(); err != nil {
		return err
	}
	ans, err := s.prompt("\n|07Node # |08(Enter = cancel)|07: |15", 4)
	if err != nil || ans == "" {
		return err
	}
	nodeID, convErr := strconv.Atoi(ans)
	if convErr != nil {
		s.printf("%sThat's not a node number.\n", colAlert)
		return s.pause()
	}
	if nodeID == s.node.id {
		s.printf("%sThat's you. Use Goodbye instead.\n", colAlert)
		return s.pause()
	}
	ok, err := s.yesNo(fmt.Sprintf("Disconnect node %d?", nodeID), false)
	if err != nil || !ok {
		return err
	}
	if !s.srv.nodes.kick(nodeID, "*** You have been disconnected by the sysop. ***") {
		s.printf("%sNo such node.\n", colAlert)
		return s.pause()
	}
	s.audit("kicked node %d", nodeID)
	s.printf("%sNode %d disconnected.\n", colOK, nodeID)
	return s.pause()
}

func (s *session) sysopBroadcast() error {
	msg, err := s.prompt("\n|07Broadcast |08(Enter = cancel)|07: |15", maxBroadcastLen)
	if err != nil || msg == "" {
		return err
	}
	s.srv.nodes.broadcast("Sysop "+s.user.Handle+": "+term.StripControl(msg), s.node.id)
	s.audit("broadcast %q", msg)
	s.printf("%sSent to everyone online.\n", colOK)
	return s.pause()
}

func (s *session) sysopEventLog() error {
	s.header("Event Log")
	events, err := s.srv.store.Events("", eventLogSize)
	if err != nil {
		return err
	}
	heading := fmt.Sprintf("%s %s %s From", term.Pad("When", 12), term.Pad("What", 13), term.Pad("Who", 16))
	var lines []string
	for _, e := range events {
		color := colLabel
		switch e.Kind {
		case store.EventLoginFailed, store.EventLocked:
			color = colAlert
		case store.EventSysop:
			color = colSysop
		}
		lines = append(lines, fmt.Sprintf("%s%s %s%s %s%s %s%s",
			colInfo, term.Pad(e.At.Local().Format("Jan 02 15:04"), 12),
			color, term.Pad(e.Kind, 13),
			colHandle, safe(term.Pad(e.Handle, 16)),
			colDim, safe(e.IP)))
		if e.Detail != "" {
			lines = append(lines, fmt.Sprintf("%s%13s%s", colDim, "", safe(term.Truncate(e.Detail, s.inner(s.width())-13))))
		}
	}
	if err := s.table("Newest first", heading, lines, "Nothing has happened yet."); err != nil {
		return err
	}
	return s.pause()
}

func (s *session) sysopBoards() error {
	for {
		boards, err := s.srv.store.Boards(s.user.ID)
		if err != nil {
			return err
		}
		s.header("Manage Boards")
		rows := make([]string, 0, len(boards))
		for i, b := range boards {
			who := colDim + "everyone"
			if b.SysopOnly {
				who = colSysop + "sysops only"
			}
			rows = append(rows, fmt.Sprintf("%s%4d  %s%s %s%s %s", colValue, i+1, colHandle, safe(term.Pad(b.Name, 24)),
				colLabel, term.Pad(plural(b.Posts, "post"), 12), who))
		}
		heading := fmt.Sprintf("%4s  %s %s %s", "#", term.Pad("Board", 24), term.Pad("Posts", 12), "Who posts")
		if err := s.table(plural(len(boards), "board"), heading, rows, "No boards yet. Press C to create one."); err != nil {
			return err
		}
		words := []string{"Create", "Delete", "Quit"}
		if len(boards) == 0 {
			words = []string{"Create", "Quit"}
		}
		k, err := s.actionPrompt("", words...)
		if err != nil || k == 'Q' {
			return err
		}
		if ok, err := s.requireSysop(); err != nil || !ok {
			return err
		}
		if k == 'C' {
			err = s.createBoard()
		} else {
			err = s.deleteBoard(boards)
		}
		if err != nil {
			return err
		}
	}
}

func (s *session) createBoard() error {
	name, err := s.prompt("|07Board name|08: |15", store.MaxBoardNameLen)
	if err != nil || name == "" {
		return err
	}
	desc, err := s.prompt("|07Description|08: |15", store.MaxBoardDescLen)
	if err != nil {
		return err
	}
	sysopOnly, err := s.yesNo("Only sysops may post?", false)
	if err != nil {
		return err
	}
	b, err := s.srv.store.CreateBoard(s.user.ID, name, desc, sysopOnly)
	if err != nil {
		return s.reportError("create board", err)
	}
	s.audit("created board %q", b.Name)
	return nil
}

func (s *session) deleteBoard(boards []store.BoardSummary) error {
	n, ok, err := s.pickNumber(len(boards), "Delete board")
	if err != nil || !ok {
		return err
	}
	b := boards[n-1]
	sure, err := s.yesNo(fmt.Sprintf("Delete %s and its %s?", safe(b.Name), plural(b.Posts, "post")), false)
	if err != nil || !sure {
		return err
	}
	if err := s.srv.store.DeleteBoard(s.user.ID, b.ID); err != nil {
		return s.reportError("delete board", err)
	}
	s.audit("deleted board %q", b.Name)
	return nil
}

func (s *session) sysopNews() error {
	for {
		bulletins, err := s.srv.store.Bulletins()
		if err != nil {
			return err
		}
		s.header("Manage News")
		if err := s.bulletinTable(bulletins, "No bulletins yet. Press A to post the first one."); err != nil {
			return err
		}
		words := []string{"Add", "Delete", "Quit"}
		if len(bulletins) == 0 {
			words = []string{"Add", "Quit"}
		}
		k, err := s.actionPrompt("", words...)
		if err != nil || k == 'Q' {
			return err
		}
		if ok, err := s.requireSysop(); err != nil || !ok {
			return err
		}
		if k == 'A' {
			err = s.addBulletin()
		} else {
			err = s.deleteBulletin(bulletins)
		}
		if err != nil {
			return err
		}
	}
}

func (s *session) addBulletin() error {
	title, err := s.prompt("|07Title|08: |15", store.MaxSubjectLen)
	if err != nil || title == "" {
		return err
	}
	body, ok, err := s.editor(nil)
	if err != nil || !ok {
		return err
	}
	b, err := s.srv.store.AddBulletin(s.user.ID, title, body)
	if err != nil {
		return s.reportError("add bulletin", err)
	}
	s.audit("posted bulletin %q", b.Title)
	s.srv.nodes.broadcast("News: "+b.Title, s.node.id)
	return nil
}

func (s *session) deleteBulletin(bulletins []store.Bulletin) error {
	n, ok, err := s.pickNumber(len(bulletins), "Delete bulletin")
	if err != nil || !ok {
		return err
	}
	b := bulletins[n-1]
	if err := s.srv.store.DeleteBulletin(s.user.ID, b.ID); err != nil {
		return s.reportError("delete bulletin", err)
	}
	s.audit("deleted bulletin %q", b.Title)
	return nil
}

func (s *session) sysopOneliners() error {
	for {
		lines, err := s.srv.store.Oneliners(sysopOnelinerList)
		if err != nil {
			return err
		}
		s.header("Manage Oneliners")
		rows := make([]string, 0, len(lines))
		for i, o := range lines {
			rows = append(rows, fmt.Sprintf("%s%4d  %s%s %s%s", colValue, i+1, colHandle, safe(term.Pad(o.Author, onelinerAuthorPad)), colLabel, safe(o.Text)))
		}
		if err := s.table("The wall", "", rows, "The wall is empty."); err != nil {
			return err
		}
		if len(lines) == 0 {
			return s.pause()
		}
		n, ok, err := s.pickNumber(len(lines), "Delete oneliner")
		if err != nil || !ok {
			return err
		}
		if ok, err := s.requireSysop(); err != nil || !ok {
			return err
		}
		o := lines[n-1]
		if err := s.srv.store.DeleteOneliner(s.user.ID, o.ID); err != nil {
			return s.reportError("delete oneliner", err)
		}
		s.audit("deleted oneliner by %s: %q", o.Author, o.Text)
	}
}
