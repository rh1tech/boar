// SPDX-License-Identifier: GPL-3.0-or-later

package ftn

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseAddr(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Addr
		out  string
	}{
		{"2:410/9", Addr{Zone: 2, Net: 410, Node: 9}, "2:410/9"},
		{"2:410/9.1", Addr{Zone: 2, Net: 410, Node: 9, Point: 1}, "2:410/9.1"},
		{"2:5020/1042.17@FidoNet", Addr{2, 5020, 1042, 17, "fidonet"}, "2:5020/1042.17"},
		{" 21:1/100 ", Addr{Zone: 21, Net: 1, Node: 100}, "21:1/100"},
	} {
		got, err := ParseAddr(tc.in)
		if err != nil || got != tc.want || got.String() != tc.out {
			t.Errorf("ParseAddr(%q) = %+v %v, String %q", tc.in, got, err, got.String())
		}
	}
	for _, bad := range []string{"", "2:410", "410/9", "0:1/1", "2:x/9", "2:410/9.x", "2:70000/1"} {
		if _, err := ParseAddr(bad); err == nil {
			t.Errorf("ParseAddr(%q) accepted", bad)
		}
	}
}

func TestCharsets(t *testing.T) {
	if len(cp866High) != 128 {
		t.Fatalf("CP866 table has %d entries, want 128", len(cp866High))
	}
	// Spot checks against code page 866.
	for b, want := range map[byte]rune{0x80: 'А', 0x9f: 'Я', 0xa0: 'а', 0xaf: 'п', 0xe0: 'р', 0xef: 'я', 0xf0: 'Ё', 0xf1: 'ё', 0xfc: '№', 0xb0: '░', 0xdb: '█', 0xc9: '╔'} {
		if got := []rune(Decode([]byte{b}, CP866))[0]; got != want {
			t.Errorf("CP866 %#x = %q, want %q", b, got, want)
		}
	}
	for _, tc := range []struct {
		cs   Charset
		text string
	}{
		{CP866, "Привет, Фидо! Ёлка №5 ╔═╗"},
		{CP437, "Boar ╔══╗ █▓▒░ ü é"},
		{Latin1, "café naïve"},
		{UTF8, "Θεσσαλονίκη → 2:410"},
	} {
		if !Fits(tc.text, tc.cs) {
			t.Errorf("%s cannot hold %q", tc.cs, tc.text)
		}
		if got := Decode(Encode(tc.text, tc.cs), tc.cs); got != tc.text {
			t.Errorf("%s round trip: %q → %q", tc.cs, tc.text, got)
		}
	}
	if Fits("Θεσσαλονίκη", CP866) || string(Encode("Θ", CP866)) != "?" {
		t.Error("Greek does not fit CP866 and must become ?")
	}
	for v, want := range map[string]Charset{"CP866 2": CP866, "+7_FIDO 2": CP866, "IBMPC 2": CP437, "UTF-8 4": UTF8, "latin-1 2": Latin1} {
		if got, ok := ParseCHRS(v); !ok || got != want {
			t.Errorf("ParseCHRS(%q) = %v %v", v, got, ok)
		}
	}
}

func TestDate(t *testing.T) {
	want := time.Date(2026, 9, 25, 7, 4, 5, 0, time.UTC)
	if s := FormatDate(want); s != "25 Sep 26  07:04:05" {
		t.Errorf("FormatDate = %q", s)
	}
	for _, s := range []string{"25 Sep 26  07:04:05", "25 Sep 26 07:04:05", " 5 Sep 26  07:04:05"} {
		got, ok := ParseDate(s)
		if !ok || got.Year() != 2026 || got.Month() != time.September || got.Hour() != 7 {
			t.Errorf("ParseDate(%q) = %v %v", s, got, ok)
		}
	}
}

func TestNetmailRoundTrip(t *testing.T) {
	from, to := MustParseAddr("2:410/9999.3"), MustParseAddr("2:5020/1042.17")
	sent := Message{
		From: "Mikhail Matveev", To: "Иван Петров", Subject: "Проверка связи",
		Date: time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC),
		Orig: from, Dest: to, Attr: AttrPrivate | AttrLocal,
		MsgID: "2:410/9999.3 5f3a9c01", Charset: CP866, TZUTC: "0300",
		Body:    "Привет!\nЭто тест.",
		Kludges: []Kludge{{"PID", "Boar BBS 1.0"}},
	}
	pkt := Packet{From: from.Boss(), To: MustParseAddr("2:410/0"), Password: "secret", Created: sent.Date,
		Messages: []PackedMessage{sent.Pack()}}
	var buf bytes.Buffer
	if _, err := pkt.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPacket(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !got.From.Same(pkt.From) || !got.To.Same(pkt.To) || got.Password != "SECRET" || len(got.Messages) != 1 {
		t.Fatalf("header: %+v", got)
	}
	m := ParseMessage(got.Messages[0], got.From, got.To, CP437)
	if !m.Orig.Same(from) || !m.Dest.Same(to) {
		t.Errorf("addresses: %s → %s", m.Orig, m.Dest)
	}
	if m.From != sent.From || m.To != sent.To || m.Subject != sent.Subject || m.Body != sent.Body {
		t.Errorf("text: %+v", m)
	}
	if m.Charset != CP866 || m.MsgID != sent.MsgID || m.TZUTC != "0300" || !m.Date.Equal(sent.Date) {
		t.Errorf("kludges: %+v", m)
	}
	if len(m.Kludges) != 1 || m.Kludges[0] != (Kludge{"PID", "Boar BBS 1.0"}) {
		t.Errorf("other kludges: %+v", m.Kludges)
	}
}

