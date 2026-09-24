// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"boar/internal/store"
	"boar/internal/term"
)

const (
	keyEnter     = '\r'
	keyBackspace = '\b'
	keyCtrlU     = 0x15 // erase line
	keyEsc       = 0x1b
	keyDel       = 0x7f

	maxScreenWidth = 80
	minPageLines   = 5
)

type escState int

const (
	escNone  escState = iota
	escStart          // saw ESC
	escSeq            // inside ESC [ ... or ESC O ...
)

// session is one caller's conversation with the BBS. It runs on a single
// goroutine; only node is shared with others.
type session struct {
	srv    *Server
	tc     Terminal
	node   *node
	ip     string // for logs
	ipKey  string // for rate limits
	secure bool   // encrypted transport (SSH)
	start  time.Time

	cs    term.Charset
	mode  term.ColorMode
	color bool // mode != NoColor
	rend  *term.Renderer

	user     store.User
	loggedIn atomic.Bool
	keyAuth  bool // signed in with an SSH key

	in     *inputPump
	esc    escState
	lastCR bool
	ubuf   []byte
	werr   error
}

func newSession(srv *Server, t Terminal, n *node, ip string, secure bool) *session {
	s := &session{srv: srv, tc: t, node: n, ip: ip, ipKey: limitKey(ip), secure: secure, start: time.Now(), in: newInputPump(t)}
	s.setTerminal(term.ASCII, term.NoColor)
	return s
}

func (s *session) run() error {
	if err := s.chooseTerminal(false); err != nil {
		return err
	}
	if err := s.welcome(); err != nil {
		return err
	}
	var ok bool
	var err error
	if id := keyUserOf(s.tc); id != 0 {
		ok, err = s.keyLogin(id)
	} else {
		ok, err = s.login()
	}
	if err != nil || !ok {
		return err
	}
	s.loggedIn.Store(true)
	if err := s.afterLogin(); err != nil {
		return err
	}
	if err := s.mainMenu(); err != nil {
		return err
	}
	return s.goodbye()
}

func (s *session) setTerminal(cs term.Charset, mode term.ColorMode) {
	s.cs, s.mode, s.color = cs, mode, mode != term.NoColor
	s.rend = term.NewRenderer(mode)
}

func (s *session) terminalName() string {
	if !s.color {
		return s.cs.String() + ", no color"
	}
	return s.cs.String() + " + ANSI color"
}

func (s *session) width() int {
	w, _ := s.tc.Size()
	return min(w, maxScreenWidth) - 1
}

func (s *session) height() int {
	_, h := s.tc.Size()
	return h
}

func (s *session) setActivity(a string) { s.node.setActivity(a) }

// refreshUser reloads the caller's account, so a sysop's changes (lock,
// demotion) take effect immediately.
func (s *session) refreshUser() error {
	u, err := s.srv.store.UserByID(s.user.ID)
	if err != nil {
		return err
	}
	s.user = u
	return nil
}

func (s *session) transport() string {
	if s.keyAuth {
		return "ssh key"
	}
	if s.secure {
		return "ssh"
	}
	return "telnet"
}

// ---- output -------------------------------------------------------------

// write sends bytes, remembering the first error; the next read reports it.
func (s *session) write(p []byte) {
	if s.werr != nil {
		return
	}
	if _, err := s.tc.Write(p); err != nil {
		s.werr = err
	}
}

// print expands pipe codes, turns \n into CR LF and encodes for the terminal.
// Anything that came from a user must be passed through safe() first.
func (s *session) print(text string) {
	out := strings.ReplaceAll(s.rend.Render(text), "\n", "\r\n")
	s.write(term.Encode(s.cs, out))
}

func (s *session) printf(format string, args ...any) { s.print(fmt.Sprintf(format, args...)) }

// raw writes text without interpreting pipe codes (used to echo input).
func (s *session) raw(text string) { s.write(term.Encode(s.cs, text)) }

func (s *session) clearLine() {
	if s.color {
		s.raw("\r\x1b[K")
		return
	}
	s.raw("\r\n")
}

// safe neutralises user-supplied text for embedding in pipe-coded output.
func safe(v string) string { return term.Escape(term.StripControl(v)) }

func (s *session) flushNotices() {
	for _, n := range s.node.takeNotices() {
		s.printf("\a\n|15*** |14%s |15***|07\n", safe(n))
	}
}

// ---- input --------------------------------------------------------------

// readRune returns the next key as a rune. Enter is keyEnter, both BS and
// DEL are keyBackspace, and cursor-key escape sequences are swallowed.
func (s *session) readRune() (rune, error) {
	if s.werr != nil {
		return 0, s.werr
	}
	for {
		b, err := s.in.next()
		if err != nil {
			return 0, err
		}
		if s.skipEscape(b) {
			continue
		}
		// CR LF and CR NUL are one Enter, whatever the transport.
		wasCR := s.lastCR
		s.lastCR = b == '\r'
		if wasCR && (b == '\n' || b == 0) {
			continue
		}
		switch {
		case b == '\r' || b == '\n':
			return keyEnter, nil
		case b == keyBackspace || b == keyDel:
			return keyBackspace, nil
		case b == 0:
			continue
		case b < 0x80:
			return rune(b), nil
		}
		if r, ok := s.decodeHigh(b); ok {
			return r, nil
		}
	}
}

