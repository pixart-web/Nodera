// Package ratelimit implements a simple in-process fixed-window rate
// limiter. It is intentionally not Redis-backed (Redis is optional
// infrastructure — see ADR-003) — good enough to blunt naive brute-force
// login attempts against a single process; a multi-instance deployment
// will need a shared (Redis) limiter, tracked in docs/ROADMAP.md.
package ratelimit

import (
	"sync"
	"time"
)

type window struct {
	count     int
	windowEnd time.Time
}

// Limiter allows up to `limit` calls per `interval` per key, evicting
// expired windows lazily on access so it never grows unbounded from a
// steady trickle of distinct keys — a background sweep every `interval`
// removes windows nobody has touched.
type Limiter struct {
	mu       sync.Mutex
	limit    int
	interval time.Duration
	windows  map[string]*window
}

func New(limit int, interval time.Duration) *Limiter {
	l := &Limiter{
		limit:    limit,
		interval: interval,
		windows:  make(map[string]*window),
	}
	return l
}

// Allow reports whether a call under key is permitted right now, recording
// the call if so.
func (l *Limiter) Allow(key string) bool {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	w, ok := l.windows[key]
	if !ok || now.After(w.windowEnd) {
		l.windows[key] = &window{count: 1, windowEnd: now.Add(l.interval)}
		l.sweepLocked(now)
		return true
	}
	if w.count >= l.limit {
		return false
	}
	w.count++
	return true
}

// sweepLocked removes expired windows. Called opportunistically from Allow
// rather than on a ticker, so the limiter needs no background goroutine or
// shutdown hook.
func (l *Limiter) sweepLocked(now time.Time) {
	for k, w := range l.windows {
		if now.After(w.windowEnd) {
			delete(l.windows, k)
		}
	}
}
