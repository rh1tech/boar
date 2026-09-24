package bbs

import (
	"errors"
	"fmt"
	"time"

	"boar/internal/store"
	"boar/internal/term"
)

const (
	userColumns     = 4
	lastCallerCount = 15
	userListMax     = 1000
	pickerListMax   = 200
)

// mainMenuCounts gathers the numbers shown next to main menu entries.
type mainMenuCounts struct {
	unreadMail, newPosts, members, online, inChat int
}

func (s *session) loadMainMenuCounts() (mainMenuCounts, error) {
	var c mainMenuCounts
	var err error
	if c.unreadMail, err = s.srv.store.UnreadCount(s.user.ID); err != nil {
		return c, err
	}
	boards, err := s.srv.store.Boards(s.user.ID)
	if err != nil {
		return c, err
	}
	for _, b := range boards {
		c.newPosts += b.New
	}
	if c.members, err = s.srv.store.UserCount(); err != nil {
		return c, err
	}
	c.online = len(s.srv.nodes.online())
	c.inChat = s.srv.chat.count()
	return c, nil
}

func countNote(n int, suffix string) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%s(%d%s)", colAlert, n, suffix)
}

func (s *session) mainMenu() error {
	for {
		if err := s.refreshUser(); err != nil {
			return err
		}
		s.setActivity("Main menu")
		c, err := s.loadMainMenuCounts()
		if err != nil {
			return err
		}
		s.header("Main Menu")
		chatNote := ""
		if c.inChat > 0 {
			chatNote = fmt.Sprintf("%s(%d here)", colValue, c.inChat)
		}
		s.menuColumns("Messages", []menuItem{
			{'M', "Mailbox", countNote(c.unreadMail, " new")},
			{'B', "Message boards", countNote(c.newPosts, " new")},
			{'C', "Chat room", chatNote},
			{'P', "Page someone", ""},
			{'N', "News", ""},
			{'D', "Door games", s.doorCountNote()},
		}, "People", []menuItem{
			{'W', "Who's online", fmt.Sprintf("%s(%d)", colDim, c.online)},
			{'L', "Last callers", ""},
			{'U', "User list", fmt.Sprintf("%s(%d)", colDim, c.members)},
			{'O', "Oneliner wall", ""},
			{'S', "Settings", ""},
		})
		keys := "MBCPNDWLUOSG"
		s.approvalNotice()
		s.print("\n")
		if s.user.Sysop {
			keys += "!"
			s.print("   " + menuItem{'!', "Sysop menu", s.pendingNote()}.render() + "\n")
		}
		s.print("   " + menuItem{'G', "Goodbye", colDim + "(log off)"}.render() + "\n")

		k, err := s.menuPrompt("Main", keys)
		if err != nil {
			return err
		}
		if k == 'G' {
			bye, err := s.yesNo("Log off?", true)
			if err != nil || bye {
				return err
			}
			continue
		}
		if err := s.mainMenuAction(k); err != nil {
			return err
		}
	}
}

func (s *session) mainMenuAction(k rune) error {
	switch k {
	case 'M':
		return s.mailMenu()
	case 'B':
		return s.boardsMenu()
	case 'C':
		if stop, err := s.awaitingApproval("Chat"); stop || err != nil {
			return err
		}
		return s.chatRoom()
	case 'P':
		if stop, err := s.awaitingApproval("Paging"); stop || err != nil {
			return err
		}
		return s.pageSomeone()
	case 'N':
		return s.news()
	case 'D':
		if stop, err := s.awaitingApproval("The door games"); stop || err != nil {
			return err
		}
		return s.doorsMenu()
	case 'W':
		return s.whoOnline()
	case 'L':
		return s.lastCallers()
	case 'U':
		return s.userList()
	case 'O':
		return s.onelinerWall(false)
	case 'S':
		return s.settings()
	case '!':
		return s.sysopMenu()
	}
	return nil
}

