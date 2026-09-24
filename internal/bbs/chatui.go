package bbs

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"boar/internal/term"
)

const chatHelp = "|08Commands: |15/me|08 action · |15/who|08 · |15/rooms|08 · |15/join|08 name · |15/q|08 quit"

// chatScreen owns the terminal while in chat. Incoming lines arrive on
// another goroutine, so every write goes through mu: an incoming line erases
// the input line, prints, then redraws the prompt and what was typed.
type chatScreen struct {
	s      *session
	mu     sync.Mutex
	rend   *term.Renderer
	buf    []rune
	prompt string
	shown  bool // the prompt is on screen
	werr   error
}

func (c *chatScreen) emit(text string) {
	if c.werr != nil {
		return
	}
	out := strings.ReplaceAll(c.rend.Render(text), "\n", "\r\n")
	if _, err := c.s.tc.Write(term.Encode(c.s.cs, out)); err != nil {
		c.werr = err
	}
}

// show prints an incoming line above the input line.
func (c *chatScreen) show(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.s.color {
		c.emit("\r\x1b[K")
	} else {
		c.emit("\n")
	}
	c.emit(line + "|RE\n" + c.prompt)
	c.emit(term.Escape(string(c.buf)))
	c.shown = true
}

// readLine edits the input line under mu so incoming lines can redraw it.
func (c *chatScreen) readLine() (string, error) {
	c.mu.Lock()
	c.buf = c.buf[:0]
	if !c.shown {
		c.emit(c.prompt)
		c.shown = true
	}
	c.mu.Unlock()
	for {
		r, err := c.s.readRune()
		if err != nil {
			return "", err
		}
		c.mu.Lock()
		switch {
		case r == keyEnter:
			line := string(c.buf)
			c.buf = c.buf[:0]
			c.shown = false
			if c.s.color {
				c.emit("\r\x1b[K")
			} else {
				c.emit("\n")
			}
			c.mu.Unlock()
			return line, c.werr
		case r == keyBackspace:
			if len(c.buf) > 0 {
				c.buf = c.buf[:len(c.buf)-1]
				c.emit("\b \b")
			}
		case r >= 0x20 && len(c.buf) < maxChatLineLen:
			c.buf = append(c.buf, r)
			c.emit(term.Escape(string(r)))
		}
		c.mu.Unlock()
	}
}

// chatRoom runs the teleconference until the caller types /q.
func (s *session) chatRoom() error {
	s.setActivity("Chatting")
	blockedUsers, err := s.srv.store.BlockedUsers(s.user.ID)
	if err != nil {
		return err
	}
	blocked := make(map[int64]bool, len(blockedUsers))
	for _, u := range blockedUsers {
		blocked[u.ID] = true
	}

	s.header("Chat")
	s.box("Teleconference", colLabel+"Say something and press Enter. Everyone in the room sees it.", chatHelp)
	c := &chatScreen{s: s, rend: term.NewRenderer(s.mode), prompt: colTitle + "» " + colLabel}
	m := newChatMember(s.user.ID, s.user.Handle, blocked)

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() { c.forward(m, done) })
	s.srv.chat.join(m, defaultRoom)
	defer func() {
		s.srv.chat.leave(m)
		close(done)
		wg.Wait()
		s.rend = term.NewRenderer(s.mode) // colors on screen changed under us
	}()

	for {
		line, err := c.readLine()
		if err != nil {
			return err
		}
		line = strings.TrimSpace(term.StripControl(line))
		if line == "" {
			continue
		}
		if quit := s.chatCommand(c, m, line); quit {
			return nil
		}
	}
}

// forward prints incoming chat lines and node notices until done closes.
func (c *chatScreen) forward(m *chatMember, done <-chan struct{}) {
	for {
		select {
		case line := <-m.out:
			c.show(line.text)
		case <-c.s.node.notify:
			for _, n := range c.s.node.takeNotices() {
				c.show("\a|15*** |14" + safe(n) + " |15***")
			}
		case <-done:
			return
		}
	}
}

// chatCommand handles one input line. It reports true when the caller quits.
func (s *session) chatCommand(c *chatScreen, m *chatMember, line string) bool {
	cmd, arg, _ := strings.Cut(line, " ")
	if !strings.HasPrefix(cmd, "/") {
		s.chatSay(c, m, fmt.Sprintf("%s<%s>%s %s", colHandle, safe(m.handle), colLabel, safe(line)))
		return false
	}
	switch strings.ToLower(cmd) {
	case "/q", "/quit", "/exit":
		return true
	case "/me":
		if arg != "" {
			s.chatSay(c, m, fmt.Sprintf("%s* %s %s", colSysop, safe(m.handle), safe(arg)))
		}
	case "/who":
		room := s.srv.chat.room(m)
		c.show(fmt.Sprintf("%s*** In #%s: %s%s", colDim, room, colHandle, safe(strings.Join(s.srv.chat.who(room), ", "))))
	case "/rooms":
		counts := s.srv.chat.roomCounts()
		names := slices.Sorted(maps.Keys(counts))
		parts := make([]string, 0, len(names))
		for _, name := range names {
			parts = append(parts, fmt.Sprintf("#%s (%d)", name, counts[name]))
		}
		c.show(colDim + "*** Rooms: " + colLabel + strings.Join(parts, ", "))
	case "/join":
		room := normalizeRoom(arg)
		if room == "" {
			c.show(colAlert + "*** Usage: /join roomname (letters, digits, - and _)")
			return false
		}
		s.srv.chat.join(m, room)
		s.setActivity("Chatting in #" + room)
	default:
		c.show(chatHelp)
	}
	return false
}

func (s *session) chatSay(c *chatScreen, m *chatMember, text string) {
	key := userKey(m.userID)
	if s.srv.limit.chat.blocked(key) {
		c.show(colAlert + "*** Slow down a little.")
		return
	}
	s.srv.limit.chat.hit(key)
	stamp := colDim + time.Now().Format("15:04") + " "
	s.srv.chat.say(m, stamp+text)
}
