package bbs

import (
	"embed"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"boar/internal/term"
)

//go:embed art/*.ans
var artFS embed.FS

// Palette, as pipe codes. Brown and yellow for the boar.
const (
	colFrame   = "|06"
	colBarBG   = "|22"
	colBarText = "|15"
	colBarDim  = "|14"
	colKey     = "|14"
	colLabel   = "|07"
	colDim     = "|08"
	colAlert   = "|12"
	colOK      = "|10"
	colInfo    = "|03"
	colValue   = "|11"
	colHandle  = "|14"
	colBright  = "|15"
	colSysop   = "|13"
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

// header clears the screen and draws the title bar: a brown slab with
// half-block top and bottom edges.
func (s *session) header(title string) {
	w := s.width()
	clock := time.Now().Format("15:04")
	left := " ▓ " + s.srv.cfg.Name + " » " + title
	gap := max(w-utf8.RuneCountInString(left)-utf8.RuneCountInString(clock)-1, 1)

	s.print("|CL")
	s.print(colFrame + strings.Repeat("▄", w) + "\n")
	s.printf("%s%s ▓ %s%s %s» %s%s%s%s%s |16\n",
		colBarBG, colBarDim, colBarText, safe(s.srv.cfg.Name), colBarDim,
		colBarText, safe(title), strings.Repeat(" ", gap), colBarDim, clock)
	s.print(colFrame + strings.Repeat("▀", w) + colLabel + "\n")
}

func (s *session) rule() string {
	return colDim + strings.Repeat("─", s.width()) + colLabel
}

// section prints a small "── Title ─────" divider of the given width.
func section(title string, width int) string {
	fill := max(width-utf8.RuneCountInString(title)-4, 2)
	return fmt.Sprintf("%s── %s%s %s%s", colDim, colLabel, title, colDim, strings.Repeat("─", fill))
}

type menuItem struct {
	key   rune
	label string
	note  string // pipe-coded
}

const (
	menuLabelWidth = 16
	menuColumn     = 38
)

func (m menuItem) render() string {
	if m.key == 0 {
		return ""
	}
	return fmt.Sprintf("%s[%s%c%s] %s%s %s", colDim, colKey, m.key, colDim, colLabel, term.Pad(m.label, menuLabelWidth), m.note)
}

// item prints one single-column menu entry.
func (s *session) item(key rune, label, note string) {
	s.print("   " + menuItem{key, label, note}.render() + "\n")
}

// menuColumns prints two titled columns of menu items side by side.
func (s *session) menuColumns(leftTitle string, left []menuItem, rightTitle string, right []menuItem) {
	s.printf("\n  %s  %s\n", padVisible(section(leftTitle, menuColumn-2), menuColumn-1), section(rightTitle, menuColumn-2))
	for i := range max(len(left), len(right)) {
		var l, r string
		if i < len(left) {
			l = left[i].render()
		}
		if i < len(right) {
			r = right[i].render()
		}
		s.printf("   %s  %s\n", padVisible(l, menuColumn-2), r)
	}
}

// padVisible right-pads a pipe-coded string to width visible columns.
func padVisible(s string, width int) string {
	return s + strings.Repeat(" ", max(width-term.VisibleLen(s), 0))
}

// menuPrompt shows pending notices, then waits for one of keys.
func (s *session) menuPrompt(where, keys string) (rune, error) {
	s.flushNotices()
	s.printf("\n%s[|06%s%s] %s%s %s» |15", colDim, safe(s.user.Handle), colDim, colLabel, where, colDim)
	return s.choose(keys)
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

// showLetter draws a letter, paging its body.
func (s *session) showLetter(l letter) error {
	s.header(l.title)
	s.print("\n")
	for _, f := range l.fields {
		s.printf(" %s%s %s: %s%s\n", colInfo, term.Pad(f.label, 4), colDim, f.color, safe(f.value))
	}
	s.print(s.rule() + "\n")
	used := 5 + len(l.fields)
	if _, err := s.page(s.bodyLines(l.body), used); err != nil {
		return err
	}
	s.print(s.rule() + "\n")
	return nil
}

// bodyLines wraps a body for display, tinting quoted lines.
func (s *session) bodyLines(body string) []string {
	var out []string
	for _, ln := range term.Wrap(body, s.width()-1) {
		color := colLabel
		if strings.HasPrefix(strings.TrimSpace(ln), ">") {
			color = colInfo
		}
		out = append(out, " "+color+safe(ln))
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

// actions renders "[R]eply  [N]ext ..." from words whose first letter is the key.
func actions(words ...string) string {
	parts := make([]string, 0, len(words))
	for _, w := range words {
		parts = append(parts, fmt.Sprintf("%s[%s%c%s]%s", colDim, colBright, w[0], colDim, w[1:]))
	}
	return strings.Join(parts, "  ")
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
