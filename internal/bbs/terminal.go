package bbs

import "boar/internal/term"

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

// detectCharset guesses the charset from a terminal type. ok is false when
// the caller should be asked.
func detectCharset(termType string) (cs term.Charset, color bool, ok bool) {
	switch termType {
	case "":
		return 0, false, false
	case "syncterm", "ansi-bbs", "pcansi", "ansi", "scoansi":
		return term.CP437, true, true
	case "dumb":
		return term.ASCII, false, true
	default: // xterm-256color, screen, tmux, vt100, linux ...
		return term.UTF8, true, true
	}
}

const (
	minTermWidth, maxTermWidth   = 20, 512
	minTermHeight, maxTermHeight = 5, 512
)

func clampSize(w, h int) (int, int) {
	return min(max(w, minTermWidth), maxTermWidth), min(max(h, minTermHeight), maxTermHeight)
}
