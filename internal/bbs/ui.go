// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"fmt"
	"strings"
	"time"

	"boar/internal/term"
)

// The screen toolkit. Every screen is a framed title bar, then panels
// (bordered boxes with a title), then a bar of keys. Only glyphs CP437 has
// are used, so classic clients draw the same frames; plain ASCII terminals
// get +-| instead.

// Box glyphs.
const (
	dTL, dTR, dBL, dBR, dH, dV = "╔", "╗", "╚", "╝", "═", "║"
	sTL, sTR, sBL, sBR, sH, sV = "┌", "┐", "└", "┘", "─", "│"
	sLT, sRT                   = "├", "┤"
)

// separator marks a divider row inside a panel.
const separator = "\x00sep"

// Layout.
const (
	menuLabelWidth = 16 // label column in menus
	panelGap       = 1  // space between side-by-side panels
	panelPad       = 4  // "│ " + " │"
)

type menuItem struct {
	key   rune
	label string
	note  string // pipe-coded
}

// inner is the text width inside a panel of the given width.
func (s *session) inner(width int) int { return width - panelPad }

// fit truncates or pads a pipe-coded string to exactly width columns.
func fit(text string, width int) string {
	text = term.TruncateVisible(text, width)
	return text + strings.Repeat(" ", max(width-term.VisibleLen(text), 0))
}

// header clears the screen and draws the title frame:
//
//	╔══════════════════════════════════════════════════════╗
//	║ ■ Boar BBS  │ Main Menu               xtreme · 18:32 ║
//	╚══════════════════════════════════════════════════════╝
func (s *session) header(title string) {
	w := s.width()
	left := fmt.Sprintf(" %s■ %s%s  %s│ %s%s", colFrame, colTitle, safe(s.srv.cfg.Name), colBorder, colBright, safe(title))
	right := colLabel + time.Now().Format("15:04") + " "
	if s.user.Handle != "" {
		right = colInfo + safe(s.user.Handle) + colBorder + " · " + right
	}
	gap := w - 2 - term.VisibleLen(left) - term.VisibleLen(right)
	if gap < 1 {
		left = term.TruncateVisible(left, max(w-3-term.VisibleLen(right), 0))
		gap = w - 2 - term.VisibleLen(left) - term.VisibleLen(right)
	}
	s.print("|CL|RE")
	s.print(colFrame + dTL + strings.Repeat(dH, w-2) + dTR + "\n")
	s.print(colFrame + dV + left + strings.Repeat(" ", max(gap, 0)) + right + colFrame + dV + "\n")
	s.print(colFrame + dBL + strings.Repeat(dH, w-2) + dBR + colLabel + "\n")
}

// rule is a thin divider across the screen.
func (s *session) rule() string {
	return colBorder + strings.Repeat(sH, s.width()) + colLabel
}

// boxLines renders a panel. Body lines are pipe-coded; the separator line
// draws a divider.
func (s *session) boxLines(title string, width int, body []string) []string {
	out := make([]string, 0, len(body)+2)
	out = append(out, s.boxTop(title, width))
	for _, ln := range body {
		out = append(out, s.boxRow(ln, width))
	}
	return append(out, s.boxBottom(width))
}

func (s *session) boxTop(title string, width int) string {
	if title == "" {
		return colBorder + sTL + strings.Repeat(sH, width-2) + sTR
	}
	head := colBorder + sTL + sH + " " + colTitle + safe(title) + " "
	fill := width - 1 - term.VisibleLen(head)
	return head + colBorder + strings.Repeat(sH, max(fill, 0)) + sTR
}

func (s *session) boxRow(line string, width int) string {
	if line == separator {
		return colBorder + sLT + strings.Repeat(sH, width-2) + sRT
	}
	return colBorder + sV + " " + colLabel + fit(line, s.inner(width)) + "|16" + colBorder + " " + sV
}

func (s *session) boxBottom(width int) string {
	return colBorder + sBL + strings.Repeat(sH, width-2) + sBR + colLabel
}

// box prints a full-width panel.
func (s *session) box(title string, body ...string) {
	for _, ln := range s.boxLines(title, s.width(), body) {
		s.print(ln + "\n")
	}
}

// table prints a full-width panel with a heading row and paged rows. When
// rows is empty it shows the empty message instead.
func (s *session) table(title, heading string, rows []string, empty string) error {
	w := s.width()
	s.print(s.boxTop(title, w) + "\n")
	if len(rows) == 0 {
		s.print(s.boxRow("", w) + "\n" + s.boxRow("  "+colDim+empty, w) + "\n" + s.boxRow("", w) + "\n")
		s.print(s.boxBottom(w) + "\n")
		return nil
	}
	if heading != "" {
		s.print(s.boxRow(colDim+heading, w) + "\n" + s.boxRow(separator, w) + "\n")
	}
	framed := make([]string, 0, len(rows)+1)
	for _, r := range rows {
		framed = append(framed, s.boxRow(r, w))
	}
	framed = append(framed, s.boxBottom(w))
	_, err := s.page(framed, headerRows+3)
	return err
}

