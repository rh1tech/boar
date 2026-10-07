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
