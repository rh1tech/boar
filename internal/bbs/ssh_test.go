// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"boar/internal/store"
	"boar/internal/term"
)

func startSSHServer(t *testing.T) (string, *store.Store, ssh.PublicKey) {
	t.Helper()
	addr, st, _, key := startSSHServerFull(t)
	return addr, st, key
}

func startSSHServerFull(t *testing.T) (string, *store.Store, *Server, ssh.PublicKey) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(store.Config{Path: filepath.Join(dir, "boar.db"), KDFIterations: 1000})
	if err != nil {
		t.Fatal(err)
	}
	key, err := LoadOrCreateHostKey(filepath.Join(dir, "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := New(Config{}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan error, 1)
	go func() { done <- srv.ServeSSH(ctx, ln, key) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeSSH: %v", err)
		}
		st.Close()
	})
	return ln.Addr().String(), st, srv, key.PublicKey()
}

// sshCaller is a scripted SSH client with a pty.
type sshCaller struct {
	t   *testing.T
	in  io.WriteCloser
	out chan []byte
	buf bytes.Buffer
	all bytes.Buffer // the whole transcript
}

func dialSSH(t *testing.T, addr string, hostKey ssh.PublicKey, termType string) *sshCaller {
	t.Helper()
	return dialSSHWith(t, addr, hostKey, termType, ssh.KeyboardInteractive(noAnswers))
}

// noAnswers answers a keyboard-interactive challenge with nothing, as the
// BBS never asks a question at the SSH level.
func noAnswers(string, string, []string, []bool) ([]string, error) { return nil, nil }

func dialSSHWith(t *testing.T, addr string, hostKey ssh.PublicKey, termType string, auth ...ssh.AuthMethod) *sshCaller {
	t.Helper()
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "anyone",
		Auth:            auth,
		HostKeyCallback: ssh.FixedHostKey(hostKey),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.RequestPty(termType, 30, 100, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	in, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}
	c := &sshCaller{t: t, in: in, out: make(chan []byte, 64)}
	go func() {
		defer close(c.out)
		for {
			b := make([]byte, 4096)
			n, err := stdout.Read(b)
			if n > 0 {
				c.out <- b[:n]
			}
			if err != nil {
				return
			}
		}
	}()
	return c
}

func (c *sshCaller) expect(sub string) {
	c.t.Helper()
	deadline := time.After(expectTimeout)
	for !bytes.Contains(c.buf.Bytes(), []byte(sub)) {
		select {
		case chunk, ok := <-c.out:
			if !ok {
				c.t.Fatalf("ssh: connection closed waiting for %q\n--- received ---\n%s", sub, c.buf.String())
			}
			c.buf.Write(chunk)
			c.all.Write(chunk)
		case <-deadline:
			c.t.Fatalf("ssh: timed out waiting for %q\n--- received ---\n%s", sub, c.buf.String())
		}
	}
	i := bytes.Index(c.buf.Bytes(), []byte(sub))
	c.buf.Next(i + len(sub))
}

func (c *sshCaller) send(s string) {
	c.t.Helper()
	if _, err := c.in.Write([]byte(s)); err != nil {
		c.t.Fatal(err)
	}
}

func TestSSHLoginSkipsTerminalQuestion(t *testing.T) {
	addr, st, hostKey := startSSHServer(t)
	mustCreateSysopAndUsers(t, st, "Alice")

	c := dialSSH(t, addr, hostKey, "dumb") // dumb = plain ASCII, easy to match
	c.expect("Handle (or NEW to register):")
	c.send("alice\r")
	c.expect("Password:")
	c.send("secret12\r")
	c.expect("No new mail.")
	c.send(" ")
	c.expect("Add a oneliner?")
	c.send("n")
	c.expect("] Main")
	c.send("W")
	c.expect("Alice")
	c.expect(". connected over SSH")

	logins := must(st.Events(store.EventLogin, 1))
	if len(logins) != 1 || !strings.Contains(logins[0].Detail, "via ssh") {
		t.Fatalf("login event = %+v", logins)
	}
}

func TestSSHSignupHasNoTelnetWarning(t *testing.T) {
	addr, _, hostKey := startSSHServer(t)
	c := dialSSH(t, addr, hostKey, "xterm-256color")
	c.expect("Handle")
	c.send("new\r")
	c.expect("Leave the handle empty to go back.")
	c.send("\r")
	c.expect("Handle")
	if strings.Contains(c.all.String(), "not encrypted") {
		t.Fatal("SSH callers should not get the telnet warning")
	}
}

func TestHostKeyIsGeneratedOnceAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "host")
	k1, err := LoadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("host key mode = %v", info.Mode().Perm())
	}
	k2, err := LoadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k1.PublicKey().Marshal(), k2.PublicKey().Marshal()) {
		t.Fatal("host key changed between runs")
	}
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateHostKey(path); err == nil {
		t.Fatal("corrupt key accepted")
	}
}

func TestDetectCharset(t *testing.T) {
	cases := []struct {
		termType string
		cs       term.Charset
		mode     term.ColorMode
		ok       bool
	}{
		{"xterm-256color", term.UTF8, term.ANSI256, true},
		{"linux", term.UTF8, term.ANSI16, true},
		{"syncterm", term.CP437, term.ANSI16, true},
		{"dumb", term.ASCII, term.NoColor, true},
		{"", 0, term.NoColor, false},
	}
	for _, c := range cases {
		cs, mode, ok := detectCharset(c.termType)
		if cs != c.cs || mode != c.mode || ok != c.ok {
			t.Errorf("detectCharset(%q) = %v %v %v", c.termType, cs, mode, ok)
		}
	}
}

func TestClampSize(t *testing.T) {
	if w, h := clampSize(1, 99999); w != minTermWidth || h != maxTermHeight {
		t.Fatalf("clampSize = %d, %d", w, h)
	}
}
