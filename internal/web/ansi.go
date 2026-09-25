// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"html/template"
	"strconv"
	"strings"

	"boar/internal/term"
)

// A small ANSI.SYS interpreter, for previewing custom art in the browser.
//
// It draws onto an 80-column grid the way a BBS terminal would: SGR colour
// (bold lifts the foreground to the bright half of the VGA palette), cursor
// movement, clears and erases. Anything else is ignored. The grid is then
// written out as runs of <span>s whose classes pick VGA colours from the
// stylesheet, so the page never needs inline styles and the CSP can stay
// strict.

const (
	artColumns = 80
	artMaxRows = 500
)

type cell struct {
	r      rune
	fg, bg int // 0-15 and 0-7, in ANSI order (1 = red, 4 = blue)
}

type screen struct {
	rows   [][]cell
	x, y   int
	fg, bg int
	bold   bool
	saveX  int
	saveY  int
}

func newScreen() *screen { return &screen{fg: 7} }

func (s *screen) row(y int) []cell {
	for len(s.rows) <= y && len(s.rows) < artMaxRows {
		s.rows = append(s.rows, nil)
	}
	if y >= len(s.rows) {
		return nil
	}
	return s.rows[y]
}

func (s *screen) put(r rune) {
	if s.x >= artColumns {
		s.x = 0
		s.y++
	}
	row := s.row(s.y)
	if s.y >= artMaxRows {
		return
	}
	for len(row) <= s.x {
		row = append(row, cell{r: ' ', fg: 7})
	}
	fg := s.fg
	if s.bold && fg < 8 {
		fg += 8
	}
	row[s.x] = cell{r: r, fg: fg, bg: s.bg}
	s.rows[s.y] = row
	s.x++
}

func (s *screen) sgr(params []int) {
	if len(params) == 0 {
		params = []int{0}
	}
	for _, p := range params {
		switch {
		case p == 0:
			s.fg, s.bg, s.bold = 7, 0, false
		case p == 1:
			s.bold = true
		case p == 22:
			s.bold = false
		case p >= 30 && p <= 37:
			s.fg = p - 30
		case p == 39:
			s.fg = 7
		case p >= 40 && p <= 47:
			s.bg = p - 40
		case p == 49:
			s.bg = 0
		case p >= 90 && p <= 97:
			s.fg = p - 90 + 8
		}
	}
}

func (s *screen) csi(final byte, params []int) {
	n := 1
	if len(params) > 0 && params[0] > 0 {
		n = params[0]
	}
	switch final {
	case 'm':
		s.sgr(params)
	case 'A':
		s.y = max(0, s.y-n)
	case 'B':
		s.y += n
	case 'C':
		s.x = min(artColumns-1, s.x+n)
	case 'D':
		s.x = max(0, s.x-n)
	case 'H', 'f':
		row, col := 1, 1
		if len(params) > 0 && params[0] > 0 {
			row = params[0]
		}
		if len(params) > 1 && params[1] > 0 {
			col = params[1]
		}
		s.y, s.x = row-1, min(artColumns-1, col-1)
	case 'J':
		if len(params) > 0 && params[0] == 2 {
			s.rows, s.x, s.y = nil, 0, 0
		}
	case 'K':
		if s.y < len(s.rows) && s.x < len(s.rows[s.y]) {
			s.rows[s.y] = s.rows[s.y][:s.x]
		}
	case 's':
		s.saveX, s.saveY = s.x, s.y
	case 'u':
		s.x, s.y = s.saveX, s.saveY
	}
}

// feed interprets UTF-8 text with ANSI escapes.
func (s *screen) feed(text string) {
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == 0x1b && i+1 < len(text) && text[i+1] == '[':
			j := i + 2
			for j < len(text) && (text[j] < 0x40 || text[j] > 0x7e) {
				j++
			}
			if j >= len(text) {
				return
			}
			var params []int
			for _, p := range strings.Split(strings.TrimLeft(text[i+2:j], "?"), ";") {
				v, err := strconv.Atoi(p)
				if err != nil {
					v = 0
				}
				params = append(params, v)
			}
			s.csi(text[j], params)
			i = j + 1
			continue
		case c == '\r':
			s.x = 0
		case c == '\n':
			s.x, s.y = 0, s.y+1
		case c < 0x20:
			// other controls draw nothing
		default:
			r, size := decodeRune(text[i:])
			s.put(r)
			i += size
			continue
		}
		i++
	}
}

func decodeRune(s string) (rune, int) {
	for i, r := range s {
		if i == 0 {
			return r, len(string(r))
		}
	}
	return ' ', 1
}

// html writes the grid as coloured runs.
func (s *screen) html() template.HTML {
	var b strings.Builder
	last := len(s.rows)
	for last > 0 && len(s.rows[last-1]) == 0 {
		last--
	}
	for y := 0; y < last; y++ {
		row := s.rows[y]
		for x := 0; x < len(row); {
			start := x
			for x < len(row) && row[x].fg == row[start].fg && row[x].bg == row[start].bg {
				x++
			}
			b.WriteString(`<span class="f`)
			b.WriteString(strconv.Itoa(row[start].fg))
			b.WriteString(` b`)
			b.WriteString(strconv.Itoa(row[start].bg))
			b.WriteString(`">`)
			for _, c := range row[start:x] {
				b.WriteString(template.HTMLEscapeString(string(c.r)))
			}
			b.WriteString(`</span>`)
		}
		b.WriteByte('\n')
	}
	return template.HTML(b.String()) // every rune above was escaped
}

// renderANS previews a CP437 .ans file.
func renderANS(data []byte) template.HTML {
	s := newScreen()
	s.feed(string(term.ConvertANSIArt(data, term.UTF8, true)))
	return s.html()
}

// renderPipeText previews a .txt screen written with pipe colour codes.
func renderPipeText(text string) template.HTML {
	s := newScreen()
	s.feed(term.NewRenderer(term.ANSI16).Render(text))
	return s.html()
}
