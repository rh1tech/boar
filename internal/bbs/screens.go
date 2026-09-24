package bbs

import (
	"embed"
	"fmt"
	"strings"
	"time"

	"boar/internal/term"
)

//go:embed art/*.ans
var artFS embed.FS

// Palette, as pipe codes. Brown and amber for the boar, cyan for facts,
// dark gray for structure.
const (
	colFrame  = "|06" // the title frame
	colBorder = "|08" // panel borders
	colTitle  = "|14" // panel titles and the BBS name
	colKey    = "|14"
	colLabel  = "|07"
	colDim    = "|08"
	colAlert  = "|12"
	colOK     = "|10"
	colInfo   = "|03"
	colValue  = "|11"
	colHandle = "|14"
	colBright = "|15"
	colSysop  = "|13"
)

var logoRows = []string{
	"██████╗  ██████╗  █████╗ ██████╗ ",
	"██╔══██╗██╔═══██╗██╔══██╗██╔══██╗",
	"██████╔╝██║   ██║███████║██████╔╝",
	"██╔══██╗██║   ██║██╔══██║██╔══██╗",
	"██████╔╝╚██████╔╝██║  ██║██║  ██║",
	"╚═════╝  ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝",
}

// logoShades fades the solid blocks from white-hot to boar brown.
var logoShades = []int{15, 14, 14, 6, 6, 6}

const logoShadow = 8

// logo returns the BOAR banner in pipe codes: solid blocks get the row's
// shade, the box-drawing "shadow" is dark gray.
func logo(indent int) string {
	var b strings.Builder
	pad := strings.Repeat(" ", indent)
	for i, row := range logoRows {
		b.WriteString(pad)
		cur := -1
		for _, r := range row {
			c := logoShadow
			switch r {
			case '█':
				c = logoShades[i]
			case ' ':
				c = cur
			}
			if c != cur && c >= 0 {
				fmt.Fprintf(&b, "|%02d", c)
				cur = c
			}
			b.WriteRune(r)
		}
		b.WriteString("\n")
	}
	return b.String() + colLabel
}

// showArt prints an embedded screen, substituting @KEY@ tokens with escaped
// values.
func (s *session) showArt(name string, vars map[string]string) error {
	data, err := artFS.ReadFile("art/" + name + ".ans")
	if err != nil {
		return fmt.Errorf("art %q: %w", name, err)
	}
	text := string(data)
	for k, v := range vars {
		text = strings.ReplaceAll(text, "@"+k+"@", safe(v))
	}
	s.print(text)
	return nil
}

// letter is anything shown like a message: mail, board posts, bulletins.
type letter struct {
	title  string
	fields []field
	body   string
}

type field struct {
	label string
	value string // plain text, escaped on display
	color string
}

// bodyLines wraps a body for display, tinting quoted lines.
func (s *session) bodyLines(body string) []string {
	var out []string
	for _, ln := range term.Wrap(body, s.inner(s.width())) {
		color := colLabel
		if strings.HasPrefix(strings.TrimSpace(ln), ">") {
			color = colInfo
		}
		out = append(out, color+safe(ln))
	}
	return out
}

// reportError tells the caller what went wrong. Validation problems are
// shown verbatim; anything else is logged and summarised.
func (s *session) reportError(action string, err error) error {
	if userFacing(err) {
		s.printf("\n%s%s\n", colAlert, safe(err.Error()))
	} else {
		s.srv.log.Error(action, "err", err, "user", s.user.Handle)
		s.printf("\n%sSomething went wrong. Please try again later.\n", colAlert)
	}
	return s.pause()
}

// keysOf returns the upper-case first letters of words.
func keysOf(words ...string) string {
	var b strings.Builder
	for _, w := range words {
		b.WriteString(strings.ToUpper(w[:1]))
	}
	return b.String()
}

func shortDate(t time.Time) string {
	t = t.Local()
	if t.Year() == time.Now().Year() {
		return t.Format("Jan 02")
	}
	return t.Format("2006-01")
}

func longDate(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format("Mon Jan 2 2006 15:04")
}

// ago formats a time as "5m ago", "3h ago" or a date.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return shortDate(t)
	}
}

func shortDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
