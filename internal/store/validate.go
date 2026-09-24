// SPDX-License-Identifier: GPL-3.0-or-later

package store

import "strings"

const (
	MinHandleLen   = 3
	MaxHandleLen   = 20
	MinPasswordLen = 8
	MaxPasswordLen = 64
	MaxLocationLen = 30
	MaxSubjectLen  = 60
	MaxBodyBytes   = 16 << 10
	MaxInbox       = 500

	MaxSearchResults = 100
	MinBoardNameLen  = 2
	MaxBoardNameLen  = 30
	MaxBoardDescLen  = 60
	MaxOnelinerLen   = 70
	MaxBoardPosts    = 500
)

var reservedHandles = map[string]bool{"new": true, "all": true, "sysop": true}

// ValidateHandle enforces handles that display well on every terminal:
// ASCII letters, digits, single spaces and _ - . starting with a letter.
func ValidateHandle(h string) error {
	if len(h) < MinHandleLen || len(h) > MaxHandleLen {
		return invalid("Handles must be %d-%d characters.", MinHandleLen, MaxHandleLen)
	}
	if !isLetter(h[0]) {
		return invalid("Handles must start with a letter.")
	}
	prevSpace := false
	for i := 0; i < len(h); i++ {
		c := h[i]
		switch {
		case isLetter(c) || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.':
			prevSpace = false
		case c == ' ':
			if prevSpace || i == len(h)-1 {
				return invalid("Handles cannot have double or trailing spaces.")
			}
			prevSpace = true
		default:
			return invalid("Handles may only use letters, digits, spaces and _ - .")
		}
	}
	if reservedHandles[strings.ToLower(h)] {
		return invalid("That handle is reserved.")
	}
	return nil
}

func ValidatePassword(p string) error {
	if len(p) < MinPasswordLen {
		return invalid("Passwords must be at least %d characters.", MinPasswordLen)
	}
	if len(p) > MaxPasswordLen {
		return invalid("Passwords must be at most %d characters.", MaxPasswordLen)
	}
	return nil
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func handleKey(h string) string { return strings.ToLower(strings.TrimSpace(h)) }
