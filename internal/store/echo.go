// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"boar/internal/ftn"
	"boar/internal/term"
)

const (
	MaxEchoTagLen     = 40
	MaxEchoFromLen    = 36
	MaxEchoAddrLen    = 40
	MaxEchoBodyBytes  = 64 << 10
	MaxEchoMessages   = 2000 // kept per area; oldest are pruned on import
	MaxEchoSubjectLen = MaxSubjectLen
)

// EchoArea is one FidoNet echo conference the BBS has mail for.
type EchoArea struct {
	ID          int64
	Tag         string
	Description string
	CreatedAt   time.Time
}

// EchoAreaSummary is an area with per-user counters.
type EchoAreaSummary struct {
	EchoArea
	Posts    int
	New      int
	LastPost time.Time
}

// EchoMessage is one tossed echomail message.
type EchoMessage struct {
	ID       int64
	AreaID   int64
	MsgID    string
	FromName string
	FromAddr string
	ToName   string
	Subject  string
	Body     string
	PostedAt time.Time
}

const echoMsgCols = "id, area_id, msgid, from_name, from_addr, to_name, subject, body, posted_at"

func scanEchoMessage(row scanner) (EchoMessage, error) {
	var m EchoMessage
	var at int64
	err := row.Scan(&m.ID, &m.AreaID, &m.MsgID, &m.FromName, &m.FromAddr, &m.ToName, &m.Subject, &m.Body, &at)
	m.PostedAt = fromNano(at)
	return m, err
}

// EchoAreas lists every area with post and unread counts for userID.
func (s *Store) EchoAreas(userID int64) ([]EchoAreaSummary, error) {
	rows, err := s.db.Query(`
		SELECT a.id, a.tag, a.description, a.created_at,
		       COUNT(m.id),
		       COALESCE(SUM(m.id > COALESCE(r.last_read_id, 0)), 0),
		       COALESCE(MAX(m.posted_at), 0)
		FROM echo_areas a
		LEFT JOIN echo_messages m ON m.area_id = a.id
		LEFT JOIN echo_reads r ON r.area_id = a.id AND r.user_id = ?
		GROUP BY a.id
		ORDER BY a.tag`, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (EchoAreaSummary, error) {
		var es EchoAreaSummary
		var created, last int64
		err := row.Scan(&es.ID, &es.Tag, &es.Description, &created, &es.Posts, &es.New, &last)
		es.CreatedAt, es.LastPost = fromNano(created), fromNano(last)
		return es, err
	})
}

// EchoAreaByID returns one area.
func (s *Store) EchoAreaByID(id int64) (EchoArea, error) {
	var a EchoArea
	var at int64
	err := s.db.QueryRow("SELECT id, tag, description, created_at FROM echo_areas WHERE id = ?", id).
		Scan(&a.ID, &a.Tag, &a.Description, &at)
	a.CreatedAt = fromNano(at)
	return a, notFound(err)
}

// EchoMessages returns an area's newest MaxEchoMessages messages, newest first.
func (s *Store) EchoMessages(areaID int64) ([]EchoMessage, error) {
	rows, err := s.db.Query("SELECT "+echoMsgCols+" FROM echo_messages WHERE area_id = ? ORDER BY id DESC LIMIT ?",
		areaID, MaxEchoMessages)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanEchoMessage)
}

