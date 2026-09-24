// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"boar/internal/term"
)

// Every screen goes to CP437 clients too, so every non-ASCII glyph in the
// BBS's own source must exist in CP437.
func TestScreensOnlyUseCP437Glyphs(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range string(src) {
			if r > 127 && string(term.Encode(term.CP437, string(r))) == "?" {
				t.Errorf("%s uses %q, which CP437 terminals can't show", f, r)
			}
		}
	}
}

func TestActionBarWrapsAtScreenEdge(t *testing.T) {
	s, _ := pipeSession(t, term.ASCII, "")
	bar := s.actions("Mail", "Password", "Lock", "Sysop rights", "Delete", "Forward", "Thread", "Quit")
	for _, line := range strings.Split(bar, "\n") {
		if n := term.VisibleLen(line); n > s.width()-4 {
			t.Errorf("bar line is %d columns: %q", n, line)
		}
	}
	if !strings.Contains(bar, "\n") {
		t.Fatal("a long bar should wrap")
	}
}

func TestBoxRowsAreExactlyScreenWidth(t *testing.T) {
	s, _ := pipeSession(t, term.ASCII, "")
	w := s.width()
	for _, ln := range s.boxLines("Title", w, []string{"short", strings.Repeat("x", 200), "|14colored||pipe", separator}) {
		if n := term.VisibleLen(ln); n != w {
			t.Errorf("row is %d columns, want %d: %q", n, w, ln)
		}
	}
}
