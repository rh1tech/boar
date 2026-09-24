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
	maxLoginAttempts = 3
	maxPasswordTries = 3
	logoIndent       = 23 // (79 - logo width 33) / 2
)

// chooseTerminal asks for the caller's charset unless the transport told us
// the terminal type. It prints in plain ASCII, since we don't know yet.
func (s *session) chooseTerminal(ask bool) error {
	if tt, ok := s.tc.(termTyper); ok && !ask {
		if cs, mode, known := detectCharset(tt.TermType()); known {
			s.setTerminal(cs, mode)
			return nil
		}
	}
	s.setTerminal(term.ASCII, term.NoColor)
	s.print("\n" + safe(s.srv.cfg.Name) + "\n\n" +
		"Select your terminal:\n" +
		"  1) UTF-8 with ANSI color  (macOS Terminal, iTerm2, PuTTY)\n" +
		"  2) CP437 with ANSI color  (SyncTERM, NetRunner, classic BBS clients)\n" +
		"  3) Plain ASCII, no color\n\n" +
		"Choice [1]: ")
	// A line prompt, not a hotkey: most callers type "2" and then Enter, and
	// a stray Enter must not leak into the login prompt.
	choice, err := s.prompt("", 1)
	if err != nil {
		return err
	}
	switch choice {
	case "2":
		s.setTerminal(term.CP437, term.ANSI16)
	case "3":
		s.setTerminal(term.ASCII, term.NoColor)
	default:
		s.setTerminal(term.UTF8, term.ANSI256)
	}
	return nil
}

func (s *session) welcome() error {
	if shown, err := s.showCustomArt("welcome", s.screenTokens()); err != nil || shown {
		return err
	}
	members, err := s.srv.store.UserCount()
	if err != nil {
		return err
	}
	s.print("|CL\n" + logo(logoIndent) + "\n")
	info := []string{
		center(colLabel+"the wild boar "+colFrame+"·"+colLabel+" private mail "+colFrame+"·"+colLabel+" est. 2026", welcomeInner),
		separator,
		center(fmt.Sprintf("%sNode %s%d%s of %s%d  %s·  %sonline %s%d  %s·  %smembers %s%d",
			colInfo, colValue, s.node.id, colInfo, colValue, s.srv.cfg.MaxNodes, colBorder,
			colInfo, colValue, len(s.srv.nodes.online()), colBorder, colInfo, colValue, members), welcomeInner),
		center(colDim+time.Now().Format("Monday, January 2 2006  15:04 MST"), welcomeInner),
	}
	pad := strings.Repeat(" ", (s.width()-welcomeWidth)/2)
	for _, ln := range s.boxLines(s.srv.cfg.Name, welcomeWidth, info) {
		s.print(pad + ln + "\n")
	}
	return nil
}

const (
	welcomeWidth = 61
	welcomeInner = welcomeWidth - panelPad
)

// center pads a pipe-coded string to sit in the middle of width columns.
func center(text string, width int) string {
	return strings.Repeat(" ", max((width-term.VisibleLen(text))/2, 0)) + text
}

// loginFailed is the one message for a wrong password and a locked account,
// so a correct guess against a locked account reveals nothing.
const loginFailed = "Invalid handle or password, or the account is locked."

// login returns true once the caller is authenticated (or registered).
// Only wrong passwords count as attempts.
func (s *session) login() (bool, error) {
	for attempt := 0; attempt < maxLoginAttempts; {
		if s.srv.limit.loginFailures.blocked(s.ipKey) {
			s.printf("\n%sToo many failed logins from your address. Try again later.\n", colAlert)
			return false, nil
		}
		handle, err := s.prompt("\n |07Handle |08(or |15NEW|08 to register)|07: |15", store.MaxHandleLen)
		if err != nil {
			return false, err
		}
		if handle == "" {
			continue
		}
		if strings.EqualFold(handle, "new") {
			u, ok, err := s.register()
			if err != nil {
				return false, err
			}
			if ok {
				s.user = u
				return true, nil
			}
			continue
		}
		password, err := s.promptSecret(" |07Password|08: |15", store.MaxPasswordLen)
		if err != nil {
			return false, err
		}
		u, ok, err := s.checkPassword(handle, password)
		if err != nil {
			return false, err
		}
		if ok {
			s.user = u
			return true, nil
		}
		attempt++
	}
	s.printf("\n%sToo many attempts. Goodbye.\n", colAlert)
	return false, nil
}

