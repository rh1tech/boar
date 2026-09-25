// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"boar/internal/store"
)

func writeArt(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCustomArtReplacesScreens(t *testing.T) {
	dir := t.TempDir()
	// CP437 art: full blocks, red, a token, then SAUCE junk that must not show.
	writeArt(t, dir, "welcome.ans", "\xdb\xdb \x1b[31mNode @NODE@ of @BBS@\r\n\x1aSAUCE00 hidden")
	writeArt(t, dir, "logon.txt", "|14Hello @HANDLE@, call #@CALLS@!\n")
	writeArt(t, dir, "goodbye.txt", "|12See you, @HANDLE@.|RE\n")
	writeArt(t, dir, "unrelated.ans", "ignored")

	ts := startServerWith(t, Config{Name: "Boar|Net", ArtDir: dir}, nil)
	mustCreateSysopAndUsers(t, ts.st, "Alice")

	// A UTF-8 caller gets the art converted from CP437.
	c := dial(t, ts.addr, "utf8")
	c.expect("Choice [1]:")
	c.line("1")
	c.expect("██ \x1b[31mNode 1 of Boar|Net")
	if between := c.expect("Handle"); strings.Contains(between, "SAUCE") || strings.Contains(between, "hidden") {
		t.Fatalf("SAUCE record leaked: %q", between)
	}

	// A plain caller sees the text screens at logon and goodbye.
	p := dial(t, ts.addr, "plain")
	p.expect("Choice [1]:")
	p.line("3")
	p.expect("## Node 2 of Boar|Net")
	p.line("alice")
	p.expect("Password:")
	p.line("secret12")
	p.expect("Hello Alice, call #1!")
	p.expect("press any key")
	p.key(" ")
	p.expect("No new mail.")
	p.key(" ")
	p.skipWall()
	p.key("G")
	p.expect("Log off?")
	p.key("Y")
	p.expect("See you, Alice.")
}

func TestSysopArtPreview(t *testing.T) {
	dir := t.TempDir()
	writeArt(t, dir, "welcome.ans", "\xdb PREVIEW-ME")
	writeArt(t, dir, "welcome.2.txt", "second variant")
	ts := startServerWith(t, Config{ArtDir: dir}, nil)
	mustCreateSysopAndUsers(t, ts.st)

	c := dial(t, ts.addr, "root")
	c.expect("Choice [1]:")
	c.line("3")
	c.expect("Handle")
	c.line("root")
	c.expect("Password:")
	c.line("secret12")
	c.expect("No new mail.")
	c.key(" ")
	c.skipWall()
	c.key("!")
	c.expect("] Sysop")
	c.key("A")
	c.expect("welcome    2 variants")
	c.expect("goodbye    built-in")
	c.expect("Preview")
	c.line("2") // sorted: welcome.2.txt, welcome.ans
	c.expect("# PREVIEW-ME")
	c.expect("press any key")
}

func TestArtFilesIgnoresMissingFolder(t *testing.T) {
	if got := artFiles("", "welcome"); got != nil {
		t.Fatalf("got %v", got)
	}
	if got := artFiles(filepath.Join(t.TempDir(), "nope"), "welcome"); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestOversizedArtFallsBack(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, maxArtBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	writeArt(t, dir, "welcome.ans", string(big))
	ts := startServerWith(t, Config{ArtDir: dir}, nil)
	c := dial(t, ts.addr, "big")
	c.expect("Choice [1]:")
	c.line("3")
	c.expect("the wild boar") // built-in welcome
}

// The built-in signup screen states the rules the validator enforces, from the
// same constants: it once said six characters while signup demanded eight.
func TestNewUserScreenStatesTheRealLimits(t *testing.T) {
	addr, _ := startServer(t, Config{})
	c := dial(t, addr, "alice")
	c.connectPlain()
	c.line("new")
	c.expect(fmt.Sprintf("%d-%d characters", store.MinHandleLen, store.MaxHandleLen))
	c.expect(fmt.Sprintf("of at least %d characters", store.MinPasswordLen))
	c.expect("Choose a handle:")
}
