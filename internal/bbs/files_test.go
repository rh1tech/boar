// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"bytes"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"boar/internal/filelink"
	"boar/internal/store"
	"boar/internal/telnet"
)

// fileAreaServer starts a BBS with one file in one area and the sysop on the
// main menu. content is every byte value a link can choke on, and then some.
func fileAreaServer(t *testing.T, links FileLinkConfig) (*client, []byte, *store.Store) {
	t.Helper()
	root := t.TempDir()
	content := make([]byte, 0, 300<<10)
	for i := 0; i < 256; i++ {
		content = append(content, byte(i), 0xff, 0x18, '\r', '\n', 0x11, 0x13)
	}
	rnd := rand.New(rand.NewSource(1))
	for len(content) < cap(content) {
		content = append(content, byte(rnd.Intn(256)))
	}
	if err := os.MkdirAll(filepath.Join(root, "nodelist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nodelist", "NODELIST.Z79"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Name: "Test BBS", MaxNodes: 4, IdleTimeout: time.Minute, Files: root, FileLinks: links}
	addr, st := startServer(t, cfg)
	mustCreateSysopAndUsers(t, st)
	if _, err := st.AddFile("NODELIST", "Nodelists", store.File{Name: "NODELIST.Z79",
		Path: filepath.Join("nodelist", "NODELIST.Z79"), Size: int64(len(content)), CRC32: "00000000",
		Description: "Weekly nodelist"}); err != nil {
		t.Fatal(err)
	}
	c := dial(t, addr, "root")
	c.enter("Root")
	c.key("F")
	c.expect("NODELIST")
	c.expect("Area #")
	c.line("1")
	c.expect("Weekly nodelist")
	c.expect("File #")
	c.line("1")
	c.expect("NODELIST.Z79")
	return c, content, st
}

// The real thing: lrzsz's rz receives the file over Telnet.
func TestZmodemDownloadWithRz(t *testing.T) {
	rz, err := exec.LookPath("rz")
	if err != nil {
		t.Skip("lrzsz's rz is not installed")
	}
	c, content, st := fileAreaServer(t, FileLinkConfig{})
	c.key("Z")
	c.expect("by ZMODEM")

	dir := t.TempDir()
	cmd := exec.Command(rz, "-b", "-y", "-q")
	cmd.Dir = dir
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Telnet in between: commands out of what rz reads, 0xFF doubled in
	// what it writes, as a Telnet client does.
	var (
		mu    sync.Mutex
		after bytes.Buffer
	)
	stop := make(chan struct{})
	bridged := make(chan struct{})
	go func() {
		defer close(bridged)
		defer stdin.Close()
		in := append([]byte(nil), c.buf.Bytes()...)
		c.buf.Reset()
		buf := make([]byte, 4096)
		for {
			if len(in) > 0 {
				data := stripTelnet(in)
				select {
				case <-stop:
					mu.Lock()
					after.Write(data)
					mu.Unlock()
				default:
					if _, err := stdin.Write(data); err != nil {
						mu.Lock()
						after.Write(data)
						mu.Unlock()
					}
				}
				in = in[:0]
			}
			_ = c.c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, err := c.c.Read(buf)
			in = append(in, buf[:n]...)
			if err != nil && n == 0 {
				select {
				case <-stop:
					return
				default:
				}
			}
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				_, _ = c.c.Write(bytes.ReplaceAll(buf[:n], []byte{telnet.IAC}, []byte{telnet.IAC, telnet.IAC}))
			}
			if err != nil {
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("rz: %v", err)
		}
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("rz did not finish")
	}
	time.Sleep(3 * time.Second) // the BBS settles and prints its result
	close(stop)
	<-bridged
	mu.Lock()
	defer mu.Unlock()

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("rz wrote %d files; after the transfer the BBS said %q", len(entries), after.String())
	}
	// rz lower-cases names that are all upper case, as it does for DOS files.
	got, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(entries[0].Name(), "NODELIST.Z79") {
		t.Errorf("received as %q", entries[0].Name())
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("received %d bytes, sent %d; they differ", len(got), len(content))
	}
	if !strings.Contains(after.String(), "Sent NODELIST.Z79") {
		t.Errorf("no success line after the transfer; got %q", after.String())
	}
	if f, _ := st.FileByID(1); f.Downloads != 1 {
		t.Errorf("downloads = %d", f.Downloads)
	}
}

// stripTelnet drops IAC commands (WILL/DO BINARY and the like) and undoes
// IAC IAC.
func stripTelnet(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != telnet.IAC || i+1 >= len(b) {
			out = append(out, b[i])
			continue
		}
		switch b[i+1] {
		case telnet.IAC:
			out = append(out, telnet.IAC)
			i++
		case telnet.WILL, telnet.WONT, telnet.DO, telnet.DONT:
			i += 2
		default:
			i++
		}
	}
	return out
}

func TestDownloadLink(t *testing.T) {
	key := bytes.Repeat([]byte("k"), 32)
	c, _, _ := fileAreaServer(t, FileLinkConfig{URL: "https://bbs.example/f/", Key: key, TTL: time.Hour})
	c.key("L")
	c.expect("on the web, until")
	text := c.expect("The link is yours")
	m := regexp.MustCompile(`https://bbs\.example/f/(\S+)`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no link in %q", text)
	}
	l, err := filelink.Verify(key, m[1], time.Now())
	if err != nil || l.FileID != 1 {
		t.Fatalf("link %q: %+v, %v", m[1], l, err)
	}
}

var _ = io.EOF