// headerRows is how many rows header takes, for paging.
const headerRows = 3

// keycap draws a key as a lit button, or [K] without color.
func (s *session) keycap(k rune) string {
	if s.color {
		return fmt.Sprintf("|22|15 %c |16", k)
	}
	return fmt.Sprintf("[%c]", k)
}

func (s *session) menuLine(m menuItem) string {
	if m.key == 0 {
		return ""
	}
	return s.keycap(m.key) + " " + colLabel + term.Pad(m.label, menuLabelWidth) + " " + m.note
}

// menu prints a full-width panel of menu items.
func (s *session) menu(title string, items []menuItem) {
	lines := make([]string, 0, len(items))
	for _, m := range items {
		lines = append(lines, s.menuLine(m))
	}
	s.box(title, lines...)
}

// menuColumns prints two titled panels side by side.
func (s *session) menuColumns(leftTitle string, left []menuItem, rightTitle string, right []menuItem) {
	w := s.width()
	lw := (w - panelGap) / 2
	rw := w - panelGap - lw
	rows := max(len(left), len(right))
	col := func(items []menuItem) []string {
		out := make([]string, rows)
		for i := range out {
			if i < len(items) {
				out[i] = s.menuLine(items[i])
			}
		}
		return out
	}
	a := s.boxLines(leftTitle, lw, col(left))
	b := s.boxLines(rightTitle, rw, col(right))
	for i := range a {
		s.print(a[i] + strings.Repeat(" ", panelGap) + b[i] + "\n")
	}
}

// actions renders a bar of keys, one per word, using each word's first
// letter as its key: "Reply" and "Next" become the buttons R Reply and
// N Next. The bar wraps between keys so it never runs past the screen edge.
func (s *session) actions(words ...string) string {
	limit := s.width() - 4 // room for the "» " prompt
	var b strings.Builder
	b.WriteString(" ")
	col := 1
	for i, w := range words {
		part := s.keycap(rune(strings.ToUpper(w[:1])[0])) + " " + colLabel + w
		width := term.VisibleLen(part)
		if i > 0 {
			if col+2+width > limit {
				b.WriteString("\n ")
				col = 1
			} else {
				b.WriteString("  ")
				col += 2
			}
		}
		b.WriteString(part)
		col += width
	}
	return b.String()
}

// actionPrompt shows a bar of keys and waits for one of them (or Enter,
// when extra includes "\r").
func (s *session) actionPrompt(extra string, words ...string) (rune, error) {
	s.flushNotices()
	s.print("\n" + s.actions(words...) + " " + colBorder + "» |15")
	return s.choose(keysOf(words...) + extra)
}

// menuPrompt shows pending notices, then waits for one of keys.
func (s *session) menuPrompt(where, keys string) (rune, error) {
	s.flushNotices()
	s.printf("\n %s[%s%s%s] %s%s %s» |15", colBorder, colFrame, safe(s.user.Handle), colBorder, colLabel, where, colBorder)
	return s.choose(keys)
}

// showLetter draws a letter in a panel: its fields, a divider, then the
// body, paged.
func (s *session) showLetter(l letter) error {
	s.header(l.title)
	w := s.width()
	s.print(s.boxTop("", w) + "\n")
	for _, f := range l.fields {
		s.print(s.boxRow(fmt.Sprintf("%s%s %s: %s%s", colInfo, term.Pad(f.label, 4), colBorder, f.color, safe(f.value)), w) + "\n")
	}
	s.print(s.boxRow(separator, w) + "\n")
	body := s.bodyLines(l.body)
	framed := make([]string, 0, len(body)+1)
	for _, ln := range body {
		framed = append(framed, s.boxRow(ln, w))
	}
	framed = append(framed, s.boxBottom(w))
	_, err := s.page(framed, headerRows+len(l.fields)+2)
	return err
}

// pauseText is the "press any key" line.
func (s *session) pauseText() string {
	return " " + colBorder + sH + sH + " " + colLabel + "press any key" + colBorder + " " + sH + sH
}

// note prints a short message under the panels, colored by kind.
func (s *session) note(color, format string, args ...any) {
	s.printf("\n "+color+format+"\n", args...)
}

// keyBar is a row of keys that don't fit a panel (Settings, Goodbye).
func (s *session) keyBar(items []menuItem) string {
	parts := make([]string, 0, len(items))
	for _, m := range items {
		part := s.keycap(m.key) + " " + colLabel + m.label
		if m.note != "" {
			part += " " + m.note
		}
		parts = append(parts, part)
	}
	return " " + strings.Join(parts, "   ")
}