// skipEscape tracks ANSI escape sequences sent by cursor and function keys
// and reports whether b belongs to one.
func (s *session) skipEscape(b byte) bool {
	switch s.esc {
	case escStart:
		if b == '[' || b == 'O' {
			s.esc = escSeq
			return true
		}
		s.esc = escNone
	case escSeq:
		if b >= 0x40 && b <= 0x7e {
			s.esc = escNone
		}
		return true
	}
	if b == keyEsc {
		s.esc = escStart
		return true
	}
	return false
}

func (s *session) decodeHigh(b byte) (rune, bool) {
	switch s.cs {
	case term.CP437:
		return term.DecodeCP437(b), true
	case term.ASCII:
		return 0, false
	}
	s.ubuf = append(s.ubuf, b)
	if !utf8.FullRune(s.ubuf) {
		if len(s.ubuf) >= utf8.UTFMax {
			s.ubuf = s.ubuf[:0]
		}
		return 0, false
	}
	r, _ := utf8.DecodeRune(s.ubuf)
	s.ubuf = s.ubuf[:0]
	return r, r != utf8.RuneError
}

// readKey waits for one of the (upper-case) runes in allowed.
func (s *session) readKey(allowed string) (rune, error) {
	for {
		r, err := s.readRune()
		if err != nil {
			return 0, err
		}
		r = unicode.ToUpper(r)
		if strings.ContainsRune(allowed, r) {
			return r, nil
		}
	}
}

// choose reads a menu key and echoes it.
func (s *session) choose(allowed string) (rune, error) {
	k, err := s.readKey(allowed)
	if err != nil {
		return 0, err
	}
	if k != keyEnter {
		s.raw(string(k))
	}
	s.raw("\r\n")
	return k, nil
}

type lineOpts struct {
	max  int
	mask bool
	wrap bool   // word-wrap: overflow is returned as carry instead of refused
	init []rune // text already typed (a carried-over word)
}

// readLine edits one line of input. In wrap mode, typing past max moves the
// last word to carry so the editor can continue it on the next line.
func (s *session) readLine(o lineOpts) (line string, carry []rune, err error) {
	buf := append([]rune(nil), o.init...)
	s.raw(string(buf))
	for {
		r, err := s.readRune()
		if err != nil {
			return "", nil, err
		}
		switch {
		case r == keyEnter:
			s.raw("\r\n")
			return string(buf), nil, nil
		case r == keyBackspace:
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				s.raw("\b \b")
			}
		case r == keyCtrlU:
			s.raw(strings.Repeat("\b \b", len(buf)))
			buf = buf[:0]
		case r < 0x20:
			// ignore other control keys
		case len(buf) < o.max:
			buf = append(buf, r)
			if o.mask {
				s.raw("*")
			} else {
				s.raw(string(r))
			}
		case o.wrap:
			return s.wrapOverflow(buf, r)
		}
	}
}

func (s *session) wrapOverflow(buf []rune, r rune) (string, []rune, error) {
	if r == ' ' {
		s.raw("\r\n")
		return string(buf), nil, nil
	}
	cut := -1
	for i := len(buf) - 1; i >= 0; i-- {
		if buf[i] == ' ' {
			cut = i
			break
		}
	}
	if cut < 0 {
		s.raw("\r\n")
		return string(buf), []rune{r}, nil
	}
	word := buf[cut+1:]
	s.raw(strings.Repeat("\b \b", len(word)) + "\r\n")
	carry := append(append([]rune(nil), word...), r)
	return strings.TrimRight(string(buf[:cut]), " "), carry, nil
}

func (s *session) prompt(label string, max int) (string, error) {
	s.print(label)
	line, _, err := s.readLine(lineOpts{max: max})
	return strings.TrimSpace(line), err
}

func (s *session) promptSecret(label string, max int) (string, error) {
	s.print(label)
	line, _, err := s.readLine(lineOpts{max: max, mask: true})
	return line, err
}

// yesNo asks a question; question must already be safe for print.
func (s *session) yesNo(question string, def bool) (bool, error) {
	hint := "|08[|15Y|08/n]"
	if !def {
		hint = "|08[y/|15N|08]"
	}
	s.printf("|07%s %s |15", question, hint)
	k, err := s.readKey("YN\r")
	if err != nil {
		return false, err
	}
	yes := k == 'Y' || (k == keyEnter && def)
	if yes {
		s.raw("Yes\r\n")
	} else {
		s.raw("No\r\n")
	}
	return yes, nil
}

func (s *session) pause() error {
	s.print(s.pauseText())
	_, err := s.readRune()
	s.clearLine()
	return err
}

// page prints lines, stopping after each screenful. used is how many screen
// rows are already taken. It reports false if the caller chose to stop.
func (s *session) page(lines []string, used int) (bool, error) {
	per := max(s.height()-2, minPageLines)
	row := used
	for _, ln := range lines {
		if row >= per {
			more, err := s.more()
			if err != nil || !more {
				return false, err
			}
			row = 0
		}
		s.print(ln + "\n")
		row++
	}
	return true, nil
}

func (s *session) more() (bool, error) {
	s.print("|08-- |07more |08(|15Enter|08 = next page, |15Q|08 = stop) --")
	k, err := s.readKey("Q\r ")
	s.clearLine()
	if err != nil {
		return false, err
	}
	return k != 'Q', nil
}
