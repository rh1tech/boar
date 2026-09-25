// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"boar/internal/store"
	"boar/internal/term"
)

// Sysop operations for the web interface (internal/web).
//
// Each one does what the matching sysop menu item does, including the parts
// that are not in the store: hanging up a caller who was just locked out,
// telling someone online that they were approved, and writing the audit log.
// Keeping them here, next to the menu code, means the two front ends cannot
// drift apart. The store still checks that the actor is a sysop, so a bug in
// the web layer cannot skip that.

// webTag marks audit entries made from the web interface.
const webTag = " (web)"

var (
	// ErrLoginFailed is the one answer to every failed web login: wrong
	// password, unknown handle, locked account, or an account that is not a
	// sysop. Telling them apart would tell a guesser which handles are worth
	// attacking.
	ErrLoginFailed = errors.New("login failed")

	// ErrLoginThrottled means too many recent failures from this address or
	// for this handle. SysopLogin also returns how long to wait.
	ErrLoginThrottled = errors.New("too many failed logins")
)

// Store gives read access to the database for listing pages.
func (s *Server) Store() *store.Store { return s.store }

// Name is the BBS name shown to callers.
func (s *Server) Name() string { return s.cfg.Name }

// Online lists the connected nodes.
func (s *Server) Online() []NodeInfo { return s.nodes.online() }

// SysopLogin checks a web login. Only unlocked sysops get in. It shares the
// telnet and SSH limits: the per-address failure count and the per-handle
// backoff, so moving from one front end to another gains a guesser nothing.
func (s *Server) SysopLogin(handle, password, ip string) (store.User, time.Duration, error) {
	ipKey := limitKey(ip)
	if s.limit.loginFailures.blocked(ipKey) {
		return store.User{}, 0, ErrLoginThrottled
	}
	account := "handle:" + strings.ToLower(handle)
	wait, ok := s.limit.accounts.begin(account)
	if !ok {
		return store.User{}, wait, ErrLoginThrottled
	}
	u, err := s.store.Authenticate(handle, password)
	if err != nil && !errors.Is(err, store.ErrBadCredentials) {
		s.limit.accounts.release(account)
		return store.User{}, 0, err
	}
	if err == nil && u.Sysop && !u.Locked {
		s.limit.accounts.end(account, true)
		s.event(store.EventLogin, u, ip, "web")
		return u, 0, nil
	}
	// A right password on an account that may not use the web counts as a
	// failure too. Clearing the backoff here would let a guesser confirm a
	// regular caller's password by noticing the delay go away.
	s.limit.accounts.end(account, false)
	s.limit.loginFailures.hit(ipKey)
	if err == nil {
		s.event(store.EventLoginFailed, u, ip, "web: not a sysop or locked")
	} else {
		s.event(store.EventLoginFailed, store.User{Handle: handle}, ip, "web")
	}
	return store.User{}, 0, ErrLoginFailed
}

// Sysop reloads an account and confirms it may still use the web interface,
// so a demotion or a lock takes effect on the very next request.
func (s *Server) Sysop(id int64) (store.User, error) {
	u, err := s.store.UserByID(id)
	if err != nil {
		return store.User{}, err
	}
	if !u.Sysop || u.Locked {
		return store.User{}, store.ErrForbidden
	}
	return u, nil
}

func (s *Server) webAudit(actor store.User, ip, format string, args ...any) {
	detail := fmt.Sprintf(format, args...) + webTag
	s.event(store.EventSysop, actor, ip, detail)
	s.log.Info("sysop action", "sysop", actor.Handle, "action", detail)
}

// KickNode hangs up one caller.
func (s *Server) KickNode(actor store.User, ip string, nodeID int) bool {
	if !s.nodes.kick(nodeID, "*** You have been disconnected by the sysop. ***") {
		return false
	}
	s.webAudit(actor, ip, "kicked node %d", nodeID)
	return true
}

// Broadcast sends one line to everyone online.
func (s *Server) Broadcast(actor store.User, ip, text string) error {
	text = strings.TrimSpace(term.StripControl(text))
	if text == "" {
		return &store.InputError{Msg: "Type something to broadcast."}
	}
	if len([]rune(text)) > maxBroadcastLen {
		return &store.InputError{Msg: fmt.Sprintf("Broadcasts are at most %d characters.", maxBroadcastLen)}
	}
	s.nodes.broadcast("Sysop "+actor.Handle+": "+text, 0)
	s.webAudit(actor, ip, "broadcast %q", text)
	return nil
}

// ApproveUser lets a new caller write.
func (s *Server) ApproveUser(actor store.User, ip string, id int64) error {
	u, err := s.store.UserByID(id)
	if err != nil {
		return err
	}
	if err := s.store.ValidateUser(actor.ID, id); err != nil {
		return err
	}
	s.webAudit(actor, ip, "approved %s", u.Handle)
	s.nodes.notify(u.ID, "A sysop approved your account. Welcome in!")
	return nil
}

// SetLocked locks or unlocks an account. Locking hangs the caller up.
func (s *Server) SetLocked(actor store.User, ip string, id int64, locked bool) error {
	u, err := s.store.UserByID(id)
	if err != nil {
		return err
	}
	if err := s.store.SetLocked(actor.ID, id, locked); err != nil {
		return err
	}
	if locked {
		s.nodes.kickUser(id, "*** Your account has been locked by the sysop. ***")
		s.webAudit(actor, ip, "locked %s", u.Handle)
	} else {
		s.webAudit(actor, ip, "unlocked %s", u.Handle)
	}
	return nil
}

