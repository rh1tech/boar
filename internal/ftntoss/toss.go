// SPDX-License-Identifier: GPL-3.0-or-later

// Package ftntoss imports FidoNet inbound packets into the BBS database.
package ftntoss

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"boar/internal/ftn"
	"boar/internal/store"
)

// Stats counts what one Toss pass did.
type Stats struct {
	Files, Stored, Duplicate, Skipped, Failed int
}

// Toss reads every mail packet in inbound, stores echomail in db, and moves
// each file to done (or bad if it could not be read). A file whose content
// was tossed before is only moved aside: names are no guide, since mailers
// reuse them. Netmail is counted as skipped until the BBS has a
// netmail box of its own.
func Toss(db *store.Store, inbound, done, bad string) (Stats, error) {
	var st Stats
	if inbound == "" {
		return st, nil
	}
	if err := os.MkdirAll(done, 0o775); err != nil {
		return st, err
	}
	if err := os.MkdirAll(bad, 0o775); err != nil {
		return st, err
	}
	entries, err := os.ReadDir(inbound)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, err
	}
	for _, e := range entries {
		if e.IsDir() || !ftn.IsMailBundle(e.Name()) {
			continue
		}
		path := filepath.Join(inbound, e.Name())
		sum, err := fileSHA256(path)
		if err != nil {
			return st, err
		}
		seen, err := db.FTNFileSeen(sum)
		if err != nil {
			return st, err
		}
		if seen {
			// Left behind after a crash; finish moving it.
			_ = moveAside(path, done, e.Name())
			continue
		}
		st.Files++
		err = ftn.EachMessage(path, func(m ftn.Message) error {
			res, err := db.ImportEcho(m)
			if err != nil {
				return err
			}
			switch res {
			case store.TossStored:
				st.Stored++
			case store.TossDuplicate:
				st.Duplicate++
			default:
				st.Skipped++
			}
			return nil
		})
		if err != nil {
			st.Failed++
			// Leave unmarked so a fixed tosser can retry after the file is
			// moved back from bad/.
			if moveErr := moveAside(path, bad, e.Name()); moveErr != nil {
				return st, fmt.Errorf("%s: unreadable (%v); move to bad: %w", e.Name(), err, moveErr)
			}
			continue
		}
		if err := db.MarkFTNFile(sum, e.Name()); err != nil {
			return st, err
		}
		if err := moveAside(path, done, e.Name()); err != nil {
			return st, err
		}
	}
	return st, nil
}

// fileSHA256 is the hex SHA-256 of the file at path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func moveAside(path, dir, name string) error {
	dest := filepath.Join(dir, name)
	if _, err := os.Lstat(dest); err == nil {
		// An earlier bundle of the same name is already there; keep both.
		dest = fmt.Sprintf("%s.%d", dest, time.Now().UnixNano())
	}
	if err := os.Rename(path, dest); err == nil {
		return nil
	}
	// Cross-device rename: copy then remove. O_TRUNC matches Rename's
	// overwrite behaviour when a previous crash left the destination behind.
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o664)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return closeErr
	}
	return os.Remove(path)
}

// DefaultDirs returns done and bad directories next to inbound.
func DefaultDirs(inbound string) (done, bad string) {
	parent := filepath.Dir(strings.TrimRight(inbound, string(filepath.Separator)))
	return filepath.Join(parent, "tossed"), filepath.Join(parent, "bad")
}

// TossAll runs Toss over every inbound directory. Secure and insecure
// inbounds that live as siblings share one tossed/ and bad/ under the spool.
func TossAll(db *store.Store, inbounds ...string) (Stats, error) {
	var total Stats
	for _, in := range inbounds {
		if in == "" {
			continue
		}
		done, bad := DefaultDirs(in)
		// Secure and insecure share one tossed/bad under the spool root when
		// both live as siblings (in / in.insecure).
		if strings.HasSuffix(filepath.Clean(in), "in.insecure") {
			done, bad = DefaultDirs(filepath.Join(filepath.Dir(in), "in"))
		}
		st, err := Toss(db, in, done, bad)
		total.Files += st.Files
		total.Stored += st.Stored
		total.Duplicate += st.Duplicate
		total.Skipped += st.Skipped
		total.Failed += st.Failed
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
