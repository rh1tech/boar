// SPDX-License-Identifier: GPL-3.0-or-later

package ftn

import (
	"strings"
	"testing"
	"time"
)

func TestRoutesHop(t *testing.T) {
	r := Routes{
		Direct: []Addr{MustParseAddr("2:5030/731"), MustParseAddr("2:5030/1651.1")},
		Via:    MustParseAddr("2:5030/1651.1"),
	}
	for dest, want := range map[string]string{
		"2:5030/731":    "2:5030/731",    // a direct link
		"2:5030/731.5":  "2:5030/731",    // a point of one: its boss
		"2:5030/1651.1": "2:5030/1651.1", // our own point is a direct link too
		"2:410/9":       "2:5030/1651.1", // everything else: via
		"1:153/757":     "2:5030/1651.1",
	} {
		hop, ok := r.Hop(MustParseAddr(dest))
		if !ok || hop.String() != want {
			t.Errorf("Hop(%s) = %s, %v; want %s", dest, hop, ok, want)
		}
	}
	if _, ok := (Routes{}).Hop(MustParseAddr("2:410/9")); ok {
		t.Error("no direct links and no via must mean no route")
	}
}

func TestAddViaKeepsTheMessage(t *testing.T) {
	pm := PackedMessage{Text: []byte("\x01INTL 2:410/9 2:5030/1651\rHello.\r\x00")}
	AddVia(&pm, MustParseAddr("2:410/51"), time.Date(2026, 10, 7, 9, 5, 6, 0, time.UTC), "boar-ftn")
	want := "\x01INTL 2:410/9 2:5030/1651\rHello.\r\x01Via 2:410/51 @20261007.090506.UTC boar-ftn\r"
	if string(pm.Text) != want {
		t.Fatalf("got %q", pm.Text)
	}
}

func TestIsNetmail(t *testing.T) {
	if (PackedMessage{Text: []byte("AREA:BINKD\rhi\r")}).IsNetmail() {
		t.Error("echomail taken for netmail")
	}
	if !(PackedMessage{Text: []byte("\x01INTL 2:1/1 2:1/2\rhi\r")}).IsNetmail() {
		t.Error("netmail not recognised")
	}
	if got, _ := ParseAddrList(" 2:410/51 , ,2:5030/1651.1"); len(got) != 2 || !strings.HasSuffix(got[1].String(), ".1") {
		t.Errorf("ParseAddrList = %v", got)
	}
}

func TestDefaultCharset(t *testing.T) {
	for addr, want := range map[string]Charset{
		"2:5030/731": CP866, "2:5020/715": CP866, "2:5099/1": CP866,
		"2:410/9": CP437, "2:5100/1": CP437, "1:5030/1": CP437,
	} {
		if got := DefaultCharset(MustParseAddr(addr)); got != want {
			t.Errorf("DefaultCharset(%s) = %v, want %v", addr, got, want)
		}
	}
	// A CHRS-less CP866 line from the hub reads as Russian.
	pm := PackedMessage{Text: Encode("Московский нодлист", CP866), To: []byte("Sysop")}
	m := ParseMessage(pm, MustParseAddr("2:5030/731"), MustParseAddr("2:5030/1651"), DefaultCharset(MustParseAddr("2:5030/731")))
	if m.Body != "Московский нодлист" {
		t.Errorf("body = %q", m.Body)
	}
}
