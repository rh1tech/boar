package store

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"time"
)

// EmailMode says what a caller wants emailed when new BBS mail arrives.
type EmailMode int

const (
	EmailOff    EmailMode = iota // nothing
	EmailNotice                  // "you have mail from X", no content
	EmailCopy                    // a full copy of the message
)

func (m EmailMode) String() string {
	switch m {
	case EmailNotice:
		return "notice only"
	case EmailCopy:
		return "full copy"
	default:
		return "off"
	}
}

const (
	MaxEmailLen         = 254
	EmailCodeTTL        = 15 * time.Minute
	maxEmailCodeTries   = 5
	emailCodeDigits     = 6
	emailCodeUpperBound = 1_000_000 // 10^emailCodeDigits
)

// ValidateEmail accepts a bare address like kasia@example.com.
func ValidateEmail(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" || len(addr) > MaxEmailLen {
		return invalid("That doesn't look like an email address.")
	}
	parsed, err := mail.ParseAddress(addr)
	if err != nil || parsed.Address != addr || parsed.Name != "" {
		return invalid("That doesn't look like an email address.")
	}
	_, domain, _ := strings.Cut(addr, "@")
	if !strings.Contains(domain, ".") || strings.ContainsAny(addr, " \t\r\n<>\"") {
		return invalid("That doesn't look like an email address.")
	}
	return nil
}

// StartEmailVerification records a pending address and returns the code to
// send there. Asking again replaces any earlier code.
func (s *Store) StartEmailVerification(userID int64, addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if err := ValidateEmail(addr); err != nil {
		return "", err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(emailCodeUpperBound))
	if err != nil {
		return "", fmt.Errorf("store: generate code: %w", err)
	}
	code := fmt.Sprintf("%0*d", emailCodeDigits, n.Int64())
	hash := sha256.Sum256([]byte(code))
	_, err = s.db.Exec(`INSERT INTO email_verifications (user_id, email, code_hash, expires_at, attempts)
		VALUES (?, ?, ?, ?, 0)
		ON CONFLICT (user_id) DO UPDATE SET email = excluded.email, code_hash = excluded.code_hash,
			expires_at = excluded.expires_at, attempts = 0`,
		userID, addr, hash[:], s.now().Add(EmailCodeTTL).UnixNano())
	if err != nil {
		return "", err
	}
	return code, nil
}

// PendingEmail returns the address awaiting verification, if any.
func (s *Store) PendingEmail(userID int64) (string, bool, error) {
	var addr string
	var expires int64
	err := s.db.QueryRow("SELECT email, expires_at FROM email_verifications WHERE user_id = ?", userID).Scan(&addr, &expires)
	if err == sql.ErrNoRows || (err == nil && s.now().UnixNano() > expires) {
		return "", false, nil
	}
	return addr, err == nil, err
}

// ConfirmEmail checks a code. On success the pending address becomes the
// caller's email, with new-mail notices switched on if they were off.
func (s *Store) ConfirmEmail(userID int64, code string) (User, error) {
	var u User
	var codeErr error // reported after the transaction commits the attempt count
	err := s.tx(func(tx *sql.Tx) error {
		var addr string
		var hash []byte
		var expires int64
		var attempts int
		err := tx.QueryRow("SELECT email, code_hash, expires_at, attempts FROM email_verifications WHERE user_id = ?", userID).
			Scan(&addr, &hash, &expires, &attempts)
		if err == sql.ErrNoRows {
			return invalid("No address is waiting to be verified.")
		}
		if err != nil {
			return err
		}
		if s.now().UnixNano() > expires || attempts >= maxEmailCodeTries {
			if _, err := tx.Exec("DELETE FROM email_verifications WHERE user_id = ?", userID); err != nil {
				return err
			}
			codeErr = invalid("That code has expired. Ask for a new one.")
			return nil
		}
		given := sha256.Sum256([]byte(strings.TrimSpace(code)))
		if subtle.ConstantTimeCompare(given[:], hash) != 1 {
			_, err := tx.Exec("UPDATE email_verifications SET attempts = attempts + 1 WHERE user_id = ?", userID)
			codeErr = invalid("Wrong code.")
			return err
		}
		// Checked only after the code proves ownership, so this reveals
		// nothing to someone typing in other people's addresses.
		taken, err := exists(tx, "SELECT 1 FROM users WHERE lower(email) = lower(?) AND id != ?", addr, userID)
		if err != nil {
			return err
		}
		if taken {
			if _, err := tx.Exec("DELETE FROM email_verifications WHERE user_id = ?", userID); err != nil {
				return err
			}
			codeErr = invalid("That address already belongs to another account.")
			return nil
		}
		if _, err := tx.Exec("UPDATE users SET email = ?, email_mode = max(email_mode, ?) WHERE id = ?", addr, EmailNotice, userID); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM email_verifications WHERE user_id = ?", userID); err != nil {
			return err
		}
		u, err = getUser(tx, userID)
		return err
	})
	if err != nil {
		return User{}, err
	}
	return u, codeErr
}

func (s *Store) SetEmailMode(userID int64, mode EmailMode) (User, error) {
	if mode < EmailOff || mode > EmailCopy {
		return User{}, invalid("Unknown email setting.")
	}
	res, err := s.db.Exec("UPDATE users SET email_mode = ? WHERE id = ? AND email != ''", mode, userID)
	if err != nil {
		return User{}, err
	}
	if err := requireRow(res); err != nil {
		return User{}, invalid("Add and verify an email address first.")
	}
	return s.UserByID(userID)
}

// RemoveEmail forgets the caller's address and any pending verification.
func (s *Store) RemoveEmail(userID int64) (User, error) {
	err := s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM email_verifications WHERE user_id = ?", userID); err != nil {
			return err
		}
		_, err := tx.Exec("UPDATE users SET email = '', email_mode = 0 WHERE id = ?", userID)
		return err
	})
	if err != nil {
		return User{}, err
	}
	return s.UserByID(userID)
}
