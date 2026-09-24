// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"database/sql"
	"strings"
	"time"

	"boar/internal/term"
)

type User struct {
	ID        int64
	Handle    string
	Location  string
	Sysop     bool
	Locked    bool
	CreatedAt time.Time
	LastLogin time.Time
	Calls     int
	Email     string    // verified address, or ""
	EmailMode EmailMode // what to email when new mail arrives
	Validated bool      // approved by a sysop (or registration is open)
}

const userCols = "id, handle, location, is_sysop, is_locked, created_at, last_login, calls, email, email_mode, is_validated"

// userColsOf is userCols qualified with a table alias.
func userColsOf(alias string) string {
	cols := strings.Split(userCols, ", ")
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
}

type scanner interface{ Scan(dest ...any) error }

func scanUser(row scanner) (User, error) {
	var u User
	var created, last int64
	err := row.Scan(userDest(&u, &created, &last)...)
	u.CreatedAt, u.LastLogin = fromNano(created), fromNano(last)
	return u, err
}

// userDest lists scan destinations matching userCols.
func userDest(u *User, created, last *int64) []any {
	return []any{&u.ID, &u.Handle, &u.Location, &u.Sysop, &u.Locked, created, last, &u.Calls, &u.Email, &u.EmailMode, &u.Validated}
}

func getUser(q querier, id int64) (User, error) {
	u, err := scanUser(q.QueryRow("SELECT "+userCols+" FROM users WHERE id = ?", id))
	return u, notFound(err)
}

