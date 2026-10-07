// SPDX-License-Identifier: GPL-3.0-or-later

package filelink

import (
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"boar/internal/store"
)

// Files is what the download server needs from the database.
type Files interface {
	FileByID(id int64) (store.File, error)
	CountDownload(id int64) error
}

// Handler serves GET /f/<token>: the one file the token names, from inside
// root, as an attachment. Anything else is a 404 that says nothing more.
func Handler(key []byte, root string, files Files, log *slog.Logger) http.Handler {
	root = filepath.Clean(root)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /f/{token}", func(w http.ResponseWriter, r *http.Request) {
		l, err := Verify(key, r.PathValue("token"), time.Now())
		if err != nil {
			http.Error(w, "This download link is not valid, or has expired. Ask the BBS for a new one.", http.StatusNotFound)
			return
		}
		f, err := files.FileByID(l.FileID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(root, f.Path)
		if !strings.HasPrefix(path, root+string(filepath.Separator)) {
			http.NotFound(w, r)
			return
		}
		fh, err := os.Open(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer fh.Close()
		fi, err := fh.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		h.Set("Cache-Control", "private, no-store")
		// A resumed download is the same download: count only those that
		// start at the beginning.
		if r.Header.Get("Range") == "" {
			if err := files.CountDownload(f.ID); err != nil {
				log.Warn("count download failed", "file", f.ID, "err", err)
			}
			log.Info("web download", "user_id", l.UserID, "file", f.Name, "ip", r.Header.Get("X-Real-IP"))
		}
		http.ServeContent(w, r, "", fi.ModTime(), fh)
	})
	return mux
}
