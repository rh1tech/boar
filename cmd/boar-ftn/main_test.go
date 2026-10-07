// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"boar/internal/ftn"
)

// A bundle relayed from rbx1 to spb1 carries echomail and both nodes'
// netmail; each node must announce only its own.
func TestDescribeOnlyOwnNetmail(t *testing.T) {
	uplink := ftn.MustParseAddr("2:410/9")
	gr, ru := ftn.MustParseAddr("2:410/51"), ftn.MustParseAddr("2:5030/1651")
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	netmail := func(to ftn.Addr, subject string) ftn.PackedMessage {
		m := ftn.Message{From: "Petros", To: "Sysop", Subject: subject, Orig: uplink, Dest: to,
			Date: now, MsgID: "2:410/9 " + subject, Charset: ftn.CP437, Body: "Hello.\n"}
		return m.Pack()
	}
	em := ftn.Message{Area: "BINKD", From: "Sysop", To: "All", Subject: "echo", Orig: uplink,
		Date: now, MsgID: "2:410/9 e1", Charset: ftn.CP437, Body: "Echo.\n",
		SeenBy: []ftn.Net2D{{Net: 410, Node: 9}}, Path: []ftn.Net2D{{Net: 410, Node: 9}}}
	echo := em.Pack()
	pkt := ftn.Packet{From: uplink, To: gr, Created: now,
		Messages: []ftn.PackedMessage{echo, netmail(gr, "for-gr"), netmail(ru, "for-ru")}}
	path := filepath.Join(t.TempDir(), "00000001.pkt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pkt.WriteTo(f); err != nil {
		t.Fatal(err)
	}
	f.Close()

	for _, tc := range []struct {
		only       []ftn.Addr
		shown      int
		want, deny string
	}{
		{[]ftn.Addr{ru}, 1, "for-ru", "for-gr"},
		{[]ftn.Addr{gr, ftn.MustParseAddr("2:5030/1651.1")}, 1, "for-gr", "for-ru"},
		{[]ftn.Addr{ftn.MustParseAddr("2:9/9")}, 0, "", "for-"},
		{nil, 3, "echo BINKD", ""},
	} {
		text, shown, err := describe(path, tc.only)
		if err != nil {
			t.Fatal(err)
		}
		if shown != tc.shown && tc.only != nil {
			t.Errorf("only %v: shown %d, want %d", tc.only, shown, tc.shown)
		}
		if tc.want != "" && !strings.Contains(text, tc.want) {
			t.Errorf("only %v: %q missing from\n%s", tc.only, tc.want, text)
		}
		if tc.deny != "" && strings.Contains(text, tc.deny) {
			t.Errorf("only %v: %q should not be in\n%s", tc.only, tc.deny, text)
		}
	}
}

func TestParseAddrList(t *testing.T) {
	got, err := parseAddrList("2:410/51, 2:5030/1651.1")
	if err != nil || len(got) != 2 || got[1].String() != "2:5030/1651.1" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := parseAddrList(" , "); err == nil {
		t.Fatal("an empty list must be an error")
	}
}

// rbx1 routing what spb1 sends: its own netmail stays, the rest is queued for
// the next hop with a Via line, and echomail is not touched.
func TestRoutePassesOnOthersNetmail(t *testing.T) {
	dir := t.TempDir()
	spb1, rbx1, petros := ftn.MustParseAddr("2:5030/1651"), ftn.MustParseAddr("2:410/51"), ftn.MustParseAddr("2:410/9")
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	nm := func(to ftn.Addr, subject string) ftn.PackedMessage {
		m := ftn.Message{From: "Sysop", To: "Areafix", Subject: subject, Orig: rbx1, Dest: to, Date: now,
			MsgID: "2:410/51 " + subject, Charset: ftn.CP437, Body: "%LIST\n", Attr: ftn.AttrCrash}
		return m.Pack()
	}
	pkt := ftn.Packet{From: spb1, To: ftn.MustParseAddr("2:5030/1651.1"), Created: now,
		Messages: []ftn.PackedMessage{nm(petros, "to-petros"), nm(rbx1, "to-us"), nm(ftn.MustParseAddr("1:153/757"), "far")}}
	in := filepath.Join(dir, "in.spb1")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(in, "abcd0001.pkt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pkt.WriteTo(f); err != nil {
		t.Fatal(err)
	}
	f.Close()

	out := filepath.Join(dir, "out")
	err = route([]string{"-outbound", out, "-done", filepath.Join(dir, "routed"),
		"-own", "2:410/51,2:5030/1651.1", "-direct", "2:410/9,2:5030/1651", "-via", "2:410/9", path})
	if err != nil {
		t.Fatal(err)
	}
	// Both outgoing messages go to Petros (one direct, one via him), crash.
	queued := filepath.Join(out, "019a0009.cut")
	text, n, err := describe(queued, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || !strings.Contains(text, "to-petros") || !strings.Contains(text, "far") || strings.Contains(text, "to-us") {
		t.Fatalf("queued for Petros (%d):\n%s", n, text)
	}
	raw, _ := os.ReadFile(queued)
	if !strings.Contains(string(raw), "\x01Via 2:410/51 @") {
		t.Error("routed netmail carries no Via line")
	}
	if _, err := os.Stat(filepath.Join(dir, "routed", "abcd0001.pkt")); err != nil {
		t.Errorf("FILE was not moved to -done: %v", err)
	}
}
