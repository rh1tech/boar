// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func play(t *testing.T, boar int, input string) string {
	t.Helper()
	var out bytes.Buffer
	g := &game{in: bufio.NewReader(strings.NewReader(input)), out: &out, name: "Kasia", pick: func() int { return boar }}
	if err := g.run(); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	return out.String()
}

func TestWinningHunt(t *testing.T) {
	out := play(t, 42, "50\r30\r4x\b2\rn\r")
	for _, want := range []string{"Welcome, \x1b[1;37mKasia", "south", "north", "found the boar at tree 42 in 3 guesses", "Good hunting, Kasia"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\n") && !strings.Contains(out, "\r\n") {
		t.Error("door output should use CR LF line endings")
	}
}

func TestLosingHuntAndBadInput(t *testing.T) {
	in := "abc\r" + strings.Repeat("1\r", maxTries) + "y\r100\rn\r"
	out := play(t, 100, in)
	for _, want := range []string{"not a tree", "slipped away. It was at tree 100", "found the boar at tree 100 in 1 guesses"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestHangUpEndsGame(t *testing.T) {
	var out bytes.Buffer
	g := &game{in: bufio.NewReader(strings.NewReader("5")), out: &out, name: "x", pick: func() int { return 1 }}
	if err := g.run(); err == nil {
		t.Fatal("expected EOF when the caller hangs up")
	}
}

func TestAliasFrom(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 52)
	lines[aliasLine-1] = "Kasia K."
	path := filepath.Join(dir, "DOOR.SYS")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\r\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := aliasFrom(path); got != "Kasia K." {
		t.Errorf("alias = %q", got)
	}
	if got := aliasFrom(filepath.Join(dir, "missing")); got != "stranger" {
		t.Errorf("missing file alias = %q", got)
	}
}