func (s *session) whoOnline() error {
	s.setActivity("Who's online")
	s.header("Who's Online")
	s.print("\n" + s.whoTable() + "\n")
	return s.pause()
}

// whoTable renders the online callers, one per line.
func (s *session) whoTable() string {
	out := fmt.Sprintf("%s Node  %s %s %s Online\n", colDim, term.Pad("Handle", 18), term.Pad("Location", 16), term.Pad("Doing", 18))
	for _, n := range s.srv.nodes.online() {
		handle := n.Handle
		if handle == "" {
			handle = "(logging in)"
		}
		lock := " "
		if n.Secure {
			lock = colOK + "·"
		}
		out += fmt.Sprintf("%s%5d%s %s%s %s%s %s%s %s%s\n",
			colBright, n.ID, lock,
			colHandle, safe(term.Pad(handle, 18)),
			colLabel, safe(term.Pad(n.Location, 16)),
			colInfo, safe(term.Pad(n.Activity, 18)),
			colDim, shortDuration(time.Since(n.Since)))
	}
	return out + fmt.Sprintf("%s        %s·%s = connected over SSH\n", colDim, colOK, colDim)
}

func (s *session) userList() error {
	s.setActivity("User list")
	s.header("User List")
	users, err := s.srv.store.ListUsers(userListMax)
	if err != nil {
		return err
	}
	lines := []string{fmt.Sprintf("%s %s %s %s Calls", colDim, term.Pad("Handle", 20), term.Pad("Location", 20), term.Pad("Last call", 12))}
	for _, u := range users {
		badge := " "
		if u.Sysop {
			badge = colSysop + "*"
		}
		lines = append(lines, fmt.Sprintf("%s%s%s %s%s %s%s %s%5d",
			badge, colHandle, safe(term.Pad(u.Handle, 20)),
			colLabel, safe(term.Pad(u.Location, 20)),
			colInfo, term.Pad(lastCall(u.LastLogin), 12),
			colValue, u.Calls))
	}
	s.print("\n")
	if _, err := s.page(lines, 4); err != nil {
		return err
	}
	s.printf("\n %s*%s = sysop\n", colSysop, colDim)
	return s.pause()
}

func (s *session) lastCallers() error {
	s.setActivity("Last callers")
	s.header("Last Callers")
	events, err := s.srv.store.Events(store.EventLogin, lastCallerCount)
	if err != nil {
		return err
	}
	s.printf("\n%s %s %s %s\n", colDim, term.Pad("Handle", 20), term.Pad("When", 12), "How")
	for _, e := range events {
		s.printf(" %s%s %s%s %s%s\n",
			colHandle, safe(term.Pad(e.Handle, 20)),
			colInfo, term.Pad(ago(e.At), 12),
			colDim, safe(e.Detail))
	}
	s.print("\n")
	return s.pause()
}

// compactUserList prints handles in columns, for picking recipients.
func (s *session) compactUserList() error {
	users, err := s.srv.store.ListUsers(pickerListMax)
	if err != nil {
		return err
	}
	for i, u := range users {
		s.printf("%s%s", colHandle, safe(term.Pad(u.Handle, maxScreenWidth/userColumns-1)))
		if (i+1)%userColumns == 0 || i == len(users)-1 {
			s.print("\n")
		}
	}
	s.print(colLabel)
	return nil
}

func lastCall(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return shortDate(t)
}

func userFacing(err error) bool {
	var ie *store.InputError
	return errors.As(err, &ie) ||
		errors.Is(err, store.ErrHandleTaken) ||
		errors.Is(err, store.ErrMailboxFull) ||
		errors.Is(err, store.ErrNotFound) ||
		errors.Is(err, store.ErrBadCredentials) ||
		errors.Is(err, store.ErrBlocked) ||
		errors.Is(err, store.ErrForbidden) ||
		errors.Is(err, store.ErrNotValidated)
}
