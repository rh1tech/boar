// SPDX-License-Identifier: GPL-3.0-or-later

package ftntoss

import (
	"bufio"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"boar/internal/store"
)

const (
	maxTICBytes = 64 << 10
	// MaxFileBytes caps one file-echo file: big enough for any nodelist or
	// software archive a hub passes on, small enough that a broken link
	// cannot fill the disk with one file.
	MaxFileBytes = 1 << 30
	// ticWaitForFile is how long a TIC waits for its file to arrive before it
	// is set aside: the two travel separately and either may come first.
	ticWaitForFile = 24 * time.Hour
)

// TIC is a file echo's announcement of one file (FSC-0087).
type TIC struct {
	Area, AreaDesc, File, Replaces, Origin, From string
	Desc, LDesc                                  []string
	Size                                         int64 // -1 when absent
	CRC                                          uint32
	HasCRC                                       bool
}

var (
	safeAreaTag  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._!-]{0,39}$`)
	unsafeInName = regexp.MustCompile(`[/\\\x00-\x1f\x7f]`)
)

// ParseTIC reads a TIC file. Keywords are case-insensitive; Desc and LDesc
// may repeat.
func ParseTIC(r io.Reader) (TIC, error) {
	t := TIC{Size: -1}
	sc := bufio.NewScanner(io.LimitReader(r, maxTICBytes))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		key, value, _ := strings.Cut(strings.TrimLeft(line, " \t"), " ")
		value = strings.TrimSpace(value)
		switch strings.ToLower(key) {
		case "area":
			t.Area = strings.ToUpper(value)
		case "areadesc":
			t.AreaDesc = value
		case "file":
			t.File = value
		case "lfile", "fullname":
			if t.File == "" {
				t.File = value
			}
		case "replaces":
			t.Replaces = value
		case "origin":
			t.Origin = value
		case "from":
			t.From = value
		case "desc":
			t.Desc = append(t.Desc, value)
		case "ldesc":
			t.LDesc = append(t.LDesc, value)
		case "size":
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				t.Size = n
			}
		case "crc":
			if n, err := strconv.ParseUint(value, 16, 32); err == nil {
				t.CRC, t.HasCRC = uint32(n), true
			}
		}
	}
	if err := sc.Err(); err != nil {
		return t, err
	}
	switch {
	case !safeAreaTag.MatchString(t.Area):
		return t, fmt.Errorf("TIC: bad area %q", t.Area)
	case t.File == "" || len(t.File) > 255 || unsafeInName.MatchString(t.File) || strings.HasPrefix(t.File, "."):
		return t, fmt.Errorf("TIC: bad file name %q", t.File)
	case !t.HasCRC:
		return t, errors.New("TIC: no CRC")
	}
	return t, nil
}

// IsTIC reports whether name is a TIC file.
func IsTIC(name string) bool { return strings.EqualFold(filepath.Ext(name), ".tic") }

// FileStore is where file echoes are kept: Root/<area>/<file>.
type FileStore struct{ Root string }

// areaDir is the directory for an area tag, lower case.
func (fs FileStore) areaDir(tag string) string { return filepath.Join(fs.Root, strings.ToLower(tag)) }

// findInDir finds name in dir, ignoring case, as mailers change it.
func findInDir(dir, name string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(e.Name(), name) {
			return filepath.Join(dir, e.Name()), true
		}
	}
	return "", false
}

// checkFile verifies size and CRC-32 against the TIC.
func checkFile(path string, t TIC) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := crc32.NewIEEE()
	n, err := io.Copy(h, io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return err
	}
	switch {
	case n > MaxFileBytes:
		return fmt.Errorf("%s: larger than %d bytes", t.File, MaxFileBytes)
	case t.Size >= 0 && n != t.Size:
		return fmt.Errorf("%s: %d bytes, TIC says %d", t.File, n, t.Size)
	case h.Sum32() != t.CRC:
		return fmt.Errorf("%s: CRC %08X, TIC says %08X", t.File, h.Sum32(), t.CRC)
	}
	return nil
}

// moveFile renames, or copies across file systems.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

// tossTICs puts every file-echo file in inbound whose TIC has arrived in its
// area, checked against the TIC, and records it. A TIC still waiting for its
// file is left for the next pass, up to ticWaitForFile. Anything that does not
// check out goes to bad, with its TIC.
func (t Tosser) tossTICs(inbound, done, bad string, st *Stats) error {
	entries, err := os.ReadDir(inbound)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !IsTIC(e.Name()) {
			continue
		}
		ticPath := filepath.Join(inbound, e.Name())
		if err := t.tossTIC(inbound, ticPath, done, bad, st); err != nil {
			return err
		}
	}
	return nil
}

func (t Tosser) tossTIC(inbound, ticPath, done, bad string, st *Stats) error {
	setAside := func(paths ...string) error {
		st.FilesBad++
		for _, p := range paths {
			if err := moveAside(p, bad, filepath.Base(p)); err != nil {
				return err
			}
		}
		return nil
	}
	f, err := os.Open(ticPath)
	if err != nil {
		return err
	}
	tic, err := ParseTIC(f)
	f.Close()
	if err != nil {
		return setAside(ticPath)
	}
	data, ok := findInDir(inbound, tic.File)
	if !ok {
		if fi, err := os.Stat(ticPath); err == nil && time.Since(fi.ModTime()) > ticWaitForFile {
			return setAside(ticPath)
		}
		return nil // the file is still on its way
	}
	if err := checkFile(data, tic); err != nil {
		return setAside(ticPath, data)
	}

	dir := t.Files.areaDir(tic.Area)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if tic.Replaces != "" && !strings.EqualFold(tic.Replaces, tic.File) {
		if old, err := t.DB.RemoveFile(tic.Area, tic.Replaces); err == nil {
			_ = os.Remove(filepath.Join(t.Files.Root, old))
		}
	}
	dst := filepath.Join(dir, tic.File)
	if err := moveFile(data, dst); err != nil {
		return err
	}
	rel, err := filepath.Rel(t.Files.Root, dst)
	if err != nil {
		return err
	}
	if _, err := t.DB.AddFile(tic.Area, tic.AreaDesc, store.File{
		Name: tic.File, Path: rel, Size: fileSize(dst), CRC32: fmt.Sprintf("%08X", tic.CRC),
		Description: strings.Join(tic.Desc, "\n"), LDesc: strings.Join(tic.LDesc, "\n"),
		Origin: tic.Origin, FromAddr: tic.From,
	}); err != nil {
		return err
	}
	st.FilesIn++
	return moveAside(ticPath, done, filepath.Base(ticPath))
}

func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}
