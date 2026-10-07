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

// Stats counts what one Toss pass did. Netmail is the share of Stored that
// was netmail for this node.
type Stats struct {
	Files, Stored, Duplicate, Skipped, Failed, Netmail int
	// FilesIn counts file-echo files put in place; FilesBad those set aside.
	FilesIn, FilesBad int
}

// Tosser imports inbound FidoNet mail into the BBS.
type Tosser struct {
	DB *store.Store
	// Own is this node's addresses: netmail for them goes into the netmail
	// box. Netmail for anyone else is counted as skipped.
	Own []ftn.Addr
	// Files, when set, is where file echoes go; nil leaves TICs alone.
	Files *FileStore
}

// Toss reads every mail packet in inbound, stores echomail in db, and moves
// each file to done (or bad if it could not be read). A file whose content
// was tossed before is only moved aside: names are no guide, since mailers
// reuse them.
func Toss(db *store.Store, inbound, done, bad string) (Stats, error) {
	return Tosser{DB: db}.Toss(inbound, done, bad, true)
}

// Toss is the package Toss with this Tosser's settings. secure says inbound
// holds what password-protected sessions delivered: echomail is only taken
// from there, since anybody can call and leave a packet in the other one.
// Netmail is taken from both, because that is how strangers write to a node.
func (t Tosser) Toss(inbound, done, bad string, secure bool) (Stats, error) {
	db := t.DB
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
			var res store.TossResult
			var err error
			switch {
			case m.Area == "" && ftn.Contains(t.Own, m.Dest):
				if res, err = db.ImportNetmail(m); res == store.TossStored {
					st.Netmail++
				}
			case m.Area != "" && secure:
				res, err = db.ImportEcho(m)
			default:
				res = store.TossSkipped
			}
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
	if secure && t.Files != nil {
		if err := t.tossTICs(inbound, done, bad, &st); err != nil {
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
	return Tosser{DB: db}.TossAll(inbounds...)
}

// TossAll is the package TossAll with this Tosser's settings. An inbound
// named in.insecure is the non-secure one.
func (t Tosser) TossAll(inbounds ...string) (Stats, error) {
	var total Stats
	for _, in := range inbounds {
		if in == "" {
			continue
		}
		done, bad := DefaultDirs(in)
		// Secure and insecure share one tossed/bad under the spool root when
		// both live as siblings (in / in.insecure).
		secure := true
		if strings.HasSuffix(filepath.Clean(in), "in.insecure") {
			done, bad = DefaultDirs(filepath.Join(filepath.Dir(in), "in"))
			secure = false
		}
		st, err := t.Toss(in, done, bad, secure)
		total.Files += st.Files
		total.Stored += st.Stored
		total.Duplicate += st.Duplicate
		total.Skipped += st.Skipped
		total.Failed += st.Failed
		total.Netmail += st.Netmail
		total.FilesIn += st.FilesIn
		total.FilesBad += st.FilesBad
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