func (s *Store) UserCount() (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

// CreateUser registers a caller. The very first user becomes the sysop.
// With Config.ApproveNewUsers, everyone else waits for a sysop's approval.
func (s *Store) CreateUser(handle, password, location string) (User, error) {
	handle = strings.TrimSpace(handle)
	if err := ValidateHandle(handle); err != nil {
		return User{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return User{}, err
	}
	cred, err := newCredential(password, s.kdfIter)
	if err != nil {
		return User{}, err
	}
	var u User
	err = s.tx(func(tx *sql.Tx) error {
		taken, err := exists(tx, "SELECT 1 FROM users WHERE handle_key = ?", handleKey(handle))
		if err != nil {
			return err
		}
		if taken {
			return ErrHandleTaken
		}
		first, err := exists(tx, "SELECT 1 FROM users LIMIT 1")
		if err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO users
			(handle, handle_key, location, pass_hash, salt, kdf_iter, is_sysop, is_validated, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			handle, handleKey(handle), term.Clean(location, MaxLocationLen),
			cred.hash, cred.salt, cred.iter, !first, !first || !s.approveNew, s.nowNano())
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		u, err = getUser(tx, id)
		return err
	})
	return u, err
}

// Authenticate checks a handle/password pair. Unknown handles take as long
// as wrong passwords, so timing does not reveal which handles exist. Locked
// accounts authenticate; the caller decides what to do with them.
func (s *Store) Authenticate(handle, password string) (User, error) {
	var c credential
	row := s.db.QueryRow("SELECT pass_hash, salt, kdf_iter, "+userCols+" FROM users WHERE handle_key = ?", handleKey(handle))
	var u User
	var created, last int64
	err := row.Scan(append([]any{&c.hash, &c.salt, &c.iter}, userDest(&u, &created, &last)...)...)
	if err == sql.ErrNoRows {
		_, _ = deriveKey(password, dummySalt, s.kdfIter)
		return User{}, ErrBadCredentials
	}
	if err != nil {
		return User{}, err
	}
	u.CreatedAt, u.LastLogin = fromNano(created), fromNano(last)
	match, err := c.matches(password)
	if err != nil {
		return User{}, err
	}
	if !match {
		return User{}, ErrBadCredentials
	}
	return u, nil
}

// RecordLogin bumps the call counter and returns the updated user along
// with the time of their previous login.
func (s *Store) RecordLogin(id int64) (User, time.Time, error) {
	var u User
	var prev time.Time
	err := s.tx(func(tx *sql.Tx) error {
		before, err := getUser(tx, id)
		if err != nil {
			return err
		}
		prev = before.LastLogin
		if _, err := tx.Exec("UPDATE users SET last_login = ?, calls = calls + 1 WHERE id = ?", s.nowNano(), id); err != nil {
			return err
		}
		u, err = getUser(tx, id)
		return err
	})
	return u, prev, err
}

func (s *Store) UserByID(id int64) (User, error) { return getUser(s.db, id) }

func (s *Store) UserByHandle(handle string) (User, error) {
	u, err := scanUser(s.db.QueryRow("SELECT "+userCols+" FROM users WHERE handle_key = ?", handleKey(handle)))
	return u, notFound(err)
}

// Users returns every user sorted by handle (for mass mail).
func (s *Store) Users() ([]User, error) {
	return s.ListUsers(-1)
}

// ListUsers returns up to limit users sorted by handle; a negative limit
// means all of them.
func (s *Store) ListUsers(limit int) ([]User, error) {
	rows, err := s.db.Query("SELECT "+userCols+" FROM users ORDER BY handle_key LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanUser)
}

func (s *Store) ChangePassword(id int64, current, next string) error {
	var c credential
	err := s.db.QueryRow("SELECT pass_hash, salt, kdf_iter FROM users WHERE id = ?", id).Scan(&c.hash, &c.salt, &c.iter)
	if err != nil {
		return notFound(err)
	}
	match, err := c.matches(current)
	if err != nil {
		return err
	}
	if !match {
		return ErrBadCredentials
	}
	return s.setPassword(id, next)
}

// ResetPassword lets a sysop replace someone's password.
func (s *Store) ResetPassword(actorID, id int64, password string) error {
	if err := s.requireActor(actorID, id, false); err != nil {
		return err
	}
	return s.setPassword(id, password)
}

func (s *Store) setPassword(id int64, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	cred, err := newCredential(password, s.kdfIter)
	if err != nil {
		return err
	}
	return s.updateUser(id, "UPDATE users SET pass_hash = ?, salt = ?, kdf_iter = ? WHERE id = ?",
		cred.hash, cred.salt, cred.iter, id)
}

func (s *Store) SetLocation(id int64, location string) (User, error) {
	if err := s.updateUser(id, "UPDATE users SET location = ? WHERE id = ?", term.Clean(location, MaxLocationLen), id); err != nil {
		return User{}, err
	}
	return s.UserByID(id)
}

// SetSysop grants or removes sysop rights. Sysops can't demote themselves.
func (s *Store) SetSysop(actorID, id int64, sysop bool) error {
	if err := s.requireActor(actorID, id, !sysop); err != nil {
		return err
	}
	return s.updateUser(id, "UPDATE users SET is_sysop = ?, is_validated = (is_validated OR ?) WHERE id = ?", sysop, sysop, id)
}

// SetLocked locks or unlocks an account. Sysops can't lock themselves.
func (s *Store) SetLocked(actorID, id int64, locked bool) error {
	if err := s.requireActor(actorID, id, true); err != nil {
		return err
	}
	return s.updateUser(id, "UPDATE users SET is_locked = ? WHERE id = ?", locked, id)
}

// BootstrapSysop promotes a user without an acting sysop. It exists for the
// -sysop startup flag, which runs before anyone is online.
func (s *Store) BootstrapSysop(handle string) (User, error) {
	u, err := s.UserByHandle(handle)
	if err != nil {
		return User{}, err
	}
	if err := s.updateUser(u.ID, "UPDATE users SET is_sysop = 1, is_validated = 1 WHERE id = ?", u.ID); err != nil {
		return User{}, err
	}
	return s.UserByID(u.ID)
}

// requireActor checks that actorID is a sysop, and, if notSelf, that they
// are not acting on their own account.
func (s *Store) requireActor(actorID, targetID int64, notSelf bool) error {
	if notSelf && actorID == targetID {
		return ErrForbidden
	}
	return requireSysop(s.db, actorID)
}

func (s *Store) updateUser(id int64, query string, args ...any) error {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	return requireRow(res)
}

// DeleteUser removes an account and its private mail. Mail they sent stays
// in recipients' inboxes; their public posts and oneliners stay too.
func (s *Store) DeleteUser(actorID, id int64) error {
	if actorID == id {
		return ErrForbidden
	}
	return s.tx(func(tx *sql.Tx) error {
		if err := requireSysop(tx, actorID); err != nil {
			return err
		}
		res, err := tx.Exec("DELETE FROM users WHERE id = ?", id)
		if err != nil {
			return err
		}
		if err := requireRow(res); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM messages WHERE to_id = ?", id); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE messages SET deleted_by_from = 1 WHERE from_id = ?", id); err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM messages WHERE deleted_by_from = 1 AND deleted_by_to = 1")
		return err
	})
}

func requireRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// collect scans every row with scan and closes rows.
func collect[T any](rows *sql.Rows, scan func(scanner) (T, error)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PendingUsers lists accounts waiting for approval, oldest first.
func (s *Store) PendingUsers() ([]User, error) {
	rows, err := s.db.Query("SELECT " + userCols + " FROM users WHERE is_validated = 0 ORDER BY id")
	if err != nil {
		return nil, err
	}
	return collect(rows, scanUser)
}

// ValidateUser approves a new account; only sysops may.
func (s *Store) ValidateUser(actorID, id int64) error {
	if err := s.requireActor(actorID, id, false); err != nil {
		return err
	}
	return s.updateUser(id, "UPDATE users SET is_validated = 1 WHERE id = ?", id)
}

// SysopIDs lists every sysop, for notifying them.
func (s *Store) SysopIDs() ([]int64, error) {
	rows, err := s.db.Query("SELECT id FROM users WHERE is_sysop = 1")
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (int64, error) {
		var id int64
		return id, row.Scan(&id)
	})
}
