// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"errors"
	"fmt"
	"strings"
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
		s.print(s.statusStrip(c) + "\n")
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
		})
		s.approvalNotice()
		keys := "MBCPNDWLUOSG"
		bar := []menuItem{{'S', "Settings", ""}}
		if s.user.Sysop {
			keys += "!"
			bar = append(bar, menuItem{'!', "Sysop", s.pendingNote()})
		}
		bar = append(bar, menuItem{'G', "Goodbye", ""})
		s.print("\n" + s.keyBar(bar) + "\n")

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
	if err := s.showWho(); err != nil {
		return err
	}
	return s.pause()
}

// showWho prints the online callers in a panel.
func (s *session) showWho() error {
	online := s.srv.nodes.online()
	heading := fmt.Sprintf("Node  %s %s %s On", term.Pad("Handle", 18), term.Pad("Location", 16), term.Pad("Doing", 20))
	rows := make([]string, 0, len(online))
	for _, n := range online {
		handle := n.Handle
		if handle == "" {
			handle = "(logging in)"
		}
		lock := " "
		if n.Secure {
			lock = colOK + "·"
		}
		rows = append(rows, fmt.Sprintf("%s%4d%s %s%s %s%s %s%s %s%s",
			colBright, n.ID, lock,
			colHandle, safe(term.Pad(handle, 18)),
			colLabel, safe(term.Pad(n.Location, 16)),
			colInfo, safe(term.Pad(n.Activity, 20)),
			colDim, shortDuration(time.Since(n.Since))))
	}
	if err := s.table(plural(len(online), "caller")+" online", heading, rows, "Nobody's here."); err != nil {
		return err
	}
	s.printf(" %s·%s connected over SSH\n", colOK, colDim)
	return nil
}

func (s *session) userList() error {
	s.setActivity("User list")
	s.header("User List")
	users, err := s.srv.store.ListUsers(userListMax)
	if err != nil {
		return err
	}
	heading := fmt.Sprintf(" %s %s %s Calls", term.Pad("Handle", 20), term.Pad("Location", 22), term.Pad("Last call", 12))
	rows := make([]string, 0, len(users))
	for _, u := range users {
		badge := " "
		if u.Sysop {
			badge = colSysop + "*"
		}
		rows = append(rows, fmt.Sprintf("%s%s%s %s%s %s%s %s%5d",
			badge, colHandle, safe(term.Pad(u.Handle, 20)),
			colLabel, safe(term.Pad(u.Location, 22)),
			colInfo, term.Pad(lastCall(u.LastLogin), 12),
			colValue, u.Calls))
	}
	if err := s.table(plural(len(users), "member"), heading, rows, "No members yet."); err != nil {
		return err
	}
	s.printf(" %s*%s sysop\n", colSysop, colDim)
	return s.pause()
}

func (s *session) lastCallers() error {
	s.setActivity("Last callers")
	s.header("Last Callers")
	events, err := s.srv.store.Events(store.EventLogin, lastCallerCount)
	if err != nil {
		return err
	}
	heading := fmt.Sprintf("%s %s %s", term.Pad("Handle", 20), term.Pad("When", 12), "How")
	rows := make([]string, 0, len(events))
	for _, e := range events {
		rows = append(rows, fmt.Sprintf("%s%s %s%s %s%s",
			colHandle, safe(term.Pad(e.Handle, 20)),
			colInfo, term.Pad(ago(e.At), 12),
			colDim, safe(e.Detail)))
	}
	if err := s.table("Recent calls", heading, rows, "Nobody has called yet."); err != nil {
		return err
	}
	return s.pause()
}

// compactUserList prints handles in columns, for picking recipients.
func (s *session) compactUserList() error {
	users, err := s.srv.store.ListUsers(pickerListMax)
	if err != nil {
		return err
	}
	colW := s.inner(s.width()) / userColumns
	var rows []string
	line := ""
	for i, u := range users {
		line += colHandle + safe(term.Pad(u.Handle, colW))
		if (i+1)%userColumns == 0 || i == len(users)-1 {
			rows = append(rows, line)
			line = ""
		}
	}
	s.box("Members", rows...)
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

// statusStrip is the one-line summary under the main menu's title:
// what's new and who's around.
func (s *session) statusStrip(c mainMenuCounts) string {
	part := func(n int, word, color string) string {
		if n == 0 {
			return colDim + "no " + word + "s"
		}
		return color + plural(n, word)
	}
	parts := []string{
		part(c.unreadMail, "new message", colAlert),
		part(c.newPosts, "new post", colAlert),
		colInfo + fmt.Sprintf("%d online", c.online),
		colInfo + plural(c.members, "member"),
	}
	return " " + strings.Join(parts, colBorder+"  ·  ")
}
