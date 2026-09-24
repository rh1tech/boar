package store

import "time"

// Event kinds recorded in the log.
const (
	EventLogin       = "login"
	EventLoginFailed = "login-failed"
	EventLocked      = "login-locked"
	EventSignup      = "signup"
	EventSysop       = "sysop"
)

// maxEvents bounds the log; older entries are pruned.
const maxEvents = 5000

type Event struct {
	ID     int64
	At     time.Time
	Kind   string
	UserID int64
	Handle string
	IP     string
	Detail string
}

// LogEvent appends to the event log (the caller list and sysop audit trail).
func (s *Store) LogEvent(e Event) error {
	_, err := s.db.Exec("INSERT INTO events (at, kind, user_id, handle, ip, detail) VALUES (?, ?, ?, ?, ?, ?)",
		s.nowNano(), e.Kind, e.UserID, e.Handle, e.IP, e.Detail)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("DELETE FROM events WHERE id <= (SELECT MAX(id) FROM events) - ?", maxEvents)
	return err
}

// Events returns the newest n events, newest first. kind "" means all kinds.
func (s *Store) Events(kind string, n int) ([]Event, error) {
	rows, err := s.db.Query(`SELECT id, at, kind, user_id, handle, ip, detail FROM events
		WHERE ? = '' OR kind = ? ORDER BY id DESC LIMIT ?`, kind, kind, n)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (Event, error) {
		var e Event
		var at int64
		err := row.Scan(&e.ID, &at, &e.Kind, &e.UserID, &e.Handle, &e.IP, &e.Detail)
		e.At = fromNano(at)
		return e, err
	})
}
