// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import "sync"

// connLimiter caps open connections in total and per address key.
type connLimiter struct {
	mu       sync.Mutex
	maxTotal int
	maxPerIP int
	total    int
	perIP    map[string]int
}

func newConnLimiter(maxTotal, maxPerIP int) *connLimiter {
	return &connLimiter{maxTotal: maxTotal, maxPerIP: maxPerIP, perIP: make(map[string]int)}
}

// acquire reserves a slot for key, reporting false if a cap is reached.
func (c *connLimiter) acquire(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.total >= c.maxTotal || c.perIP[key] >= c.maxPerIP {
		return false
	}
	c.total++
	c.perIP[key]++
	return true
}

func (c *connLimiter) release(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total--
	if c.perIP[key]--; c.perIP[key] <= 0 {
		delete(c.perIP, key)
	}
}
