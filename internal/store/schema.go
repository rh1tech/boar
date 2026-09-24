// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"database/sql"
	"fmt"
)

// migrations[i] upgrades the schema from version i to i+1. Append only.
var migrations = []string{
	`
CREATE TABLE users (
	id          INTEGER PRIMARY KEY,
	handle      TEXT    NOT NULL,
	handle_key  TEXT    NOT NULL UNIQUE,
	location    TEXT    NOT NULL DEFAULT '',
	pass_hash   BLOB    NOT NULL,
	salt        BLOB    NOT NULL,
	kdf_iter    INTEGER NOT NULL,
	is_sysop    INTEGER NOT NULL DEFAULT 0,
	is_locked   INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL,
	last_login  INTEGER NOT NULL DEFAULT 0,
	calls       INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE blocks (
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	blocked_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	PRIMARY KEY (user_id, blocked_id)
);

CREATE TABLE messages (
	id              INTEGER PRIMARY KEY,
	from_id         INTEGER NOT NULL,
	to_id           INTEGER NOT NULL,
	thread_id       INTEGER NOT NULL,
	reply_to        INTEGER NOT NULL DEFAULT 0,
	subject         TEXT    NOT NULL,
	body            TEXT    NOT NULL,
	sent_at         INTEGER NOT NULL,
	read_at         INTEGER NOT NULL DEFAULT 0,
	deleted_by_from INTEGER NOT NULL DEFAULT 0,
	deleted_by_to   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX messages_inbox  ON messages (to_id, deleted_by_to);
CREATE INDEX messages_outbox ON messages (from_id, deleted_by_from);
CREATE INDEX messages_thread ON messages (thread_id);

CREATE TABLE boards (
	id          INTEGER PRIMARY KEY,
	name        TEXT    NOT NULL,
	name_key    TEXT    NOT NULL UNIQUE,
	description TEXT    NOT NULL DEFAULT '',
	sysop_only  INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL
);

CREATE TABLE posts (
	id        INTEGER PRIMARY KEY,
	board_id  INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
	author_id INTEGER NOT NULL,
	thread_id INTEGER NOT NULL,
	reply_to  INTEGER NOT NULL DEFAULT 0,
	subject   TEXT    NOT NULL,
	body      TEXT    NOT NULL,
	posted_at INTEGER NOT NULL
);
CREATE INDEX posts_board ON posts (board_id, id);

CREATE TABLE board_reads (
	user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	board_id     INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
	last_read_id INTEGER NOT NULL,
	PRIMARY KEY (user_id, board_id)
);

CREATE TABLE bulletins (
	id        INTEGER PRIMARY KEY,
	author_id INTEGER NOT NULL,
	title     TEXT    NOT NULL,
	body      TEXT    NOT NULL,
	posted_at INTEGER NOT NULL
);

CREATE TABLE oneliners (
	id        INTEGER PRIMARY KEY,
	author_id INTEGER NOT NULL,
	text      TEXT    NOT NULL,
	posted_at INTEGER NOT NULL
);

CREATE TABLE events (
	id      INTEGER PRIMARY KEY,
	at      INTEGER NOT NULL,
	kind    TEXT    NOT NULL,
	user_id INTEGER NOT NULL DEFAULT 0,
	handle  TEXT    NOT NULL DEFAULT '',
	ip      TEXT    NOT NULL DEFAULT '',
	detail  TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX events_kind ON events (kind, id);

INSERT INTO boards (name, name_key, description, sysop_only, created_at) VALUES
	('Announcements', 'announcements', 'News from the sysop', 1, 0),
	('General',       'general',       'Anything goes',       0, 0);
`,
	`
ALTER TABLE users ADD COLUMN email      TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN email_mode INTEGER NOT NULL DEFAULT 0;

CREATE TABLE email_verifications (
	user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	email      TEXT    NOT NULL,
	code_hash  BLOB    NOT NULL,
	expires_at INTEGER NOT NULL,
	attempts   INTEGER NOT NULL DEFAULT 0
);
`,
	`
CREATE UNIQUE INDEX users_email ON users (lower(email)) WHERE email != '';
`,
	`
ALTER TABLE users ADD COLUMN is_validated INTEGER NOT NULL DEFAULT 1;

CREATE TABLE throttle_hits (
	bucket TEXT    NOT NULL,
	key    TEXT    NOT NULL,
	at     INTEGER NOT NULL
);
CREATE INDEX throttle_hits_bucket ON throttle_hits (bucket, key, at);

CREATE TABLE ssh_keys (
	id          INTEGER PRIMARY KEY,
	user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	fingerprint TEXT    NOT NULL UNIQUE,
	public_key  TEXT    NOT NULL,
	comment     TEXT    NOT NULL DEFAULT '',
	added_at    INTEGER NOT NULL
);
`,
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("store: database schema v%d is newer than this program (v%d)", version, len(migrations))
	}
	for v := version; v < len(migrations); v++ {
		err := s.tx(func(tx *sql.Tx) error {
			if _, err := tx.Exec(migrations[v]); err != nil {
				return err
			}
			_, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v+1))
			return err
		})
		if err != nil {
			return fmt.Errorf("store: migrate to v%d: %w", v+1, err)
		}
	}
	return nil
}
