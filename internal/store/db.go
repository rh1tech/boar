// SPDX-License-Identifier: GPL-3.0-or-later

// Package store keeps everything the BBS knows in SQLite: users, private
// mail, public boards, bulletins, oneliners and the event log.
//
// The pure-Go modernc.org/sqlite driver keeps the BBS a single static binary.
// The pool is limited to one connection: SQLite serialises writers anyway, and
// a BBS never comes close to needing more.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrHandleTaken    = errors.New("That handle is already taken.")
	ErrBadCredentials = errors.New("Invalid handle or password.")
	ErrNotFound       = errors.New("Not found.")
	ErrMailboxFull    = errors.New("The recipient's mailbox is full.")
	ErrBlocked        = errors.New("That caller is not accepting your mail.")
	ErrForbidden      = errors.New("You are not allowed to do that.")
	ErrNotValidated   = errors.New("Your account is waiting for sysop approval.")
)

// InputError reports input that failed validation. Its message is meant to
// be shown to the caller as-is.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, args...)}
}

type Config struct {
	Path            string
	KDFIterations   int  // 0 selects DefaultKDFIterations
	ApproveNewUsers bool // new accounts wait for a sysop before they can write
}

type Store struct {
	db         *sql.DB
	kdfIter    int
	approveNew bool
	now        func() time.Time
}

// querier is satisfied by *sql.DB and *sql.Tx. Code running inside a
// transaction must use the *sql.Tx only: with a single pooled connection,
// touching s.db there would deadlock.
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// Open opens (creating if needed) the database at cfg.Path and brings its
// schema up to date.
func Open(cfg Config) (*Store, error) {
	if cfg.Path == "" {
		return nil, errors.New("store: empty database path")
	}
	if strings.ContainsAny(cfg.Path, "?#") {
		return nil, errors.New("store: database path may not contain '?' or '#'")
	}
	iter := cfg.KDFIterations
	if iter <= 0 {
		iter = DefaultKDFIterations
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	// Create the file ourselves so it is private from the start.
	f, err := os.OpenFile(cfg.Path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", cfg.Path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("store: open %s: %w", cfg.Path, err)
	}

	dsn := "file:" + cfg.Path +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", cfg.Path, err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, kdfIter: iter, approveNew: cfg.ApproveNewUsers, now: time.Now}
	if err := s.migrate(); err != nil {
		_ = db.Close() // the migration error is the one worth reporting
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// tx runs fn in a transaction, rolling back if it returns an error.
func (s *Store) tx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback() // fn's error is the one worth reporting
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

func (s *Store) nowNano() int64 { return s.now().UnixNano() }

func fromNano(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

func toNano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// notFound maps sql.ErrNoRows to ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func exists(q querier, query string, args ...any) (bool, error) {
	var one int
	err := q.QueryRow(query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
