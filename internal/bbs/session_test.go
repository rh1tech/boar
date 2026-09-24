// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"boar/internal/store"
	"boar/internal/telnet"
	"boar/internal/term"
)

// pipeSession returns a session reading the given input. Output is drained
// and returned by the collect func.
func pipeSession(t *testing.T, cs term.Charset, input string) (*session, func() string) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() {
		server.Close()
		client.Close()
	})
	out := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(client)
		out <- string(b)
	}()
	go func() { _, _ = client.Write([]byte(input)) }()
	s := newSession(&Server{}, telnet.New(server, time.Second), &node{}, "test", false)
	s.setTerminal(cs, term.NoColor)
	return s, func() string {
		server.Close()
		return <-out
	}
}

func TestReadLineEditing(t *testing.T) {
	s, output := pipeSession(t, term.ASCII, "helo\blo\x1b[Dx\r")
	line, carry, err := s.readLine(lineOpts{max: 20})
	if err != nil || line != "hellox" || carry != nil {
		t.Fatalf("readLine = %q, %q, %v", line, carry, err)
	}
	if got := output(); !strings.Contains(got, "helo\b \blox\r\n") {
		t.Fatalf("echo = %q", got)
	}
}

func TestReadLineMaskAndLimit(t *testing.T) {
	s, output := pipeSession(t, term.ASCII, "secret\r")
	line, _, err := s.readLine(lineOpts{max: 4, mask: true})
	if err != nil || line != "secr" {
		t.Fatalf("readLine = %q, %v", line, err)
	}
	if got := output(); got != "****\r\n" {
		t.Fatalf("echo = %q", got)
	}
}

func TestReadLineCtrlUErases(t *testing.T) {
	s, _ := pipeSession(t, term.ASCII, "abc\x15xy\r")
	line, _, _ := s.readLine(lineOpts{max: 10})
	if line != "xy" {
		t.Fatalf("line = %q", line)
	}
}

func TestReadLineWordWrap(t *testing.T) {
	s, _ := pipeSession(t, term.ASCII, "the quick\r")
	line, carry, err := s.readLine(lineOpts{max: 7, wrap: true})
	if err != nil || line != "the" || string(carry) != "quic" {
		t.Fatalf("readLine = %q, %q, %v", line, string(carry), err)
	}
	next, carry2, _ := s.readLine(lineOpts{max: 7, wrap: true, init: carry})
	if next != "quick" || carry2 != nil {
		t.Fatalf("continued line = %q, %q", next, string(carry2))
	}
}

func TestReadLineHardWrapWithoutSpaces(t *testing.T) {
	s, _ := pipeSession(t, term.ASCII, "abcdef\r")
	line, carry, _ := s.readLine(lineOpts{max: 4, wrap: true})
	if line != "abcd" || string(carry) != "e" {
		t.Fatalf("readLine = %q, %q", line, string(carry))
	}
}

func TestReadRuneDecodesCharsets(t *testing.T) {
	s, _ := pipeSession(t, term.UTF8, "żó\r")
	line, _, _ := s.readLine(lineOpts{max: 10})
	if line != "żó" {
		t.Fatalf("utf8 line = %q", line)
	}

	s2, _ := pipeSession(t, term.CP437, "\x82\xdb\r")
	line2, _, _ := s2.readLine(lineOpts{max: 10})
	if line2 != "é█" {
		t.Fatalf("cp437 line = %q", line2)
	}

	s3, _ := pipeSession(t, term.ASCII, "a\x80b\r")
	line3, _, _ := s3.readLine(lineOpts{max: 10})
	if line3 != "ab" {
		t.Fatalf("ascii line = %q", line3)
	}
}

func TestReadKeySkipsEscapeSequences(t *testing.T) {
	s, _ := pipeSession(t, term.ASCII, "\x1b[A\x1bOBxq")
	k, err := s.readKey("Q")
	if err != nil || k != 'Q' {
		t.Fatalf("readKey = %q, %v", k, err)
	}
}

