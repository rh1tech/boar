package bbs

import (
	"log/slog"
	"sync"
	"time"
)

// hitStore persists rate-limit events; *store.Store implements it.
type hitStore interface {
	RecordHit(bucket, key string, at time.Time) error
	ClearHits(bucket, key string) error
	LoadHits(bucket string, since time.Time) (map[string][]time.Time, error)
}

// rateLimiter counts events per key (an IP address, a handle) in a sliding
// window. With a hitStore it also saves them, so a restart doesn't wipe the
// slate clean for an attacker.
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
	now    func() time.Time
	swept  time.Time

	bucket string
	db     hitStore
	log    *slog.Logger
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, hits: make(map[string][]time.Time), now: time.Now}
}

// newPersistentLimiter is a rateLimiter backed by db, loaded with the
// events still inside its window.
func newPersistentLimiter(limit int, window time.Duration, bucket string, db hitStore, log *slog.Logger) *rateLimiter {
	r := newRateLimiter(limit, window)
	r.bucket, r.db, r.log = bucket, db, log
	hits, err := db.LoadHits(bucket, r.now().Add(-window))
	if err != nil {
		log.Error("loading rate limits; starting empty", "bucket", bucket, "err", err)
		return r
	}
	r.hits = hits
	return r
}

// blocked reports whether key has reached the limit.
func (r *rateLimiter) blocked(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.recent(key)) >= r.limit
}

func (r *rateLimiter) hit(key string) {
	now := r.now()
	r.mu.Lock()
	r.hits[key] = append(r.recent(key), now)
	r.sweep()
	r.mu.Unlock()
	if r.db != nil {
		if err := r.db.RecordHit(r.bucket, key, now); err != nil {
			r.log.Error("saving rate limit", "bucket", r.bucket, "err", err)
		}
	}
}

// reset forgets key's events.
func (r *rateLimiter) reset(key string) {
	r.mu.Lock()
	delete(r.hits, key)
	r.mu.Unlock()
	if r.db != nil {
		if err := r.db.ClearHits(r.bucket, key); err != nil {
			r.log.Error("clearing rate limit", "bucket", r.bucket, "err", err)
		}
	}
}

// stats returns how many events key has in the window and when the last was.
func (r *rateLimiter) stats(key string) (int, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	hits := r.recent(key)
	if len(hits) == 0 {
		return 0, time.Time{}
	}
	return len(hits), hits[len(hits)-1]
}

// sweep drops keys that stopped calling, at most once per window, so the
// map cannot grow without bound.
func (r *rateLimiter) sweep() {
	now := r.now()
	if now.Sub(r.swept) < r.window {
		return
	}
	r.swept = now
	for key := range r.hits {
		r.recent(key)
	}
}

// recent drops expired hits for key and returns what is left.
// The caller must hold r.mu.
func (r *rateLimiter) recent(key string) []time.Time {
	cutoff := r.now().Add(-r.window)
	var kept []time.Time
	for _, t := range r.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(r.hits, key)
		return nil
	}
	r.hits[key] = kept
	return kept
}
