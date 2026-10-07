// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"database/sql"
	"strings"
	"time"

	"boar/internal/ftn"
	"boar/internal/term"
)

const (
	MaxNetmailNameLen  = 36 // the packed message header's limit
	MaxNetmailAddrLen  = 40
	MaxNetmailBodySize = 64 << 10
	MaxNetmailKept     = 1000
)

// Netmail is one FidoNet netmail message, received or sent.
type Netmail struct {
	ID         int64
	Outgoing   bool
	MsgID      string
	ReplyTo    string
	FromName   string
	FromAddr   string
	ToName     string
	ToAddr     string
	Subject    string
	Body       string
	PostedAt   time.Time
	ReceivedAt time.Time
	ReadAt     time.Time // zero while unread
	AuthorID   int64     // the BBS user who sent it; 0 for received mail
}

const netmailCols = `id, outgoing, msgid, reply_to, from_name, from_addr, to_name, to_addr,
	subject, body, posted_at, received_at, read_at, COALESCE(author_id, 0)`

func scanNetmail(row scanner) (Netmail, error) {
	var n Netmail
	var out int
	var posted, received, read int64
	err := row.Scan(&n.ID, &out, &n.MsgID, &n.ReplyTo, &n.FromName, &n.FromAddr, &n.ToName, &n.ToAddr,
		&n.Subject, &n.Body, &posted, &received, &read, &n.AuthorID)
	n.Outgoing = out != 0
	n.PostedAt, n.ReceivedAt = fromNano(posted), fromNano(received)
	if read != 0 {
		n.ReadAt = fromNano(read)
	}
	return n, err
}

func cleanNetmailName(s, def string) string {
	if s = term.Clean(s, MaxNetmailNameLen); s == "" {
		return def
	}
	return s
}

// ImportNetmail stores netmail that arrived for this node. Duplicates (same
// MSGID, or the same message when it has none) are ignored.
func (s *Store) ImportNetmail(m ftn.Message) (TossResult, error) {
	if m.Area != "" {
		return TossSkipped, nil
	}
	n := Netmail{
		MsgID:    strings.TrimSpace(m.MsgID),
		ReplyTo:  strings.TrimSpace(m.Reply),
		FromName: cleanNetmailName(m.From, "Unknown"),
		FromAddr: term.Clean(m.Orig.String(), MaxNetmailAddrLen),
		ToName:   cleanNetmailName(m.To, "Sysop"),
		ToAddr:   term.Clean(m.Dest.String(), MaxNetmailAddrLen),
		Subject:  term.Clean(m.Subject, MaxSubjectLen),
		Body:     truncateUTF8(m.Body, MaxNetmailBodySize),
		PostedAt: m.Date,
	}
	if n.MsgID == "" {
		n.MsgID = syntheticMsgID(m)
	}
	if n.Subject == "" {
		n.Subject = "(no subject)"
	}
	if n.PostedAt.IsZero() {
		n.PostedAt = s.now()
	}
	stored, err := s.insertNetmail(n)
	if err != nil || !stored {
		return TossDuplicate, err
	}
	return TossStored, nil
}

// RecordSentNetmail keeps a copy of netmail sent from the BBS, read already.
func (s *Store) RecordSentNetmail(n Netmail) (Netmail, error) {
	n.Outgoing = true
	n.FromName = cleanNetmailName(n.FromName, "Sysop")
	n.ToName = cleanNetmailName(n.ToName, "Sysop")
	n.Subject = term.Clean(n.Subject, MaxSubjectLen)
	n.Body = truncateUTF8(n.Body, MaxNetmailBodySize)
	if n.PostedAt.IsZero() {
		n.PostedAt = s.now()
	}
	n.ReadAt = n.PostedAt
	if _, err := s.insertNetmail(n); err != nil {
		return n, err
	}
	return s.NetmailByMsgID(n.MsgID)
}

func (s *Store) insertNetmail(n Netmail) (bool, error) {
	stored := false
	err := s.tx(func(tx *sql.Tx) error {
		var author any
		if n.AuthorID != 0 {
			author = n.AuthorID
		}
		var read int64
		if !n.ReadAt.IsZero() {
			read = toNano(n.ReadAt)
		}
		out := 0
		if n.Outgoing {
			out = 1
		}
		res, err := tx.Exec(`INSERT INTO netmail
			(outgoing, msgid, reply_to, from_name, from_addr, to_name, to_addr, subject, body,
			 posted_at, received_at, read_at, author_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (msgid) DO NOTHING`,
			out, n.MsgID, n.ReplyTo, n.FromName, n.FromAddr, n.ToName, n.ToAddr, n.Subject, n.Body,
			toNano(n.PostedAt), s.nowNano(), read, author)
		if err != nil {
			return err
		}
		k, err := res.RowsAffected()
		if err != nil || k == 0 {
			return err
		}
		stored = true
		// Keep the newest MaxNetmailKept.
		_, err = tx.Exec(`DELETE FROM netmail WHERE id NOT IN
			(SELECT id FROM netmail ORDER BY id DESC LIMIT ?)`, MaxNetmailKept)
		return err
	})
	return stored, err
}

// netmailVisible is the WHERE clause for what a user may see: everything for
// a sysop; for anyone else, netmail addressed to their handle and what they
// sent.
const netmailVisible = `(? OR (outgoing = 0 AND to_name = ? COLLATE NOCASE) OR author_id = ?)`

// NetmailFor lists the netmail u may read, newest first.
func (s *Store) NetmailFor(u User) ([]Netmail, error) {
	rows, err := s.db.Query("SELECT "+netmailCols+" FROM netmail WHERE "+netmailVisible+" ORDER BY id DESC",
		u.Sysop, u.Handle, u.ID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanNetmail)
}

// NetmailUnread counts received netmail u may read and nobody has read yet.
func (s *Store) NetmailUnread(u User) (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM netmail WHERE read_at = 0 AND outgoing = 0 AND "+netmailVisible,
		u.Sysop, u.Handle, u.ID).Scan(&n)
	return n, err
}

// NetmailByMsgID returns one message.
func (s *Store) NetmailByMsgID(msgid string) (Netmail, error) {
	n, err := scanNetmail(s.db.QueryRow("SELECT "+netmailCols+" FROM netmail WHERE msgid = ?", msgid))
	return n, notFound(err)
}

// MarkNetmailRead records that a message has been read. A netmail has one
// reader, the person (or node) it was sent to, so read is not per user.
func (s *Store) MarkNetmailRead(id int64) error {
	_, err := s.db.Exec("UPDATE netmail SET read_at = ? WHERE id = ? AND read_at = 0", s.nowNano(), id)
	return err
}
