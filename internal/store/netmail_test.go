// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"testing"
	"time"

	"boar/internal/ftn"
)

func TestNetmailImportAndVisibility(t *testing.T) {
	s, _ := openTemp(t)
	sysop := mustUser(t, s, "Root") // the first account is the sysop
	if !sysop.Sysop {
		t.Fatal("first user is not sysop")
	}
	alice := mustUser(t, s, "Alice")
	bob := mustUser(t, s, "Bob")

	m := ftn.Message{From: "Petros Argyrakis", To: "alice", Subject: "Hi",
		Orig: ftn.MustParseAddr("2:410/9"), Dest: ftn.MustParseAddr("2:410/51"),
		Date: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC), MsgID: "2:410/9 0001", Body: "Hello."}
	if r := must(s.ImportNetmail(m)); r != TossStored {
		t.Fatalf("first import = %v", r)
	}
	if r := must(s.ImportNetmail(m)); r != TossDuplicate {
		t.Fatalf("second import = %v", r)
	}
	if n := must(s.NetmailUnread(alice)); n != 1 {
		t.Fatalf("alice unread = %d (to_name matches her handle, any case)", n)
	}
	if n := must(s.NetmailUnread(bob)); n != 0 {
		t.Fatalf("bob sees %d netmail not for him", n)
	}
	if got := must(s.NetmailFor(sysop)); len(got) != 1 {
		t.Fatalf("sysop sees %d", len(got))
	}

	sent := must(s.RecordSentNetmail(Netmail{MsgID: "2:5030/1651 0002", FromName: "Root", FromAddr: "2:5030/1651",
		ToName: "Petros", ToAddr: "2:410/9", Subject: "Re: Hi", Body: "Thanks.", AuthorID: sysop.ID}))
	if !sent.Outgoing || sent.ReadAt.IsZero() {
		t.Fatalf("sent copy = %+v", sent)
	}
	if n := must(s.NetmailUnread(sysop)); n != 1 {
		t.Fatalf("sysop unread = %d; a sent copy is not unread mail", n)
	}
	got := must(s.NetmailFor(alice))
	must(0, s.MarkNetmailRead(got[0].ID))
	if n := must(s.NetmailUnread(sysop)); n != 0 {
		t.Fatalf("unread after alice read it = %d", n)
	}
}

// A robot request carries the link's password as its subject. Boar answers
// no robots (the hub tosser does), so it keeps no copy of a request that
// arrives, and only a redacted one of a request the sysop sends.
func TestRobotNetmailKeepsNoPassword(t *testing.T) {
	s, _ := openTemp(t)
	sysop := mustUser(t, s, "Root")
	req := ftn.Message{From: "Mikhail Matveev", To: "AreaFix", Subject: "s3cretPw",
		Orig: ftn.MustParseAddr("2:5030/1651.1"), Dest: ftn.MustParseAddr("2:5030/1651"),
		Date: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC), MsgID: "2:5030/1651.1 0001", Body: "%LIST"}
	if r := must(s.ImportNetmail(req)); r != TossSkipped {
		t.Fatalf("robot request import = %v, want skipped", r)
	}
	if got := must(s.NetmailFor(sysop)); len(got) != 0 {
		t.Fatalf("robot request stored: %+v", got)
	}

	sent := must(s.RecordSentNetmail(Netmail{MsgID: "2:5030/1651 0003", FromName: "Root", FromAddr: "2:5030/1651",
		ToName: "areafix", ToAddr: "2:5030/731", Subject: "s3cretPw", Body: "+R50.SYSOP"}))
	if sent.Subject != ftn.HiddenSubject {
		t.Fatalf("sent robot request stored with subject %q", sent.Subject)
	}
	if sent.Body != "+R50.SYSOP" {
		t.Fatalf("body = %q", sent.Body)
	}
}
