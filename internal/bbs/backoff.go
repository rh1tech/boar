// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"sync"
	"time"
)

// Per-handle login backoff. The first few wrong passwords cost nothing;
// after that each one doubles the wait before the next try, up to a cap.
// Unlike a hard lock, someone guessing against a handle can only ever make
// its owner wait backoffMax, and one successful login clears it.
const (
	backoffWindow = 15 * time.Minute
	backoffFree   = 3
	backoffBase   = 2 * time.Second
	backoffMax    = time.Minute
)

type loginBackoff struct {
	fails *rateLimiter
	mu    sync.Mutex
	busy  map[string]bool // a password check for this handle is in flight
}

func newLoginBackoff(fails *rateLimiter) *loginBackoff {
	return &loginBackoff{fails: fails, busy: make(map[string]bool)}
}

// delayFor is the wait required after n recent failures.
func delayFor(n int) time.Duration {
	if n < backoffFree {
		return 0
	}
	d := backoffBase << min(n-backoffFree, 10)
	return min(d, backoffMax)
}

// begin reserves a password check for key. If one is already running, or
// the backoff hasn't passed yet, it returns how long to wait instead. The
// reservation stops parallel connections from all guessing at once.
func (b *loginBackoff) begin(key string) (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.busy[key] {
		return backoffBase, false
	}
	n, last := b.fails.stats(key)
	if wait := time.Until(last.Add(delayFor(n))); n > 0 && wait > 0 {
		return wait, false
	}
	b.busy[key] = true
	return 0, true
}

// release drops the reservation without recording anything (the check
// itself failed, say, because the database was unavailable).
func (b *loginBackoff) release(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.busy, key)
}

// end releases the reservation and records the outcome.
func (b *loginBackoff) end(key string, ok bool) {
	b.mu.Lock()
	delete(b.busy, key)
	b.mu.Unlock()
	if ok {
		b.fails.reset(key)
	} else {
		b.fails.hit(key)
	}
}
