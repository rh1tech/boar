// SPDX-License-Identifier: GPL-3.0-or-later

package ftntoss_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"boar/internal/ftn"
	"boar/internal/ftntoss"
	"boar/internal/store"
)

func TestTossImportsEchomail(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "boar.db")
	inbound := filepath.Join(dir, "in")
	if err := os.MkdirAll(inbound, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.Config{Path: dbPath, KDFIterations: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	us := ftn.MustParseAddr("2:410/9")
	them := ftn.MustParseAddr("2:410/51")
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	m := ftn.Message{
		Area: "BINKD", From: "Sysop", To: "All", Subject: "Ping",
		Orig: us, Date: now, MsgID: "2:410/9 aabbccdd", Charset: ftn.CP437,
		Body:   "Hello from the uplink.\n\n" + ftn.EchoFooter("test", "Test BBS", us),
		SeenBy: []ftn.Net2D{{Net: 410, Node: 9}}, Path: []ftn.Net2D{{Net: 410, Node: 9}},
	}
	pkt := ftn.Packet{From: us, To: them, Created: now, Messages: []ftn.PackedMessage{m.Pack()}}
	pktPath := filepath.Join(inbound, "test.pkt")
	f, err := os.Create(pktPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pkt.WriteTo(f); err != nil {
		t.Fatal(err)
	}
	f.Close()

	stats, err := ftntoss.TossAll(st, inbound)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 1 || stats.Stored != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if _, err := os.Stat(pktPath); !os.IsNotExist(err) {
		t.Fatalf("packet still in inbound: %v", err)
	}
	areas, err := st.EchoAreas(1)
	if err != nil || len(areas) != 1 || areas[0].Tag != "BINKD" {
		t.Fatalf("areas = %+v, %v", areas, err)
	}

	// Second pass is a no-op.
	stats, err = ftntoss.TossAll(st, inbound)
	if err != nil || stats.Files != 0 {
		t.Fatalf("second pass = %+v, %v", stats, err)
	}
}

// writePacket writes one echomail message as a packet at path.
func writePacket(t *testing.T, path, msgid, subject string) {
	t.Helper()
	us := ftn.MustParseAddr("2:410/9")
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	m := ftn.Message{
		Area: "BINKD", From: "Sysop", To: "All", Subject: subject,
		Orig: us, Date: now, MsgID: msgid, Charset: ftn.CP437,
		Body:   "Body.\n\n" + ftn.EchoFooter("test", "Test BBS", us),
		SeenBy: []ftn.Net2D{{Net: 410, Node: 9}}, Path: []ftn.Net2D{{Net: 410, Node: 9}},
	}
	pkt := ftn.Packet{From: us, To: ftn.MustParseAddr("2:5030/1651"), Created: now, Messages: []ftn.PackedMessage{m.Pack()}}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := pkt.WriteTo(f); err != nil {
		t.Fatal(err)
	}
}

// Mailers reuse bundle and packet names; a new file under an old name is new
// mail, not a leftover.
func TestTossReusedNameIsStillTossed(t *testing.T) {
	dir := t.TempDir()
	inbound := filepath.Join(dir, "in")
	if err := os.MkdirAll(inbound, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.Config{Path: filepath.Join(dir, "boar.db"), KDFIterations: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	path := filepath.Join(inbound, "00000001.pkt")
	writePacket(t, path, "2:410/9 00000001", "First")
	if s, err := ftntoss.TossAll(st, inbound); err != nil || s.Stored != 1 {
		t.Fatalf("first = %+v, %v", s, err)
	}
	writePacket(t, path, "2:410/9 00000002", "Second")
	if s, err := ftntoss.TossAll(st, inbound); err != nil || s.Stored != 1 {
		t.Fatalf("same name, new content = %+v, %v", s, err)
	}
	done, _ := ftntoss.DefaultDirs(inbound)
	if entries, _ := os.ReadDir(done); len(entries) != 2 {
		t.Fatalf("tossed/ holds %d files, want both", len(entries))
	}
}
