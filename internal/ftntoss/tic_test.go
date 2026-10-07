// SPDX-License-Identifier: GPL-3.0-or-later

package ftntoss_test

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"boar/internal/ftntoss"
	"boar/internal/store"
)

func writeTIC(t *testing.T, dir, ticName, area, file string, data []byte, crc uint32, extra string) {
	t.Helper()
	tic := fmt.Sprintf("Area %s\r\nAreadesc Nodelists\r\nOrigin 2:410/9\r\nFrom 2:410/9\r\nFile %s\r\nDesc %s\r\nLDesc First line\r\nLDesc Second line\r\nSize %d\r\nCRC %08X\r\n%s",
		area, file, "Weekly nodelist", len(data), crc, extra)
	if err := os.WriteFile(filepath.Join(dir, ticName), []byte(tic), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTossTICs(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in")
	root := filepath.Join(dir, "files")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.Config{Path: filepath.Join(dir, "boar.db"), KDFIterations: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, err := st.CreateUser("Root", "secret12", "Here")
	if err != nil {
		t.Fatal(err)
	}
	tosser := ftntoss.Tosser{DB: st, Files: &ftntoss.FileStore{Root: root}}

	good := []byte("nodelist.279 contents")
	writeTIC(t, in, "aa000001.tic", "NODELIST", "NODELIST.Z79", good, crc32.ChecksumIEEE(good), "")
	if err := os.WriteFile(filepath.Join(in, "nodelist.z79"), good, 0o644); err != nil { // case differs
		t.Fatal(err)
	}
	bad := []byte("corrupted in transit")
	writeTIC(t, in, "aa000002.tic", "NODELIST", "BROKEN.ZIP", bad, 0x12345678, "")
	if err := os.WriteFile(filepath.Join(in, "BROKEN.ZIP"), bad, 0o644); err != nil {
		t.Fatal(err)
	}
	writeTIC(t, in, "aa000003.tic", "NODELIST", "LATER.ZIP", []byte("x"), crc32.ChecksumIEEE([]byte("x")), "")
	if err := os.WriteFile(filepath.Join(in, "aa000004.tic"), []byte("Area ../../etc\r\nFile passwd\r\nCRC 00000000\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := tosser.TossAll(in)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesIn != 1 || stats.FilesBad != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	got, err := os.ReadFile(filepath.Join(root, "nodelist", "NODELIST.Z79"))
	if err != nil || string(got) != string(good) {
		t.Fatalf("file in area: %q, %v", got, err)
	}
	for _, name := range []string{"BROKEN.ZIP", "aa000002.tic", "aa000004.tic"} {
		if _, err := os.Stat(filepath.Join(dir, "bad", name)); err != nil {
			t.Errorf("%s not set aside: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(in, "aa000003.tic")); err != nil {
		t.Errorf("a TIC still waiting for its file must stay: %v", err)
	}

	areas, err := st.FileAreas(u.ID)
	if err != nil || len(areas) != 1 || areas[0].Tag != "NODELIST" || areas[0].Files != 1 || areas[0].New != 1 {
		t.Fatalf("areas = %+v, %v", areas, err)
	}
	files, err := st.FilesIn(areas[0].ID)
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %+v, %v", files, err)
	}
	f := files[0]
	if f.Description != "Weekly nodelist" || !strings.Contains(f.LDesc, "Second line") || f.Path != filepath.Join("nodelist", "NODELIST.Z79") {
		t.Fatalf("file row = %+v", f)
	}

	// Next week's nodelist replaces this one.
	next := []byte("nodelist.286 contents")
	writeTIC(t, in, "aa000005.tic", "NODELIST", "NODELIST.Z86", next, crc32.ChecksumIEEE(next), "Replaces NODELIST.Z79\r\n")
	if err := os.WriteFile(filepath.Join(in, "NODELIST.Z86"), next, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tosser.TossAll(in); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "nodelist", "NODELIST.Z79")); !os.IsNotExist(err) {
		t.Errorf("replaced file still on disk: %v", err)
	}
	if files, _ := st.FilesIn(areas[0].ID); len(files) != 1 || files[0].Name != "NODELIST.Z86" {
		t.Fatalf("after Replaces: %+v", files)
	}
}

func TestParseTICRejectsUnsafeNames(t *testing.T) {
	for _, tic := range []string{
		"Area NODELIST\nFile ../x\nCRC 1\n",
		"Area NODELIST\nFile .hidden\nCRC 1\n",
		"Area ../x\nFile ok.zip\nCRC 1\n",
		"Area NODELIST\nFile ok.zip\n", // no CRC
	} {
		if _, err := ftntoss.ParseTIC(strings.NewReader(tic)); err == nil {
			t.Errorf("accepted %q", tic)
		}
	}
}
