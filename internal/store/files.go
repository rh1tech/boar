// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"boar/internal/term"
)

const (
	MaxFileAreaTagLen = 40
	MaxFileNameLen    = 255
	MaxFileDescLen    = 4 << 10
)

// FileArea is one FidoNet file echo.
type FileArea struct {
	ID          int64
	Tag         string
	Description string
	CreatedAt   time.Time
}

// FileAreaSummary is an area with counters for one caller.
type FileAreaSummary struct {
	FileArea
	Files  int
	New    int
	Newest time.Time
}

// File is one file that arrived in a file echo. Path is relative to the
// BBS's file root.
type File struct {
	ID          int64
	AreaID      int64
	Name        string
	Path        string
	Size        int64
	CRC32       string
	Description string
	LDesc       string
	Origin      string
	FromAddr    string
	ReceivedAt  time.Time
	Downloads   int
}

const fileCols = "id, area_id, name, path, size, crc32, description, ldesc, origin, from_addr, received_at, downloads"

func scanFile(row scanner) (File, error) {
	var f File
	var at int64
	err := row.Scan(&f.ID, &f.AreaID, &f.Name, &f.Path, &f.Size, &f.CRC32, &f.Description, &f.LDesc,
		&f.Origin, &f.FromAddr, &at, &f.Downloads)
	f.ReceivedAt = fromNano(at)
	return f, err
}

// AddFile records a file that has been put in place, creating its area on
// first sight. A file of the same name in the area is replaced; the old row's
// path is returned so the caller can remove that file if it moved.
func (s *Store) AddFile(areaTag, areaDesc string, f File) (old string, err error) {
	tag := strings.ToUpper(strings.TrimSpace(areaTag))
	if tag == "" || len(tag) > MaxFileAreaTagLen || f.Name == "" || len(f.Name) > MaxFileNameLen {
		return "", invalid("bad file area or file name")
	}
	err = s.tx(func(tx *sql.Tx) error {
		var areaID int64
		err := tx.QueryRow("SELECT id FROM file_areas WHERE tag = ?", tag).Scan(&areaID)
		if errors.Is(err, sql.ErrNoRows) {
			res, err := tx.Exec("INSERT INTO file_areas (tag, description, created_at) VALUES (?, ?, ?)",
				tag, term.Clean(areaDesc, MaxSubjectLen), s.nowNano())
			if err != nil {
				return err
			}
			areaID, err = res.LastInsertId()
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if areaDesc != "" {
			if _, err := tx.Exec("UPDATE file_areas SET description = ? WHERE id = ? AND description = ''",
				term.Clean(areaDesc, MaxSubjectLen), areaID); err != nil {
				return err
			}
		}
		if err := tx.QueryRow("SELECT path FROM files WHERE area_id = ? AND name = ?", areaID, f.Name).Scan(&old); err != nil &&
			!errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if old != "" {
			// A new copy is a new file: delete and insert, so it counts as new.
			if _, err := tx.Exec("DELETE FROM files WHERE area_id = ? AND name = ?", areaID, f.Name); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`INSERT INTO files
			(area_id, name, path, size, crc32, description, ldesc, origin, from_addr, received_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			areaID, f.Name, f.Path, f.Size, f.CRC32,
			truncateUTF8(f.Description, MaxFileDescLen), truncateUTF8(f.LDesc, MaxFileDescLen),
			term.Clean(f.Origin, MaxEchoAddrLen), term.Clean(f.FromAddr, MaxEchoAddrLen), s.nowNano())
		return err
	})
	return old, err
}

// RemoveFile deletes a file's row (for TIC "Replaces") and returns its path.
func (s *Store) RemoveFile(areaTag, name string) (string, error) {
	var path string
	err := s.tx(func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT f.path FROM files f JOIN file_areas a ON a.id = f.area_id
			WHERE a.tag = ? AND f.name = ? COLLATE NOCASE`, strings.ToUpper(areaTag), name).Scan(&path)
		if err != nil {
			return notFound(err)
		}
		_, err = tx.Exec("DELETE FROM files WHERE path = ?", path)
		return err
	})
	return path, err
}

// FileAreas lists every area with file and new-file counts for userID.
func (s *Store) FileAreas(userID int64) ([]FileAreaSummary, error) {
	rows, err := s.db.Query(`
		SELECT a.id, a.tag, a.description, a.created_at,
		       COUNT(f.id),
		       COALESCE(SUM(f.id > COALESCE(r.last_seen_id, 0)), 0),
		       COALESCE(MAX(f.received_at), 0)
		FROM file_areas a
		LEFT JOIN files f ON f.area_id = a.id
		LEFT JOIN file_reads r ON r.area_id = a.id AND r.user_id = ?
		GROUP BY a.id
		ORDER BY a.tag`, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (FileAreaSummary, error) {
		var fa FileAreaSummary
		var created, newest int64
		err := row.Scan(&fa.ID, &fa.Tag, &fa.Description, &created, &fa.Files, &fa.New, &newest)
		fa.CreatedAt, fa.Newest = fromNano(created), fromNano(newest)
		return fa, err
	})
}

// FilesIn lists an area's files, newest first.
func (s *Store) FilesIn(areaID int64) ([]File, error) {
	rows, err := s.db.Query("SELECT "+fileCols+" FROM files WHERE area_id = ? ORDER BY id DESC", areaID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanFile)
}

// FileByID returns one file.
func (s *Store) FileByID(id int64) (File, error) {
	f, err := scanFile(s.db.QueryRow("SELECT "+fileCols+" FROM files WHERE id = ?", id))
	return f, notFound(err)
}

// FilesLastSeen is the newest file ID userID has seen listed in an area.
func (s *Store) FilesLastSeen(userID, areaID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow("SELECT last_seen_id FROM file_reads WHERE user_id = ? AND area_id = ?", userID, areaID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// MarkFilesSeen records that userID has seen an area's files up to fileID.
func (s *Store) MarkFilesSeen(userID, areaID, fileID int64) error {
	_, err := s.db.Exec(`INSERT INTO file_reads (user_id, area_id, last_seen_id) VALUES (?, ?, ?)
		ON CONFLICT (user_id, area_id) DO UPDATE SET last_seen_id = max(last_seen_id, excluded.last_seen_id)`,
		userID, areaID, fileID)
	return err
}

// NewFilesCount is how many files userID has not seen yet, across areas.
func (s *Store) NewFilesCount(userID int64) (int, error) {
	areas, err := s.FileAreas(userID)
	n := 0
	for _, a := range areas {
		n += a.New
	}
	return n, err
}

// CountDownload adds one to a file's download count.
func (s *Store) CountDownload(id int64) error {
	_, err := s.db.Exec("UPDATE files SET downloads = downloads + 1 WHERE id = ?", id)
	return err
}
