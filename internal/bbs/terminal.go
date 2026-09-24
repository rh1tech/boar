// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"strings"

	"boar/internal/term"
)

// Terminal is a caller's connection, whatever the transport. ReadByte is
// called from one goroutine; Write and Close must be safe for concurrent use
// (the node table writes to terminals when kicking or shutting down).
type Terminal interface {
	ReadByte() (byte, error)
	Write(p []byte) (int, error)
	Size() (width, height int)
	Close() error
}

// termTyper is implemented by transports that learn the client's terminal
// type (SSH pty requests do).
type termTyper interface {
	TermType() string
}

// keyUser is implemented by transports that authenticated the caller
// themselves (SSH public keys).
type keyUser interface {
	KeyUserID() int64
}

// keyUserOf returns the account a transport already signed in, or 0.
func keyUserOf(t Terminal) int64 {
	if k, ok := t.(keyUser); ok {
		return k.KeyUserID()
	}
	return 0
}

// detectCharset guesses the charset and color mode from a terminal type.
// ok is false when the caller should be asked.
func detectCharset(termType string) (cs term.Charset, mode term.ColorMode, ok bool) {
	switch {
	case termType == "":
		return 0, term.NoColor, false
	case termType == "syncterm" || termType == "ansi-bbs" || termType == "pcansi" ||
		termType == "ansi" || termType == "scoansi":
		return term.CP437, term.ANSI16, true
	case termType == "dumb":
		return term.ASCII, term.NoColor, true
	case strings.Contains(termType, "256color") || strings.Contains(termType, "direct") ||
		strings.HasPrefix(termType, "xterm") || strings.HasPrefix(termType, "alacritty") ||
		strings.HasPrefix(termType, "wezterm") || strings.HasPrefix(termType, "kitty") ||
		termType == "tmux" || termType == "foot":
		return term.UTF8, term.ANSI256, true
	default: // linux console, vt100, screen: 16 colors
		return term.UTF8, term.ANSI16, true
	}
}

const (
	minTermWidth, maxTermWidth   = 20, 512
	minTermHeight, maxTermHeight = 5, 512
)

func clampSize(w, h int) (int, int) {
	return min(max(w, minTermWidth), maxTermWidth), min(max(h, minTermHeight), maxTermHeight)
}
