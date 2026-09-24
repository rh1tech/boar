package term

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Pipe codes, as used by Renegade/Mystic-era BBS software:
//
//	|00-|15  foreground color (DOS palette, 8-15 are bright)
//	|16-|23  background color
//	|CL      clear screen
//	|RE      reset colors
//	||       a literal pipe
//
// Anything else after a pipe is printed as-is.

const (
	defaultFG   = 7
	defaultBG   = 0
	clearScreen = "\x1b[2J\x1b[H"
	resetSGR    = "\x1b[0m"
)

// dosToANSI maps DOS color order (blue=1, red=4) to ANSI order (red=1, blue=4).
var dosToANSI = [8]int{0, 4, 2, 6, 1, 5, 3, 7}

// Renderer expands pipe codes into ANSI escape sequences, or strips them when
// color is off. It remembers the active colors so that changing only the
// foreground keeps the current background.
type Renderer struct {
	color  bool
	fg, bg int
}

func NewRenderer(color bool) *Renderer {
	return &Renderer{color: color, fg: defaultFG, bg: defaultBG}
}

// Render expands every pipe code in s.
func (r *Renderer) Render(s string) string {
	if !strings.Contains(s, "|") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 32)
	for i := 0; i < len(s); {
		if s[i] != '|' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '|' {
			b.WriteByte('|')
			i += 2
			continue
		}
		if i+2 < len(s) {
			if seq, ok := r.code(s[i+1 : i+3]); ok {
				b.WriteString(seq)
				i += 3
				continue
			}
		}
		b.WriteByte('|')
		i++
	}
	return b.String()
}

func (r *Renderer) code(c string) (string, bool) {
	switch c {
	case "CL":
		if r.color {
			return clearScreen, true
		}
		return "\n", true
	case "RE":
		r.fg, r.bg = defaultFG, defaultBG
		if r.color {
			return resetSGR, true
		}
		return "", true
	}
	if c[0] < '0' || c[0] > '9' || c[1] < '0' || c[1] > '9' {
		return "", false
	}
	n := int(c[0]-'0')*10 + int(c[1]-'0')
	switch {
	case n <= 15:
		r.fg = n
	case n <= 23:
		r.bg = n - 16
	default:
		return "", false
	}
	if !r.color {
		return "", true
	}
	return r.sgr(), true
}

func (r *Renderer) sgr() string {
	bold := ""
	if r.fg >= 8 {
		bold = "1;"
	}
	return fmt.Sprintf("\x1b[0;%s3%d;4%dm", bold, dosToANSI[r.fg%8], dosToANSI[r.bg])
}

// Escape makes arbitrary text safe to embed in a pipe-coded string.
func Escape(s string) string {
	return strings.ReplaceAll(s, "|", "||")
}

// VisibleLen is the number of screen columns s occupies once its pipe codes
// are rendered (one per rune; |CL counts as a line break).
func VisibleLen(s string) int {
	return utf8.RuneCountInString(NewRenderer(false).Render(s))
}
