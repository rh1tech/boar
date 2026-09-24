// SPDX-License-Identifier: GPL-3.0-or-later

package store

import "time"

// throttleRetention is how long throttle hits are kept; longer than any
// rate-limit window.
const throttleRetention = 48 * time.Hour

// RecordHit stores one rate-limit event so limits survive a restart.
func (s *Store) RecordHit(bucket, key string, at time.Time) error {
	if _, err := s.db.Exec("INSERT INTO throttle_hits (bucket, key, at) VALUES (?, ?, ?)", bucket, key, at.UnixNano()); err != nil {
		return err
	}
	_, err := s.db.Exec("DELETE FROM throttle_hits WHERE at < ?", at.Add(-throttleRetention).UnixNano())
	return err
}

// ClearHits forgets a key's events (after a successful login, say).
func (s *Store) ClearHits(bucket, key string) error {
	_, err := s.db.Exec("DELETE FROM throttle_hits WHERE bucket = ? AND key = ?", bucket, key)
	return err
}

// LoadHits returns a bucket's events since the given time, by key.
func (s *Store) LoadHits(bucket string, since time.Time) (map[string][]time.Time, error) {
	rows, err := s.db.Query("SELECT key, at FROM throttle_hits WHERE bucket = ? AND at >= ? ORDER BY at", bucket, since.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]time.Time{}
	for rows.Next() {
		var key string
		var at int64
		if err := rows.Scan(&key, &at); err != nil {
			return nil, err
		}
		out[key] = append(out[key], time.Unix(0, at))
	}
	return out, rows.Err()
}
