// SPDX-License-Identifier: GPL-3.0-or-later

package filelink

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"boar/internal/store"
)

func TestSignVerify(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	now := time.Unix(1_800_000_000, 0)
	tok := Sign(key, Link{FileID: 42, UserID: 7, Expires: now.Add(time.Hour)})
	l, err := Verify(key, tok, now)
	if err != nil || l.FileID != 42 || l.UserID != 7 {
		t.Fatalf("Verify = %+v, %v", l, err)
	}
	if _, err := Verify(key, tok, now.Add(2*time.Hour)); err == nil {
		t.Error("expired link accepted")
	}
	if _, err := Verify([]byte(strings.Repeat("x", 32)), tok, now); err == nil {
		t.Error("link accepted under another key")
	}
	other := Sign(key, Link{FileID: 43, UserID: 7, Expires: now.Add(time.Hour)})
	forged := strings.SplitN(other, ".", 2)[0] + "." + strings.SplitN(tok, ".", 2)[1]
	if _, err := Verify(key, forged, now); err == nil {
		t.Error("payload swapped under an old signature accepted")
	}
	for _, bad := range []string{"", ".", "abc", tok + "x"} {
		if _, err := Verify(key, bad, now); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestLoadKeyCreatesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "files.key")
	a, err := LoadKey(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadKey(path)
	if err != nil || string(a) != string(b) || len(a) != keyBytes {
		t.Fatalf("second load = %x, %v", b, err)
	}
}

type fakeFiles struct {
	f         store.File
	downloads int
}

func (ff *fakeFiles) FileByID(id int64) (store.File, error) {
	if id != ff.f.ID {
		return store.File{}, store.ErrNotFound
	}
	return ff.f, nil
}
func (ff *fakeFiles) CountDownload(int64) error { ff.downloads++; return nil }

func TestHandler(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nodelist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nodelist", "NODELIST.Z79"), []byte("list"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(t.TempDir(), "secret"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := []byte(strings.Repeat("k", 32))
	ff := &fakeFiles{f: store.File{ID: 5, Name: "NODELIST.Z79", Path: filepath.Join("nodelist", "NODELIST.Z79")}}
	h := Handler(key, root, ff, slog.New(slog.NewTextHandler(io.Discard, nil)))
	get := func(token string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/f/"+token, nil))
		return rec
	}

	rec := get(Sign(key, Link{FileID: 5, UserID: 1, Expires: time.Now().Add(time.Hour)}))
	if rec.Code != 200 || rec.Body.String() != "list" || !strings.Contains(rec.Header().Get("Content-Disposition"), "NODELIST.Z79") {
		t.Fatalf("good link: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	if ff.downloads != 1 {
		t.Errorf("downloads = %d", ff.downloads)
	}
	if rec := get(Sign(key, Link{FileID: 5, Expires: time.Now().Add(-time.Minute)})); rec.Code != 404 {
		t.Errorf("expired link: %d", rec.Code)
	}
	if rec := get("garbage"); rec.Code != 404 {
		t.Errorf("bad token: %d", rec.Code)
	}
	ff.f.Path = "../secret" // a row pointing outside the root is never served
	if rec := get(Sign(key, Link{FileID: 5, Expires: time.Now().Add(time.Hour)})); rec.Code != 404 {
		t.Errorf("path outside the root: %d", rec.Code)
	}
}