// checkPassword verifies one attempt under the per-handle backoff and says
// why it failed, if it did.
func (s *session) checkPassword(handle, password string) (store.User, bool, error) {
	// Throttle by handle too, so rotating addresses doesn't help a guesser.
	// Unknown handles are throttled the same way, revealing nothing.
	account := "handle:" + strings.ToLower(handle)
	if wait, ok := s.srv.limit.accounts.begin(account); !ok {
		s.printf("%sToo many failed logins for that handle. Wait %s and try again.\n", colAlert, waitText(wait))
		return store.User{}, false, nil
	}
	u, err := s.srv.store.Authenticate(handle, password)
	if err != nil && !errors.Is(err, store.ErrBadCredentials) {
		s.srv.limit.accounts.release(account)
		return store.User{}, false, err
	}
	if err == nil && !u.Locked {
		s.srv.limit.accounts.end(account, true)
		return u, true, nil
	}
	s.srv.limit.accounts.end(account, false)
	s.srv.limit.loginFailures.hit(s.ipKey)
	if err == nil { // right password, locked account
		s.srv.event(store.EventLocked, u, s.ip, s.transport())
	} else {
		s.srv.event(store.EventLoginFailed, store.User{Handle: handle}, s.ip, s.transport())
	}
	s.printf("%s%s\n", colAlert, loginFailed)
	return store.User{}, false, nil
}

// waitText renders a short wait, rounded up to whole seconds.
func waitText(d time.Duration) string {
	secs := int((d + time.Second - 1) / time.Second)
	return plural(max(secs, 1), "second")
}

