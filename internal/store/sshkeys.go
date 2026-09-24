package store

import (
	"database/sql"
	"strings"
	"time"

	"boar/internal/term"
)

const (
	MaxSSHKeys          = 5
	maxSSHKeyCommentLen = 40
)

// SSHKey is a public key that logs its owner in without a password.
type SSHKey struct {
	ID          int64
	UserID      int64
	Fingerprint string // SHA256:...
	PublicKey   string // authorized_keys format, without the comment
	Comment     string
	AddedAt     time.Time
}

// AddSSHKey attaches a parsed public key to an account.
func (s *Store) AddSSHKey(userID int64, fingerprint, publicKey, comment string) (SSHKey, error) {
	k := SSHKey{UserID: userID, Fingerprint: fingerprint, PublicKey: publicKey,
		Comment: term.Clean(comment, maxSSHKeyCommentLen)}
	err := s.tx(func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow("SELECT COUNT(*) FROM ssh_keys WHERE user_id = ?", userID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxSSHKeys {
			return invalid("You already have %d keys. Remove one first.", MaxSSHKeys)
		}
		taken, err := exists(tx, "SELECT 1 FROM ssh_keys WHERE fingerprint = ?", fingerprint)
		if err != nil {
			return err
		}
		if taken {
			return invalid("That key is already in use.")
		}
		now := s.nowNano()
		res, err := tx.Exec("INSERT INTO ssh_keys (user_id, fingerprint, public_key, comment, added_at) VALUES (?, ?, ?, ?, ?)",
			userID, fingerprint, publicKey, k.Comment, now)
		if err != nil {
			return err
		}
		k.ID, err = res.LastInsertId()
		k.AddedAt = fromNano(now)
		return err
	})
	return k, err
}

func (s *Store) SSHKeys(userID int64) ([]SSHKey, error) {
	rows, err := s.db.Query("SELECT id, user_id, fingerprint, public_key, comment, added_at FROM ssh_keys WHERE user_id = ? ORDER BY id", userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanSSHKey)
}

func scanSSHKey(row scanner) (SSHKey, error) {
	var k SSHKey
	var at int64
	err := row.Scan(&k.ID, &k.UserID, &k.Fingerprint, &k.PublicKey, &k.Comment, &at)
	k.AddedAt = fromNano(at)
	return k, err
}

// DeleteSSHKey removes one of the user's own keys.
func (s *Store) DeleteSSHKey(userID, keyID int64) error {
	res, err := s.db.Exec("DELETE FROM ssh_keys WHERE id = ? AND user_id = ?", keyID, userID)
	if err != nil {
		return err
	}
	return requireRow(res)
}

// UserBySSHKey finds the account a key belongs to.
func (s *Store) UserBySSHKey(fingerprint string) (User, error) {
	u, err := scanUser(s.db.QueryRow("SELECT "+userColsOf("u")+
		" FROM ssh_keys k JOIN users u ON u.id = k.user_id WHERE k.fingerprint = ?", strings.TrimSpace(fingerprint)))
	return u, notFound(err)
}