// EchoLastRead returns the newest message ID userID has read in an area.
func (s *Store) EchoLastRead(userID, areaID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow("SELECT last_read_id FROM echo_reads WHERE user_id = ? AND area_id = ?", userID, areaID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// MarkEchoRead records that userID has read everything up to msgID.
func (s *Store) MarkEchoRead(userID, areaID, msgID int64) error {
	_, err := s.db.Exec(`INSERT INTO echo_reads (user_id, area_id, last_read_id) VALUES (?, ?, ?)
		ON CONFLICT (user_id, area_id) DO UPDATE SET last_read_id = max(last_read_id, excluded.last_read_id)`,
		userID, areaID, msgID)
	return err
}

// EchoUnreadCount is the number of unread echomail messages for userID.
func (s *Store) EchoUnreadCount(userID int64) (int, error) {
	areas, err := s.EchoAreas(userID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range areas {
		n += a.New
	}
	return n, nil
}

// FTNFileSeen reports whether an inbound file with this content (its
// SHA-256, hex) was already tossed. Not its name: mailers reuse those.
func (s *Store) FTNFileSeen(sum string) (bool, error) {
	return exists(s.db, "SELECT 1 FROM ftn_tossed WHERE sha256 = ?", sum)
}

// MarkFTNFile records that the file with this content has been tossed.
func (s *Store) MarkFTNFile(sum, name string) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO ftn_tossed (sha256, name, tossed_at) VALUES (?, ?, ?)", sum, name, s.nowNano())
	return err
}

// TossResult is what ImportEcho did with one message.
type TossResult int

const (
	TossStored TossResult = iota
	TossDuplicate
	TossSkipped // netmail or empty area
)

// ImportEcho stores one echomail message. Netmail (empty Area) is skipped.
// Duplicates (same MSGID) are ignored. Areas are created on first sight.
func (s *Store) ImportEcho(m ftn.Message) (TossResult, error) {
	if m.Area == "" {
		return TossSkipped, nil
	}
	tag := strings.ToUpper(strings.TrimSpace(m.Area))
	if tag == "" || len(tag) > MaxEchoTagLen {
		return TossSkipped, nil
	}
	msgid := strings.TrimSpace(m.MsgID)
	if msgid == "" {
		msgid = syntheticMsgID(m)
	}
	fromName := term.Clean(m.From, MaxEchoFromLen)
	if fromName == "" {
		fromName = "Unknown"
	}
	fromAddr := term.Clean(m.Orig.String(), MaxEchoAddrLen)
	toName := term.Clean(m.To, MaxEchoFromLen)
	if toName == "" {
		toName = "All"
	}
	subject := term.Clean(m.Subject, MaxEchoSubjectLen)
	if subject == "" {
		subject = "(no subject)"
	}
	body := truncateUTF8(m.Body, MaxEchoBodyBytes)
	posted := m.Date
	if posted.IsZero() {
		posted = s.now()
	}

	var result TossResult
	err := s.tx(func(tx *sql.Tx) error {
		taken, err := exists(tx, "SELECT 1 FROM echo_messages WHERE msgid = ?", msgid)
		if err != nil {
			return err
		}
		if taken {
			result = TossDuplicate
			return nil
		}
		areaID, err := ensureEchoArea(tx, tag, s.nowNano())
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO echo_messages
			(area_id, msgid, from_name, from_addr, to_name, subject, body, posted_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			areaID, msgid, fromName, fromAddr, toName, subject, body, toNano(posted))
		if err != nil {
			return err
		}
		result = TossStored
		return pruneEchoArea(tx, areaID)
	})
	return result, err
}

func ensureEchoArea(tx *sql.Tx, tag string, now int64) (int64, error) {
	if _, err := tx.Exec("INSERT OR IGNORE INTO echo_areas (tag, description, created_at) VALUES (?, '', ?)", tag, now); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRow("SELECT id FROM echo_areas WHERE tag = ?", tag).Scan(&id)
	return id, err
}

func pruneEchoArea(tx *sql.Tx, areaID int64) error {
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM echo_messages WHERE area_id = ?", areaID).Scan(&n); err != nil {
		return err
	}
	if n <= MaxEchoMessages {
		return nil
	}
	_, err := tx.Exec(`DELETE FROM echo_messages WHERE area_id = ? AND id IN (
		SELECT id FROM echo_messages WHERE area_id = ? ORDER BY id ASC LIMIT ?)`,
		areaID, areaID, n-MaxEchoMessages)
	return err
}

// syntheticMsgID builds a stable id when the packet carried no MSGID.
func syntheticMsgID(m ftn.Message) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s\n%s\n%s\n%s\n%s\n%d\n%s",
		m.Area, m.From, m.Orig.String(), m.To, m.Subject, m.Date.Unix(), m.Body)
	return "boar-" + hex.EncodeToString(h.Sum(nil))
}

// truncateUTF8 shortens s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