func TestEchomailRoundTrip(t *testing.T) {
	us := MustParseAddr("2:410/9")
	sent := Message{
		Area: "fido.test", From: "kasia", To: "All", Subject: "Hello",
		Date: time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC), Orig: us, Dest: MustParseAddr("2:410/0"),
		MsgID: "2:410/9 00000001", Charset: CP437,
		Body:   "First post.\n\n" + EchoFooter("Boar BBS", "Boar BBS, Thessaloniki", us),
		SeenBy: []Net2D{{410, 9}, {410, 0}, {5020, 1042}, {410, 9}},
		Path:   []Net2D{{410, 9}},
	}
	pm := sent.Pack()
	if !bytes.HasPrefix(pm.Text, []byte("AREA:fido.test\r")) {
		t.Fatalf("AREA line: %q", pm.Text[:20])
	}
	if !bytes.Contains(pm.Text, []byte("SEEN-BY: 410/0 9 5020/1042\r")) || !bytes.Contains(pm.Text, []byte("\x01PATH: 410/9\r")) {
		t.Errorf("SEEN-BY/PATH: %q", pm.Text)
	}
	m := ParseMessage(pm, us, MustParseAddr("2:410/0"), CP437)
	if m.Area != "FIDO.TEST" || !m.Orig.Same(us) {
		t.Errorf("area %q orig %s", m.Area, m.Orig)
	}
	if !strings.HasSuffix(m.Body, " * Origin: Boar BBS, Thessaloniki (2:410/9)") || strings.Contains(m.Body, "SEEN-BY") {
		t.Errorf("body: %q", m.Body)
	}
	if len(m.SeenBy) != 3 || len(m.Path) != 1 {
		t.Errorf("seen-by %v path %v", m.SeenBy, m.Path)
	}
}

func TestFormatNetsWraps(t *testing.T) {
	var nets []Net2D
	for i := 1; i <= 60; i++ {
		nets = append(nets, Net2D{5020, i * 10})
	}
	lines := FormatNets("SEEN-BY:", nets)
	if len(lines) < 2 {
		t.Fatal("did not wrap")
	}
	for _, l := range lines {
		if len(l) > 79 {
			t.Errorf("%d columns: %q", len(l), l)
		}
		if !strings.HasPrefix(l, "SEEN-BY: 5020/") {
			t.Errorf("a continued line must restate the net: %q", l)
		}
	}
	if got := appendNets(nil, strings.Join(lines, " ")[len("SEEN-BY:"):]); len(got) < 60 {
		t.Errorf("round trip lost entries: %d", len(got))
	}
}

func TestEchoFooterKeepsTheAddress(t *testing.T) {
	f := EchoFooter("Boar", strings.Repeat("x", 200), MustParseAddr("2:410/9"))
	origin := f[strings.Index(f, "\n")+1:]
	if len(origin) > 79 || !strings.HasSuffix(origin, "(2:410/9)") {
		t.Errorf("origin line: %d %q", len(origin), origin)
	}
}

// FSC-0048 packets from a point carry 0xFFFF as the net and the real net in
// AuxNet; they must read as the same address.
func TestReadsFSC0048PointHeader(t *testing.T) {
	var buf bytes.Buffer
	p := Packet{From: MustParseAddr("2:410/9.3"), To: MustParseAddr("2:410/9")}
	p.WriteTo(&buf)
	b := buf.Bytes()
	binary.LittleEndian.PutUint16(b[20:], 0xffff)
	binary.LittleEndian.PutUint16(b[38:], 410)
	got, err := ReadPacket(bytes.NewReader(b))
	if err != nil || !got.From.Same(MustParseAddr("2:410/9.3")) {
		t.Errorf("got %v %v", got.From, err)
	}
}

func TestRejectsGarbage(t *testing.T) {
	if _, err := ReadPacket(strings.NewReader("hello")); err == nil {
		t.Error("accepted a non-packet")
	}
	var buf bytes.Buffer
	(&Packet{From: MustParseAddr("2:1/1"), To: MustParseAddr("2:1/2")}).WriteTo(&buf)
	b := append(buf.Bytes()[:headerLen], 7, 0) // message type 7
	if _, err := ReadPacket(bytes.NewReader(b)); err == nil {
		t.Error("accepted a bad message type")
	}
}

func TestOutboundQueueAppends(t *testing.T) {
	root := t.TempDir() + "/out"
	o := Outbound{Root: root, DefaultZone: 2}
	dest := MustParseAddr("2:410/0")
	hdr := Packet{From: MustParseAddr("2:410/9999"), To: dest}
	msg := func(s string) PackedMessage {
		m := Message{From: "a", To: "b", Subject: s, Orig: hdr.From, Dest: dest, Charset: CP437, Body: s}
		return m.Pack()
	}
	for _, s := range []string{"one", "two"} {
		if err := o.Queue(dest, Crash, hdr, []PackedMessage{msg(s)}); err != nil {
			t.Fatal(err)
		}
	}
	path := o.PacketPath(dest, Crash)
	if !strings.HasSuffix(path, "/out/019a0000.cut") {
		t.Errorf("path %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := ReadPacket(f)
	if err != nil || len(p.Messages) != 2 {
		t.Fatalf("%v, %d messages", err, len(p.Messages))
	}
	if got := o.PacketPath(MustParseAddr("1:1/1.5"), Hold); !strings.HasSuffix(got, "/out.001/00010001.pnt/00000005.hut") {
		t.Errorf("point in another zone: %s", got)
	}
	if err := os.WriteFile(root+"/019a0000.bsy", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := o.Queue(dest, Crash, hdr, []PackedMessage{msg("three")}); err != ErrBusy {
		t.Errorf("queued past a busy flag: %v", err)
	}
}
