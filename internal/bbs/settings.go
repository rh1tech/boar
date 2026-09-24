// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"errors"
	"strings"

	"boar/internal/store"
)

func (s *session) settings() error {
	for {
		s.setActivity("Settings")
		s.header("Settings")
		s.print("\n")
		loc := s.user.Location
		if loc == "" {
			loc = "not set"
		}
		blocked, err := s.srv.store.BlockedUsers(s.user.ID)
		if err != nil {
			return err
		}
		s.menu("Your account", []menuItem{
			{'P', "Change password", ""},
			{'L', "Location", colDim + "(" + safe(loc) + ")"},
			{'T', "Terminal", colDim + "(" + s.terminalName() + ")"},
			{'B', "Blocked callers", colDim + "(" + plural(len(blocked), "caller") + ")"},
			{'E', "Email", colDim + "(" + s.emailNote() + ")"},
			{'K', "SSH keys", colDim + "(sign in without a password)"},
			{'Q', "Back to main menu", ""},
		})

		k, err := s.menuPrompt("Settings", "PLTBEKQ")
		if err != nil {
			return err
		}
		switch k {
		case 'P':
			err = s.changePassword()
		case 'L':
			err = s.changeLocation()
		case 'T':
			err = s.chooseTerminal(true)
		case 'B':
			err = s.manageBlocks()
		case 'E':
			err = s.emailSettings()
		case 'K':
			err = s.manageSSHKeys()
		case 'Q':
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (s *session) changePassword() error {
	cur, err := s.promptSecret("\n|07Current password|08: |15", store.MaxPasswordLen)
	if err != nil || cur == "" {
		return err
	}
	next, ok, err := s.askPasswordTwice("New password")
	if err != nil || !ok {
		return err
	}
	err = s.srv.store.ChangePassword(s.user.ID, cur, next)
	if errors.Is(err, store.ErrBadCredentials) {
		s.printf("%sCurrent password is incorrect.\n", colAlert)
		return s.pause()
	}
	if err != nil {
		return s.reportError("change password", err)
	}
	s.printf("%sPassword changed.\n", colOK)
	return s.pause()
}

// askPasswordTwice reads a password and its confirmation. ok is false (after
// telling the caller) if they differ.
func (s *session) askPasswordTwice(label string) (string, bool, error) {
	next, err := s.promptSecret("|07"+label+"|08: |15", store.MaxPasswordLen)
	if err != nil {
		return "", false, err
	}
	again, err := s.promptSecret("|07"+label+" again|08: |15", store.MaxPasswordLen)
	if err != nil {
		return "", false, err
	}
	if next != again {
		s.printf("%sPasswords don't match.\n", colAlert)
		return "", false, s.pause()
	}
	return next, true, nil
}

func (s *session) changeLocation() error {
	loc, err := s.prompt("\n|07New location|08: |15", store.MaxLocationLen)
	if err != nil {
		return err
	}
	u, err := s.srv.store.SetLocation(s.user.ID, loc)
	if err != nil {
		return s.reportError("set location", err)
	}
	s.user = u
	s.node.setUser(u)
	return nil
}

// manageBlocks lists blocked callers and lets the caller add or remove one.
// Blocked callers can't mail, page or be heard in chat by this caller.
func (s *session) manageBlocks() error {
	for {
		s.header("Blocked Callers")
		blocked, err := s.srv.store.BlockedUsers(s.user.ID)
		if err != nil {
			return err
		}
		lines := []string{colDim + "Blocked callers can't mail or page you, and you won't see them in chat.", separator}
		if len(blocked) == 0 {
			lines = append(lines, colLabel+"Nobody is blocked.")
		}
		for _, u := range blocked {
			lines = append(lines, colHandle+safe(u.Handle))
		}
		s.box("Blocked", lines...)
		k, err := s.actionPrompt("", "Block", "Unblock", "Quit")
		if err != nil || k == 'Q' {
			return err
		}
		h, err := s.prompt(" |07Handle|08: |15", store.MaxHandleLen)
		if err != nil || h == "" {
			if err != nil {
				return err
			}
			continue
		}
		if err := s.applyBlock(k == 'B', h); err != nil {
			return err
		}
	}
}

func (s *session) applyBlock(block bool, handle string) error {
	u, err := s.srv.store.UserByHandle(strings.TrimSpace(handle))
	if err != nil {
		return s.reportError("find user", err)
	}
	if block {
		err = s.srv.store.Block(s.user.ID, u.ID)
	} else {
		err = s.srv.store.Unblock(s.user.ID, u.ID)
	}
	if err != nil {
		return s.reportError("block", err)
	}
	return nil
}

func (s *session) emailNote() string {
	switch {
	case !s.srv.emailEnabled():
		return "not available"
	case s.user.Email == "":
		return "not set"
	default:
		return s.user.Email + ", " + s.user.EmailMode.String()
	}
}