// keyLogin signs in a caller whose SSH key the server already matched.
func (s *session) keyLogin(userID int64) (bool, error) {
	u, err := s.srv.store.UserByID(userID)
	if errors.Is(err, store.ErrNotFound) {
		s.printf("\n%sThat key's account no longer exists.\n", colAlert)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if u.Locked {
		// The key proves who they are, so it's fine to say why.
		s.srv.event(store.EventLocked, u, s.ip, "ssh key")
		s.printf("\n%sThis account is locked. Contact the sysop.\n", colAlert)
		return false, nil
	}
	s.printf("\n%sSigned in with your SSH key as %s%s%s.\n", colOK, colBright, safe(u.Handle), colOK)
	s.keyAuth = true
	s.user = u
	return true, s.pause()
}

func (s *session) register() (store.User, bool, error) {
	if s.srv.limit.signups.blocked(s.ipKey) {
		s.printf("%sToo many new accounts from your address. Try again later.\n", colAlert)
		return store.User{}, false, nil
	}
	if s.srv.limit.signupsAll.blocked(signupsAllKey) {
		s.printf("%sWe've had a lot of new callers today. Please try again tomorrow.\n", colAlert)
		return store.User{}, false, nil
	}
	s.setActivity("New user signup")
	s.header("New Caller")
	if shown, err := s.showCustomArt("newuser", s.screenTokens()); err != nil {
		return store.User{}, false, err
	} else if !shown {
		if err := s.showArt("newuser", nil); err != nil {
			return store.User{}, false, err
		}
	}
	if !s.secure {
		s.printf("%s  Telnet is not encrypted.%s Don't reuse a password from elsewhere,\n  or call over SSH instead.\n\n", colAlert, colLabel)
	}
	handle, err := s.askNewHandle()
	if err != nil || handle == "" {
		return store.User{}, false, err
	}
	password, err := s.askNewPassword()
	if err != nil || password == "" {
		return store.User{}, false, err
	}
	location, err := s.prompt("|07Location |08(optional)|07: |15", store.MaxLocationLen)
	if err != nil {
		return store.User{}, false, err
	}
	ok, err := s.yesNo("Create account "+safe(handle)+"?", true)
	if err != nil || !ok {
		return store.User{}, false, err
	}
	u, err := s.srv.store.CreateUser(handle, password, location)
	if err != nil {
		return store.User{}, false, s.reportError("create user", err)
	}
	s.srv.limit.signups.hit(s.ipKey)
	s.srv.limit.signupsAll.hit(signupsAllKey)
	if !u.Validated {
		s.srv.notifySysops(fmt.Sprintf("New caller %s is waiting for approval (! then V)", u.Handle))
	}
	s.srv.event(store.EventSignup, u, s.ip, s.transport())
	s.srv.log.Info("new user", "handle", u.Handle, "ip", s.ip)
	return u, true, nil
}

// askNewHandle returns "" if the caller gives up.
func (s *session) askNewHandle() (string, error) {
	for {
		h, err := s.prompt("|07Choose a handle|08: |15", store.MaxHandleLen)
		if err != nil || h == "" {
			return "", err
		}
		if err := store.ValidateHandle(h); err != nil {
			s.printf("%s%s\n", colAlert, safe(err.Error()))
			continue
		}
		if _, err := s.srv.store.UserByHandle(h); err == nil {
			s.printf("%s%s\n", colAlert, store.ErrHandleTaken.Error())
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return "", err
		}
		return h, nil
	}
}

// askNewPassword returns "" if the caller fails to confirm a password.
func (s *session) askNewPassword() (string, error) {
	for range maxPasswordTries {
		p1, err := s.promptSecret("|07Password|08: |15", store.MaxPasswordLen)
		if err != nil {
			return "", err
		}
		if err := store.ValidatePassword(p1); err != nil {
			s.printf("%s%s\n", colAlert, safe(err.Error()))
			continue
		}
		p2, err := s.promptSecret("|07Password again|08: |15", store.MaxPasswordLen)
		if err != nil {
			return "", err
		}
		if p1 != p2 {
			s.printf("%sPasswords don't match.\n", colAlert)
			continue
		}
		return p1, nil
	}
	return "", nil
}

// afterLogin shows the caller's stats, news and mail, then the oneliner wall.
func (s *session) afterLogin() error {
	u, prev, err := s.srv.store.RecordLogin(s.user.ID)
	if err != nil {
		return fmt.Errorf("record login: %w", err)
	}
	s.user = u
	s.node.setUser(u)
	s.srv.event(store.EventLogin, u, s.ip, fmt.Sprintf("node %d via %s", s.node.id, s.transport()))
	s.srv.log.Info("login", "handle", u.Handle, "node", s.node.id)

	if shown, err := s.showCustomArt("logon", s.screenTokens()); err != nil {
		return err
	} else if shown {
		if err := s.pause(); err != nil {
			return err
		}
	}
	s.header("Welcome")
	greeting := "Welcome back, "
	if u.Calls == 1 {
		greeting = "Welcome aboard, "
	}
	lines := []string{colLabel + greeting + colBright + safe(u.Handle) + colLabel + "!", ""}
	if u.Calls > 1 {
		lines = append(lines, fmt.Sprintf("%sLast call %s: %s%s", colInfo, colBorder, colLabel, longDate(prev)))
	}
	lines = append(lines, fmt.Sprintf("%sCalls     %s: %s%d", colInfo, colBorder, colValue, u.Calls),
		fmt.Sprintf("%sNode      %s: %s%d %svia %s", colInfo, colBorder, colValue, s.node.id, colDim, s.transport()))
	if u.Sysop {
		lines = append(lines, "", colSysop+"You are a sysop. Press "+colBright+"!"+colSysop+" at the main menu for the sysop tools.")
	}
	s.box("Signed in", lines...)

	s.approvalNotice()
	if err := s.offerNews(prev); err != nil {
		return err
	}
	if err := s.offerNewMail(); err != nil {
		return err
	}
	return s.onelinerWall(true)
}

func (s *session) offerNews(since time.Time) error {
	n, err := s.srv.store.BulletinsSince(since)
	if err != nil || n == 0 {
		return err
	}
	s.printf("\n  %sThere %s.\n", colValue, pluralVerb(n, "new bulletin"))
	read, err := s.yesNo("Read the news now?", true)
	if err != nil || !read {
		return err
	}
	return s.news()
}

func (s *session) offerNewMail() error {
	unread, err := s.srv.store.UnreadCount(s.user.ID)
	if err != nil {
		return err
	}
	if unread == 0 {
		s.printf("\n  %sNo new mail.\n\n", colDim)
		return s.pause()
	}
	s.printf("\n  %sYou have %s.\n\n", colAlert, plural(unread, "new message"))
	read, err := s.yesNo("Read them now?", true)
	if err != nil || !read {
		return err
	}
	return s.readNewMail()
}

// pluralVerb gives "is 1 new bulletin" / "are 3 new bulletins".
func pluralVerb(n int, word string) string {
	if n == 1 {
		return "is " + plural(n, word)
	}
	return "are " + plural(n, word)
}

func (s *session) goodbye() error {
	s.setActivity("Logging off")
	if shown, err := s.showCustomArt("goodbye", s.screenTokens()); err != nil || shown {
		return err
	}
	return s.showArt("goodbye", map[string]string{
		"BBS":      s.srv.cfg.Name,
		"HANDLE":   s.user.Handle,
		"DURATION": shortDuration(time.Since(s.start)),
	})
}
