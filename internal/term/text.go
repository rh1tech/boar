package term

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// StripControl removes control characters (including ESC, so callers cannot
// inject terminal sequences), bidi overrides and invalid UTF-8.
func StripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, s)
}

// Clean sanitises a single-line field: strips control characters, trims
// surrounding space and truncates to max runes.
func Clean(s string, max int) string {
	return Truncate(strings.TrimSpace(StripControl(s)), max)
}

// Truncate shortens s to at most n runes.
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Pad truncates or right-pads s with spaces to exactly n runes.
func Pad(s string, n int) string {
	count := utf8.RuneCountInString(s)
	if count >= n {
		return Truncate(s, n)
	}
	return s + strings.Repeat(" ", n-count)
}

// Wrap word-wraps text to lines of at most width runes. Existing newlines
// are kept; words longer than width are split.
func Wrap(text string, width int) []string {
	width = max(width, 1)
	var out []string
	for _, para := range strings.Split(text, "\n") {
		out = append(out, wrapLine(para, width)...)
	}
	return out
}

func wrapLine(line string, width int) []string {
	runes := []rune(line)
	var out []string
	for len(runes) > width {
		cut := width
		for i := width; i > 0; i-- {
			if runes[i] == ' ' {
				cut = i
				break
			}
		}
		out = append(out, strings.TrimRight(string(runes[:cut]), " "))
		runes = runes[cut:]
		for len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	return append(out, string(runes))
}
