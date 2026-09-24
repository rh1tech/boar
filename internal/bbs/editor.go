// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"slices"
	"strings"
	"unicode"

	"boar/internal/store"
)

const (
	maxEditorLines = 200
	maxEditorWidth = 76
	editorGutter   = 5 // "123: "
)

var editorHelp = []string{
	"|07Type your message. Lines wrap by themselves. On an empty line type:",
	"|15/S|08 send   |15/A|08 abort   |15/L|08 list   |15/D|08 delete last line   |15/C|08 clear   |15/?|08 help",
}

func editorWidth(screen int) int { return min(screen-editorGutter, maxEditorWidth) }

// editor is a classic line editor. It returns ok=false if the caller aborts.
func (s *session) editor(initial []string) (string, bool, error) {
	width := editorWidth(s.width())
	lines := slices.Clone(initial)
	s.box("Editor", editorHelp...)
	s.listEditorLines(lines)

	var carry []rune
	for {
		s.printf(" %s%3d:%s ", colBorder, len(lines)+1, colLabel)
		line, next, err := s.readLine(lineOpts{max: width, wrap: true, init: carry})
		if err != nil {
			return "", false, err
		}
		carry = next
		if cmd, ok := editorCommand(line); ok && len(carry) == 0 {
			done, keep, newLines, err := s.editorAction(cmd, lines)
			if err != nil || done {
				return strings.Join(newLines, "\n"), keep, err
			}
			lines = newLines
			continue
		}
		if len(lines) >= maxEditorLines {
			s.printf("%sThe message is full (%d lines). Use /S to send.\n", colAlert, maxEditorLines)
			carry = nil
			continue
		}
		lines = append(lines, line)
	}
}

// editorAction runs a slash command. done ends the editor; keep says
// whether the text should be sent.
func (s *session) editorAction(cmd rune, lines []string) (done, keep bool, out []string, err error) {
	switch cmd {
	case 'S':
		body := strings.Join(lines, "\n")
		if strings.TrimSpace(body) == "" {
			s.printf("%sNothing to send yet.\n", colAlert)
			return false, false, lines, nil
		}
		if len(body) > store.MaxBodyBytes {
			s.printf("%sToo long: %d KB max. Delete some lines first.\n", colAlert, store.MaxBodyBytes>>10)
			return false, false, lines, nil
		}
		return true, true, lines, nil
	case 'A':
		abort, err := s.yesNo("Discard this message?", false)
		return abort, false, lines, err
	case 'L':
		s.print(s.rule() + "\n")
		s.listEditorLines(lines)
	case 'D':
		if len(lines) > 0 {
			lines = lines[:len(lines)-1]
			s.printf("%sLine %d deleted.\n", colDim, len(lines)+1)
		}
	case 'C':
		wipe, err := s.yesNo("Clear the whole message?", false)
		if err != nil {
			return false, false, lines, err
		}
		if wipe {
			lines = nil
		}
	default:
		s.box("Editor", editorHelp...)
	}
	return false, false, lines, nil
}

func (s *session) listEditorLines(lines []string) {
	for i, ln := range lines {
		s.printf(" %s%3d:%s %s\n", colBorder, i+1, colLabel, safe(ln))
	}
}

// editorCommand recognises "/x" typed alone on a line.
func editorCommand(line string) (rune, bool) {
	t := strings.TrimSpace(line)
	if len(t) != 2 || t[0] != '/' {
		return 0, false
	}
	cmd := unicode.ToUpper(rune(t[1]))
	if !strings.ContainsRune("SALDCH?", cmd) {
		return 0, false
	}
	return cmd, true
}