func TestYesNoDefaults(t *testing.T) {
	s, _ := pipeSession(t, term.ASCII, "\r\rn")
	if yes, _ := s.yesNo("q", true); !yes {
		t.Error("Enter should pick default yes")
	}
	if yes, _ := s.yesNo("q", false); yes {
		t.Error("Enter should pick default no")
	}
	if yes, _ := s.yesNo("q", true); yes {
		t.Error("n should mean no")
	}
}

func TestEditorCommand(t *testing.T) {
	cases := map[string]rune{"/s": 'S', " /A ": 'A', "/?": '?', "/l": 'L'}
	for in, want := range cases {
		if got, ok := editorCommand(in); !ok || got != want {
			t.Errorf("editorCommand(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"/x", "/save", "s", "", "//"} {
		if _, ok := editorCommand(in); ok {
			t.Errorf("editorCommand(%q) should not be a command", in)
		}
	}
}

func TestQuoteLinesCapsLength(t *testing.T) {
	body := strings.Repeat("line\n", maxQuoteLines+5)
	got := quoteLines("Bob", time.Now(), body, 70)
	if len(got) != maxQuoteLines+3 {
		t.Fatalf("got %d lines", len(got))
	}
	if !strings.HasSuffix(got[0], "Bob wrote:") || got[len(got)-2] != "> [...]" || got[len(got)-1] != "" {
		t.Fatalf("unexpected quote shape: %q", got)
	}
}

func TestRateLimiterWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	r := newRateLimiter(2, time.Minute)
	r.now = func() time.Time { return now }
	r.hit("a")
	if r.blocked("a") {
		t.Fatal("blocked after one hit")
	}
	r.hit("a")
	if !r.blocked("a") || r.blocked("b") {
		t.Fatal("limit not applied per key")
	}
	now = now.Add(61 * time.Second)
	if r.blocked("a") {
		t.Fatal("hits should expire")
	}
	if len(r.hits) != 0 {
		t.Fatal("expired keys should be dropped")
	}
}

func TestNodeTableNotify(t *testing.T) {
	nt := newNodeTable(2)
	a, _ := nt.acquire(nil, false)
	b, _ := nt.acquire(nil, false)
	if _, err := nt.acquire(nil, false); err != errNodesBusy {
		t.Fatalf("third node err = %v", err)
	}
	a.setUser(store.User{ID: 7, Handle: "Alice"})
	nt.notify(7, "hi")
	if got := a.takeNotices(); !reflect.DeepEqual(got, []string{"hi"}) {
		t.Fatalf("notices = %q", got)
	}
	if got := b.takeNotices(); got != nil {
		t.Fatalf("wrong node notified: %q", got)
	}
	nt.release(a)
	if n, err := nt.acquire(nil, false); err != nil || n.id != 1 {
		t.Fatal("released node number should be reused")
	}
}

func TestNodeTableRefusesAfterClose(t *testing.T) {
	nt := newNodeTable(2)
	nt.closeAll("bye")
	if _, err := nt.acquire(nil, false); err != errShuttingDown {
		t.Fatalf("err = %v, want errShuttingDown", err)
	}
}

func TestRateLimiterSweepsIdleKeys(t *testing.T) {
	now := time.Unix(1000, 0)
	r := newRateLimiter(5, time.Minute)
	r.now = func() time.Time { return now }
	r.hit("gone-1")
	r.hit("gone-2")
	now = now.Add(2 * time.Minute)
	r.hit("fresh")
	if len(r.hits) != 1 {
		t.Fatalf("stale keys kept: %v", r.hits)
	}
}

func TestShortDuration(t *testing.T) {
	if got := shortDuration(4 * time.Minute); got != "4m" {
		t.Errorf("got %q", got)
	}
	if got := shortDuration(125 * time.Minute); got != "2h05m" {
		t.Errorf("got %q", got)
	}
}
