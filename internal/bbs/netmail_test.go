// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"boar/internal/ftn"
)

// A sysop reads netmail that came for the node and replies; the reply leaves
// from the address it was written to, through the uplink, crash.
func TestNetmailReadAndReply(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	cfg := Config{Name: "Test BBS", MaxNodes: 4, IdleTimeout: time.Minute, FTN: FTNConfig{
		Addresses: []ftn.Addr{ftn.MustParseAddr("2:5030/1651"), ftn.MustParseAddr("2:410/51")},
		Outbound:  ftn.Outbound{Root: out, DefaultZone: 2},
		Routes:    ftn.Routes{Direct: []ftn.Addr{ftn.MustParseAddr("2:5030/1651.1")}, Via: ftn.MustParseAddr("2:5030/1651.1")},
		SysopName: "Real Name",
	}}
	addr, st := startServer(t, cfg)
	mustCreateSysopAndUsers(t, st)
	if _, err := st.ImportNetmail(ftn.Message{From: "Petros Argyrakis", To: "Sysop", Subject: "Welcome",
		Orig: ftn.MustParseAddr("2:410/9"), Dest: ftn.MustParseAddr("2:410/51"),
		Date: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC), MsgID: "2:410/9 0001", Body: "Welcome to Net 410."}); err != nil {
		t.Fatal(err)
	}

	c := dial(t, addr, "root")
	c.enter("Root")
	c.key("T")
	c.expect("Welcome")
	c.expect("Read #")
	c.line("1")
	c.expect("Welcome to Net 410.")
	c.key("R")
	c.expect("Quote the original message?")
	c.key("N")
	c.expect("From    : 2:410/51") // answered from the address it was sent to
	c.expect("via 2:5030/1651.1")
	c.expect("Subject")
	c.line("")
	c.writeBody("Thank you.")
	c.expect("Queued for 2:410/9, via 2:5030/1651.1.")

	packet := (ftn.Outbound{Root: out, DefaultZone: 2}).PacketPath(ftn.MustParseAddr("2:5030/1651.1"), ftn.Crash)
	msgs, err := ftn.MessagesInFile(packet)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("outbound %s: %v, %v", packet, msgs, err)
	}
	m := msgs[0]
	if m.Orig.String() != "2:410/51" || m.Dest.String() != "2:410/9" || m.From != "Real Name" ||
		m.Subject != "Re: Welcome" || m.Reply != "2:410/9 0001" || !strings.Contains(m.Body, "Thank you.") {
		t.Fatalf("queued netmail = %+v", m)
	}
}
