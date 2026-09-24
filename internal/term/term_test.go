package term

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestCP437TableRoundTrips(t *testing.T) {
	for b := 0x80; b <= 0xFF; b++ {
		r := DecodeCP437(byte(b))
		got := Encode(CP437, string(r))
		if !bytes.Equal(got, []byte{byte(b)}) {
			t.Fatalf("byte %#x -> %q -> %v", b, r, got)
		}
	}
}

func TestEncodeCP437BoxDrawing(t *testing.T) {
	got := Encode(CP437, "█═║░A")
	want := []byte{0xDB, 0xCD, 0xBA, 0xB0, 'A'}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEncodeFallbacks(t *testing.T) {
	cases := []struct {
		cs   Charset
		in   string
		want string
	}{
		{ASCII, "╔══╗ █ »", "+==+ # >"},
		{ASCII, "zażółć", "za????"},
		{CP437, "…", "."},
		{UTF8, "zażółć", "zażółć"},
	}
	for _, c := range cases {
		if got := string(Encode(c.cs, c.in)); got != c.want {
			t.Errorf("Encode(%v, %q) = %q, want %q", c.cs, c.in, got, c.want)
		}
	}
}

func TestRenderColor(t *testing.T) {
	r := NewRenderer(true)
	got := r.Render("|14Hi|20!|03x")
	want := "\x1b[0;1;33;40mHi\x1b[0;1;33;41m!\x1b[0;36;41mx"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderPlainStripsCodes(t *testing.T) {
	r := NewRenderer(false)
	cases := map[string]string{
		"|14Hello|07 world": "Hello world",
		"a||b":              "a|b",
		"|99x":              "|99x",
		"trailing |":        "trailing |",
		"|1":                "|1",
		"|CLtop|RE":         "\ntop",
		"pipe |xyz":         "pipe |xyz",
	}
	for in, want := range cases {
		if got := r.Render(in); got != want {
			t.Errorf("Render(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEscapeSurvivesRender(t *testing.T) {
	evil := "|CL|04boom||"
	got := NewRenderer(true).Render(Escape(evil))
	if got != evil {
		t.Fatalf("escaped text rendered as %q", got)
	}
}

func TestStripControl(t *testing.T) {
	in := "hi\x1b[2J‮there\x07\xff!"
	if got := StripControl(in); got != "hi[2Jthere!" {
		t.Fatalf("got %q", got)
	}
}

func TestCleanTrimsAndTruncates(t *testing.T) {
	if got := Clean("  żółw\tabc  ", 5); got != "żółwa" {
		t.Fatalf("got %q", got)
	}
}

func TestPad(t *testing.T) {
	if got := Pad("ab", 4); got != "ab  " {
		t.Errorf("got %q", got)
	}
	if got := Pad("żółwik", 3); got != "żół" {
		t.Errorf("got %q", got)
	}
}

func TestWrap(t *testing.T) {
	got := Wrap("the quick brown fox\nabcdefghij", 9)
	want := []string{"the quick", "brown fox", "abcdefghi", "j"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWrapKeepsBlankLines(t *testing.T) {
	got := Wrap("a\n\nb", 10)
	if !reflect.DeepEqual(got, []string{"a", "", "b"}) {
		t.Fatalf("got %q", got)
	}
}

func TestVisibleLen(t *testing.T) {
	if got := VisibleLen("|14ab|08||c"); got != 4 {
		t.Fatalf("VisibleLen = %d, want 4", got)
	}
}

func TestConvertANSIArt(t *testing.T) {
	// "█" in CP437, red, cursor forward 3, a bell, LF-only line end, SAUCE.
	art := []byte("\xdb\x1b[31mA\x1b[3CB\x07\nC\x1aSAUCE00junk")
	cases := []struct {
		cs    Charset
		color bool
		want  string
	}{
		{CP437, true, "\xdb\x1b[31mA\x1b[3CB\r\nC\x1b[0m"},
		{UTF8, true, "█\x1b[31mA\x1b[3CB\r\nC\x1b[0m"},
		{ASCII, false, "#A   B\r\nC"},
	}
	for _, c := range cases {
		if got := string(ConvertANSIArt(art, c.cs, c.color)); got != c.want {
			t.Errorf("%v color=%v: got %q, want %q", c.cs, c.color, got, c.want)
		}
	}
}

func TestConvertANSIArtEdgeCases(t *testing.T) {
	if got := string(ConvertANSIArt([]byte("x\x1b[1"), UTF8, false)); got != "x" {
		t.Errorf("truncated CSI: %q", got)
	}
	if got := string(ConvertANSIArt([]byte("\x1b[C|\x1b[9999C|"), ASCII, false)); got != " |"+strings.Repeat(" ", maxCursorShift)+"|" {
		t.Errorf("cursor forward: %q", got)
	}
	if got := string(ConvertANSIArt([]byte("a\r\nb"), UTF8, false)); got != "a\r\nb" {
		t.Errorf("CRLF kept: %q", got)
	}
}

func TestTranscodeCP437(t *testing.T) {
	in := []byte("\x1b[1m\xdb\x82!")
	if got := string(TranscodeCP437(in, CP437)); got != string(in) {
		t.Errorf("CP437: %q", got)
	}
	if got := string(TranscodeCP437(in, UTF8)); got != "\x1b[1m█é!" {
		t.Errorf("UTF8: %q", got)
	}
	if got := string(TranscodeCP437(in, ASCII)); got != "\x1b[1m#?!" {
		t.Errorf("ASCII: %q", got)
	}
}
