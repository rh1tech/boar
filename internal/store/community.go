// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"database/sql"
	"slices"
	"time"

	"boar/internal/term"
)

// Bulletin is sysop news shown to callers at login.
type Bulletin struct {
	ID       int64
	AuthorID int64
	Title    string
	Body     string
	PostedAt time.Time
}

// Oneliner is one line on the public wall.
type Oneliner struct {
	ID       int64
	AuthorID int64
	Author   string // handle, or "" if the author was deleted
	Text     string
	PostedAt time.Time
}

func (s *Store) AddBulletin(authorID int64, title, body string) (Bulletin, error) {
	title, body, err := cleanLetter(title, body)
	if err != nil {
		return Bulletin{}, err
	}
	var b Bulletin
	err = s.tx(func(tx *sql.Tx) error {
		if err := requireSysop(tx, authorID); err != nil {
			return err
		}
		now := s.nowNano()
		res, err := tx.Exec("INSERT INTO bulletins (author_id, title, body, posted_at) VALUES (?, ?, ?, ?)", authorID, title, body, now)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		b = Bulletin{ID: id, AuthorID: authorID, Title: title, Body: body, PostedAt: fromNano(now)}
		return err
	})
	return b, err
}

// Bulletins returns every bulletin, newest first.
func (s *Store) Bulletins() ([]Bulletin, error) {
	rows, err := s.db.Query("SELECT id, author_id, title, body, posted_at FROM bulletins ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (Bulletin, error) {
		var b Bulletin
		var at int64
		err := row.Scan(&b.ID, &b.AuthorID, &b.Title, &b.Body, &at)
		b.PostedAt = fromNano(at)
		return b, err
	})
}

// BulletinsSince counts bulletins posted after t.
func (s *Store) BulletinsSince(t time.Time) (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM bulletins WHERE posted_at > ?", toNano(t)).Scan(&n)
	return n, err
}

// DeleteBulletin removes a bulletin; only sysops may.
func (s *Store) DeleteBulletin(actorID, id int64) error {
	return s.sysopDelete(actorID, "DELETE FROM bulletins WHERE id = ?", id)
}

func (s *Store) AddOneliner(authorID int64, text string) (Oneliner, error) {
	text = term.Clean(text, MaxOnelinerLen)
	if text == "" {
		return Oneliner{}, invalid("Say something first.")
	}
	u, err := s.UserByID(authorID)
	if err != nil {
		return Oneliner{}, err
	}
	if !u.Validated {
		return Oneliner{}, ErrNotValidated
	}
	now := s.nowNano()
	res, err := s.db.Exec("INSERT INTO oneliners (author_id, text, posted_at) VALUES (?, ?, ?)", authorID, text, now)
	if err != nil {
		return Oneliner{}, err
	}
	id, err := res.LastInsertId()
	return Oneliner{ID: id, AuthorID: authorID, Author: u.Handle, Text: text, PostedAt: fromNano(now)}, err
}

// Oneliners returns the newest n lines of the wall, oldest first.
func (s *Store) Oneliners(n int) ([]Oneliner, error) {
	rows, err := s.db.Query(`SELECT o.id, o.author_id, COALESCE(u.handle, ''), o.text, o.posted_at
		FROM oneliners o LEFT JOIN users u ON u.id = o.author_id
		ORDER BY o.id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	lines, err := collect(rows, func(row scanner) (Oneliner, error) {
		var o Oneliner
		var at int64
		err := row.Scan(&o.ID, &o.AuthorID, &o.Author, &o.Text, &at)
		o.PostedAt = fromNano(at)
		return o, err
	})
	slices.Reverse(lines)
	return lines, err
}

// DeleteOneliner removes a line from the wall; only sysops may.
func (s *Store) DeleteOneliner(actorID, id int64) error {
	return s.sysopDelete(actorID, "DELETE FROM oneliners WHERE id = ?", id)
}

// sysopDelete runs a single-row delete after checking the actor is a sysop.
func (s *Store) sysopDelete(actorID int64, query string, id int64) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := requireSysop(tx, actorID); err != nil {
			return err
		}
		res, err := tx.Exec(query, id)
		if err != nil {
			return err
		}
		return requireRow(res)
	})
}

func requireSysop(q querier, userID int64) error {
	u, err := getUser(q, userID)
	if err != nil {
		return err
	}
	if !u.Sysop {
		return ErrForbidden
	}
	return nil
}
