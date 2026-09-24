// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"fmt"

	"boar/internal/store"
)

// sysopPickUser asks for a handle and opens the account editor.
func (s *session) sysopPickUser() error {
	for {
		h, err := s.prompt("\n|07Handle |08(|15?|08 = list, Enter = back)|07: |15", store.MaxHandleLen)
		if err != nil || h == "" {
			return err
		}
		if h == "?" {
			if err := s.compactUserList(); err != nil {
				return err
			}
			continue
		}
		u, err := s.srv.store.UserByHandle(h)
		if err != nil {
			if err := s.reportError("find user", err); err != nil {
				return err
			}
			continue
		}
		return s.sysopUser(u.ID)
	}
}

// sysopUser shows one account and the actions on it.
func (s *session) sysopUser(id int64) error {
	for {
		u, err := s.srv.store.UserByID(id)
		if err != nil {
			return s.reportError("load user", err)
		}
		s.showAccount(u)
		self := u.ID == s.user.ID
		words := []string{"Mail", "Password"}
		if !self {
			lock, sysop := "Lock", "Sysop rights"
			if u.Locked {
				lock = "Lock off"
			}
			if u.Sysop {
				sysop = "Sysop rights off"
			}
			words = append(words, lock, sysop, "Delete")
		}
		words = append(words, "Quit")
		s.print("\n" + s.actions(words...) + " " + colBorder + "» |15")
		k, err := s.choose(keysOf(words...))
		if err != nil || k == 'Q' {
			return err
		}
		if ok, err := s.requireSysop(); err != nil || !ok {
			return err
		}
		switch k {
		case 'M':
			err = s.compose(draft{to: []store.User{u}})
		case 'P':
			err = s.sysopResetPassword(u)
		case 'L':
			err = s.sysopToggleLock(u)
		case 'S':
			err = s.sysopToggleSysop(u)
		case 'D':
			var deleted bool
			if deleted, err = s.sysopDeleteUser(u); deleted {
				return err
			}
		}
		if err != nil {
			return err
		}
	}
}

func (s *session) showAccount(u store.User) {
	s.header("User: " + u.Handle)
	flag := func(on bool, yes string) string {
		if on {
			return colAlert + yes
		}
		return colDim + "no"
	}
	online := 0
	for _, n := range s.srv.nodes.online() {
		if n.UserID == u.ID {
			online++
		}
	}
	unread := "?"
	if n, err := s.srv.store.UnreadCount(u.ID); err == nil {
		unread = fmt.Sprint(n)
	} else {
		s.srv.log.Warn("unread count", "user", u.ID, "err", err)
	}
	rows := [][2]string{
		{"Handle", colBright + safe(u.Handle)},
		{"Location", colLabel + safe(u.Location)},
		{"Joined", colLabel + longDate(u.CreatedAt)},
		{"Last call", colLabel + longDate(u.LastLogin)},
		{"Calls", colValue + fmt.Sprint(u.Calls)},
		{"Unread", colValue + unread},
		{"Online", colValue + plural(online, "node")},
		{"Sysop", flag(u.Sysop, "yes")},
		{"Locked", flag(u.Locked, "LOCKED")},
		{"Approved", approvedText(u.Validated)},
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("%s%-10s%s: %s", colInfo, r[0], colBorder, r[1]))
	}
	s.box("Account", lines...)
}

func (s *session) sysopResetPassword(u store.User) error {
	s.print("\n")
	pw, ok, err := s.askPasswordTwice("New password for " + safe(u.Handle))
	if err != nil || !ok {
		return err
	}
	if err := s.srv.store.ResetPassword(s.user.ID, u.ID, pw); err != nil {
		return s.reportError("reset password", err)
	}
	s.audit("reset password of %s", u.Handle)
	s.printf("%sPassword reset.\n", colOK)
	return s.pause()
}

func (s *session) sysopToggleLock(u store.User) error {
	verb := "Lock"
	if u.Locked {
		verb = "Unlock"
	}
	ok, err := s.yesNo(fmt.Sprintf("%s %s?", verb, safe(u.Handle)), false)
	if err != nil || !ok {
		return err
	}
	if err := s.srv.store.SetLocked(s.user.ID, u.ID, !u.Locked); err != nil {
		return s.reportError("lock user", err)
	}
	if !u.Locked {
		s.srv.nodes.kickUser(u.ID, "*** Your account has been locked by the sysop. ***")
	}
	if u.Locked {
		s.audit("unlocked %s", u.Handle)
	} else {
		s.audit("locked %s", u.Handle)
	}
	return nil
}

func (s *session) sysopToggleSysop(u store.User) error {
	q := fmt.Sprintf("Make %s a sysop?", safe(u.Handle))
	if u.Sysop {
		q = fmt.Sprintf("Remove %s's sysop rights?", safe(u.Handle))
	}
	ok, err := s.yesNo(q, false)
	if err != nil || !ok {
		return err
	}
	if err := s.srv.store.SetSysop(s.user.ID, u.ID, !u.Sysop); err != nil {
		return s.reportError("set sysop", err)
	}
	if u.Sysop {
		s.audit("demoted %s", u.Handle)
	} else {
		s.audit("promoted %s to sysop", u.Handle)
	}
	return nil
}

// sysopDeleteUser removes the account after the sysop retypes its handle.
func (s *session) sysopDeleteUser(u store.User) (bool, error) {
	s.printf("\n%sThis deletes %s and their inbox for good.%s\n", colAlert, safe(u.Handle), colLabel)
	confirm, err := s.prompt("|07Type the handle to confirm|08: |15", store.MaxHandleLen)
	if err != nil {
		return false, err
	}
	if confirm != u.Handle {
		s.printf("%sNot deleted.\n", colDim)
		return false, s.pause()
	}
	s.srv.nodes.kickUser(u.ID, "*** Your account has been removed. ***")
	if err := s.srv.store.DeleteUser(s.user.ID, u.ID); err != nil {
		return false, s.reportError("delete user", err)
	}
	s.audit("deleted user %s", u.Handle)
	s.printf("%s%s deleted.\n", colOK, safe(u.Handle))
	return true, s.pause()
}

func approvedText(ok bool) string {
	if ok {
		return colLabel + "yes"
	}
	return colAlert + "NOT YET"
}