// SetSysop promotes or demotes an account.
func (s *Server) SetSysop(actor store.User, ip string, id int64, sysop bool) error {
	u, err := s.store.UserByID(id)
	if err != nil {
		return err
	}
	if err := s.store.SetSysop(actor.ID, id, sysop); err != nil {
		return err
	}
	if sysop {
		s.webAudit(actor, ip, "promoted %s to sysop", u.Handle)
	} else {
		s.webAudit(actor, ip, "demoted %s", u.Handle)
	}
	return nil
}

// ResetPassword sets a new password for an account.
func (s *Server) ResetPassword(actor store.User, ip string, id int64, password string) error {
	u, err := s.store.UserByID(id)
	if err != nil {
		return err
	}
	if err := s.store.ResetPassword(actor.ID, id, password); err != nil {
		return err
	}
	s.webAudit(actor, ip, "reset the password of %s", u.Handle)
	return nil
}

// DeleteUser hangs the caller up and removes the account, as rejecting a new
// caller does too.
func (s *Server) DeleteUser(actor store.User, ip string, id int64) error {
	u, err := s.store.UserByID(id)
	if err != nil {
		return err
	}
	if actor.ID == id {
		return store.ErrForbidden
	}
	s.nodes.kickUser(id, "*** Your account has been removed. ***")
	if err := s.store.DeleteUser(actor.ID, id); err != nil {
		return err
	}
	s.webAudit(actor, ip, "deleted user %s", u.Handle)
	return nil
}

// SendMail mails one caller from the sysop, with the same live notice and
// email copy mail from the BBS gets.
func (s *Server) SendMail(actor store.User, ip string, toID int64, subject, body string) error {
	to, err := s.store.UserByID(toID)
	if err != nil {
		return err
	}
	msg, err := s.store.SendMessage(actor.ID, toID, subject, body, 0)
	if err != nil {
		return err
	}
	online := s.nodes.notify(to.ID, fmt.Sprintf("New mail from %s: %s", actor.Handle, msg.Subject)) > 0
	s.emailNewMail(to, actor.Handle, msg, online)
	s.webAudit(actor, ip, "mailed %s: %q", to.Handle, msg.Subject)
	return nil
}

// CreateBoard adds a message board.
func (s *Server) CreateBoard(actor store.User, ip, name, description string, sysopOnly bool) error {
	b, err := s.store.CreateBoard(actor.ID, name, description, sysopOnly)
	if err != nil {
		return err
	}
	s.webAudit(actor, ip, "created board %q", b.Name)
	return nil
}

// DeleteBoard removes a board and its posts.
func (s *Server) DeleteBoard(actor store.User, ip string, id int64) error {
	b, err := s.store.Board(id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteBoard(actor.ID, id); err != nil {
		return err
	}
	s.webAudit(actor, ip, "deleted board %q", b.Name)
	return nil
}

// DeletePost removes one post.
func (s *Server) DeletePost(actor store.User, ip string, boardName string, post store.Post) error {
	if err := s.store.DeletePost(post.ID, actor.ID); err != nil {
		return err
	}
	s.webAudit(actor, ip, "deleted post %q on %s", post.Subject, boardName)
	return nil
}

// AddBulletin posts news and tells everyone online, as the menu does.
func (s *Server) AddBulletin(actor store.User, ip, title, body string) error {
	b, err := s.store.AddBulletin(actor.ID, title, body)
	if err != nil {
		return err
	}
	s.webAudit(actor, ip, "posted bulletin %q", b.Title)
	s.nodes.broadcast("News: "+b.Title, 0)
	return nil
}

// DeleteBulletin removes a news bulletin.
func (s *Server) DeleteBulletin(actor store.User, ip string, b store.Bulletin) error {
	if err := s.store.DeleteBulletin(actor.ID, b.ID); err != nil {
		return err
	}
	s.webAudit(actor, ip, "deleted bulletin %q", b.Title)
	return nil
}

// DeleteOneliner removes a line from the wall.
func (s *Server) DeleteOneliner(actor store.User, ip string, o store.Oneliner) error {
	if err := s.store.DeleteOneliner(actor.ID, o.ID); err != nil {
		return err
	}
	s.webAudit(actor, ip, "deleted oneliner by %s: %q", o.Author, o.Text)
	return nil
}

// ArtScreen is one replaceable screen and the custom files standing in for it.
type ArtScreen struct {
	Screen string
	Files  []string // base names inside the art folder
}

// ArtScreens lists the custom art, or nil when no art folder is configured.
func (s *Server) ArtScreens() []ArtScreen {
	if s.cfg.ArtDir == "" {
		return nil
	}
	out := make([]ArtScreen, 0, len(customScreens))
	for _, screen := range customScreens {
		var names []string
		for _, f := range artFiles(s.cfg.ArtDir, screen) {
			names = append(names, filepath.Base(f))
		}
		out = append(out, ArtScreen{Screen: screen, Files: names})
	}
	return out
}

// ArtFile returns the full path of a custom art file by its base name. Only
// names ArtScreens would list are accepted, so a request cannot reach any
// other file on disk.
func (s *Server) ArtFile(name string) (string, bool) {
	for _, screen := range s.ArtScreens() {
		for _, f := range screen.Files {
			if f == name {
				return filepath.Join(s.cfg.ArtDir, f), true
			}
		}
	}
	return "", false
}
