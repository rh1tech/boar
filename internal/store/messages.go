// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"database/sql"
	"slices"
	"strings"
	"time"

	"boar/internal/term"
)

// Message is a private letter between two users. Each side deletes its own
// copy; the message is purged once both have deleted it.
type Message struct {
	ID       int64
	FromID   int64
	ToID     int64
	ThreadID int64
	ReplyTo  int64
	Subject  string
	Body     string
	SentAt   time.Time
	ReadAt   time.Time
}

func (m Message) IsRead() bool { return !m.ReadAt.IsZero() }

const messageCols = "id, from_id, to_id, thread_id, reply_to, subject, body, sent_at, read_at"

func scanMessage(row scanner) (Message, error) {
	var m Message
	var sent, read int64
	err := row.Scan(&m.ID, &m.FromID, &m.ToID, &m.ThreadID, &m.ReplyTo, &m.Subject, &m.Body, &sent, &read)
	m.SentAt, m.ReadAt = fromNano(sent), fromNano(read)
	return m, err
}

// visibleTo is the SQL condition for messages userID still has a copy of.
const visibleTo = "((to_id = ? AND deleted_by_to = 0) OR (from_id = ? AND deleted_by_from = 0))"

// SendMessage delivers a letter. Recipients who blocked the sender refuse it,
// except that mail from a sysop always gets through.
func (s *Store) SendMessage(fromID, toID int64, subject, body string, replyTo int64) (Message, error) {
	subject, body, err := cleanLetter(subject, body)
	if err != nil {
		return Message{}, err
	}
	var m Message
	err = s.tx(func(tx *sql.Tx) error {
		sender, err := getUser(tx, fromID)
		if err != nil {
			return err
		}
		recipient, err := getUser(tx, toID)
		if err != nil {
			return err
		}
		if !sender.Validated && !recipient.Sysop {
			return ErrNotValidated // new callers may only write to the sysop
		}
		if !sender.Sysop {
			blocked, err := isBlocked(tx, toID, fromID)
			if err != nil {
				return err
			}
			if blocked {
				return ErrBlocked
			}
		}
		var inbox int
		if err := tx.QueryRow("SELECT COUNT(*) FROM messages WHERE to_id = ? AND deleted_by_to = 0", toID).Scan(&inbox); err != nil {
			return err
		}
		if inbox >= MaxInbox {
			return ErrMailboxFull
		}
		threadID, replyTo, err := messageThread(tx, fromID, replyTo)
		if err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO messages (from_id, to_id, thread_id, reply_to, subject, body, sent_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, fromID, toID, threadID, replyTo, subject, body, s.nowNano())
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if threadID == 0 {
			if _, err := tx.Exec("UPDATE messages SET thread_id = id WHERE id = ?", id); err != nil {
				return err
			}
		}
		m, err = scanMessage(tx.QueryRow("SELECT "+messageCols+" FROM messages WHERE id = ?", id))
		return err
	})
	return m, err
}

// messageThread resolves the thread a reply joins. A reply may only point at
// a message the sender can see; anything else starts a new thread.
func messageThread(q querier, senderID, replyTo int64) (threadID, parent int64, err error) {
	if replyTo == 0 {
		return 0, 0, nil
	}
	err = q.QueryRow("SELECT thread_id FROM messages WHERE id = ? AND "+visibleTo, replyTo, senderID, senderID).Scan(&threadID)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	return threadID, replyTo, err
}

func cleanLetter(subject, body string) (string, string, error) {
	subject = term.Clean(subject, MaxSubjectLen)
	if subject == "" {
		return "", "", invalid("A subject is required.")
	}
	body = cleanBody(body)
	if body == "" {
		return "", "", invalid("The message is empty.")
	}
	if len(body) > MaxBodyBytes {
		return "", "", invalid("The message is too long (max %d KB).", MaxBodyBytes>>10)
	}
	return subject, body, nil
}

func cleanBody(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, ln := range lines {
		cleaned = append(cleaned, strings.TrimRight(term.StripControl(ln), " "))
	}
	return strings.Trim(strings.Join(cleaned, "\n"), "\n")
}

func (s *Store) queryMessages(where string, args ...any) ([]Message, error) {
	rows, err := s.db.Query("SELECT "+messageCols+" FROM messages WHERE "+where+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanMessage)
}

// Inbox returns the user's received messages, oldest first.
func (s *Store) Inbox(userID int64) ([]Message, error) {
	return s.queryMessages("to_id = ? AND deleted_by_to = 0", userID)
}

// Outbox returns the user's sent messages, oldest first.
func (s *Store) Outbox(userID int64) ([]Message, error) {
	return s.queryMessages("from_id = ? AND deleted_by_from = 0", userID)
}

// Thread returns every message in msgID's conversation that userID can see.
func (s *Store) Thread(msgID, userID int64) ([]Message, error) {
	var threadID int64
	err := s.db.QueryRow("SELECT thread_id FROM messages WHERE id = ? AND "+visibleTo, msgID, userID, userID).Scan(&threadID)
	if err != nil {
		return nil, notFound(err)
	}
	return s.queryMessages("thread_id = ? AND "+visibleTo, threadID, userID, userID)
}

// SearchMail finds the user's messages whose subject, body or correspondent
// contains query (case-insensitive for ASCII).
func (s *Store) SearchMail(userID int64, query string) ([]Message, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}
	rows, err := s.db.Query("SELECT "+messageCols+" FROM messages WHERE "+visibleTo+` AND (
		instr(lower(subject), ?) > 0 OR instr(lower(body), ?) > 0 OR
		EXISTS (SELECT 1 FROM users u WHERE u.id IN (from_id, to_id) AND u.id != ? AND instr(u.handle_key, ?) > 0)
	) ORDER BY id DESC LIMIT ?`, userID, userID, query, query, userID, query, MaxSearchResults)
	if err != nil {
		return nil, err
	}
	found, err := collect(rows, scanMessage)
	slices.Reverse(found) // newest matches win the limit; show them oldest first
	return found, err
}

func (s *Store) UnreadCount(userID int64) (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE to_id = ? AND deleted_by_to = 0 AND read_at = 0", userID).Scan(&n)
	return n, err
}

func (s *Store) MarkRead(msgID, userID int64) error {
	res, err := s.db.Exec("UPDATE messages SET read_at = ? WHERE id = ? AND to_id = ? AND deleted_by_to = 0 AND read_at = 0",
		s.nowNano(), msgID, userID)
	if err != nil {
		return err
	}
	err = requireRow(res)
	if err != ErrNotFound {
		return err
	}
	// Nothing changed: either it was already read (fine) or it isn't theirs.
	ok, err := exists(s.db, "SELECT 1 FROM messages WHERE id = ? AND to_id = ? AND deleted_by_to = 0", msgID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// DeleteMessage removes the message from userID's inbox and/or outbox.
func (s *Store) DeleteMessage(msgID, userID int64) error {
	return s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE messages SET
			deleted_by_from = deleted_by_from OR (from_id = ?),
			deleted_by_to   = deleted_by_to   OR (to_id = ?)
			WHERE id = ? AND `+visibleTo, userID, userID, msgID, userID, userID)
		if err != nil {
			return err
		}
		if err := requireRow(res); err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM messages WHERE id = ? AND deleted_by_from = 1 AND deleted_by_to = 1", msgID)
		return err
	})
}
