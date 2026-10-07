// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"testing"
	"time"

	"boar/internal/ftn"
)

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
