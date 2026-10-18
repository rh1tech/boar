// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"errors"
	"testing"
	"time"

	"boar/internal/ftn"
)

func TestEchoSysopOnly(t *testing.T) {
	for _, tag := range []string{" r50.Sysop.club ", "fido_sysop", "test-private", "r50.crisis", "r50.elections", "regcon.eur"} {
		if !EchoSysopOnly(tag) {
			t.Errorf("expected restricted: %q", tag)
		}
	}
	for _, tag := range []string{"FTSC_PUBLIC", "S0T.STAT", "SPB.POINT", "HUBBLE", "LINUX", "MAKENL_NG"} {
		if EchoSysopOnly(tag) {
			t.Errorf("expected public: %q", tag)
		}
	}
}

func TestEchoVisibility(t *testing.T) {
	s, _ := openTemp(t)
	sysop := mustUser(t, s, "Alice")
	caller := mustUser(t, s, "Caller")
	restricted := []string{
		"R50.BONE", "R50.COORD", "R50.CRISIS", "R50.ELECTIONS", "R50.HUBS",
		"R50.SYSOP", "R50.SYSOP.CLUB", "R50.SYSOP.INFO", "R50.SYSOP.TALK",
		"SPB.BONE", "SPB.FEC", "SPB.HUB", "SPB.HUBS", "SPB.HUBS.PVT",
		"SPB.NC", "SPB.NEC", "SPB.SYSOP", "SPB.SYSOP.INFO", "SPB.SYSOP.TALK",
		"ENET.SYSOP", "FIDO_SYSOP", "REGCON.EUR", "TEST.PRIVATE",
	}
	public := []string{"SPB.09", "SPB.ANIME", "SPB.HARDW", "SPB.POINT", "SPB.TOURISM", "S0T.INFO", "S0T.STAT", "S0T.TALK", "FTSC_PUBLIC", "LINUX"}
	for _, tag := range append(append([]string{}, restricted...), public...) {
		if _, err := s.ImportEcho(ftn.Message{Area: tag, MsgID: tag, Body: "Test"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.EchoAreas(sysop.ID)
	if err != nil || len(all) != len(restricted)+len(public) {
		t.Fatalf("sysop areas: %d, %v", len(all), err)
	}
	visible, err := s.EchoAreas(caller.ID)
	if err != nil || len(visible) != len(public) {
		t.Fatalf("caller areas: %d, %v", len(visible), err)
	}
	for _, a := range all {
		wantRestricted := true
		for _, tag := range public {
			if tag == a.Tag {
				wantRestricted = false
			}
		}
		msgs, err := s.EchoMessagesFor(caller.ID, a.ID)
		if wantRestricted {
			if !errors.Is(err, ErrForbidden) || len(msgs) != 0 {
				t.Fatalf("restricted %s leaked: %v, %v", a.Tag, msgs, err)
			}
		} else if err != nil || len(msgs) != 1 {
			t.Fatalf("public %s unavailable: %v, %v", a.Tag, msgs, err)
		}
		if msgs, err := s.EchoMessagesFor(sysop.ID, a.ID); err != nil || len(msgs) != 1 {
			t.Fatalf("sysop cannot read %s: %v", a.Tag, err)
		}
	}
	if n, err := s.EchoUnreadCount(caller.ID); err != nil || n != len(public) {
		t.Fatalf("caller unread includes hidden echoes: %d, %v", n, err)
	}
	if n, err := s.EchoUnreadCount(sysop.ID); err != nil || n != len(all) {
		t.Fatalf("sysop unread missing echoes: %d, %v", n, err)
	}
}

func TestImportEchoAndRead(t *testing.T) {
	s, _ := openTemp(t)
	u := mustUser(t, s, "Alice")

	m := ftn.Message{
		Area: "LINUX", From: "Petros", To: "All", Subject: "Hello",
		Orig: ftn.MustParseAddr("2:410/9"), Date: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
		MsgID: "2:410/9 12345678", Body: "First echo.\n\n--- GoldED\n * Origin: test (2:410/9)",
	}
	res, err := s.ImportEcho(m)
	if err != nil || res != TossStored {
		t.Fatalf("ImportEcho = %v, %v", res, err)
	}
	res, err = s.ImportEcho(m)
	if err != nil || res != TossDuplicate {
		t.Fatalf("duplicate = %v, %v", res, err)
	}
	if res, err := s.ImportEcho(ftn.Message{From: "x", Subject: "netmail"}); err != nil || res != TossSkipped {
		t.Fatalf("netmail skip = %v, %v", res, err)
	}

	areas, err := s.EchoAreas(u.ID)
	if err != nil || len(areas) != 1 || areas[0].Tag != "LINUX" || areas[0].Posts != 1 || areas[0].New != 1 {
		t.Fatalf("areas = %+v, %v", areas, err)
	}
	msgs, err := s.EchoMessages(areas[0].ID)
	if err != nil || len(msgs) != 1 || msgs[0].FromName != "Petros" || msgs[0].FromAddr != "2:410/9" {
		t.Fatalf("msgs = %+v, %v", msgs, err)
	}
	if err := s.MarkEchoRead(u.ID, areas[0].ID, msgs[0].ID); err != nil {
		t.Fatal(err)
	}
	areas, err = s.EchoAreas(u.ID)
	if err != nil || areas[0].New != 0 {
		t.Fatalf("after read new = %+v, %v", areas, err)
	}
}

func TestImportEchoSyntheticMsgID(t *testing.T) {
	s, _ := openTemp(t)
	m := ftn.Message{
		Area: "TREK", From: "Spock", To: "All", Subject: "Live long",
		Orig: ftn.MustParseAddr("2:410/51"), Date: time.Unix(1_700_000_000, 0),
		Body: "and prosper",
	}
	if _, err := s.ImportEcho(m); err != nil {
		t.Fatal(err)
	}
	if res, err := s.ImportEcho(m); err != nil || res != TossDuplicate {
		t.Fatalf("synthetic dupe = %v, %v", res, err)
	}
}
