package ratelimit

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLimiter is a Redis-backed fixed-window limiter — the shared-state
// counterpart to Limiter. Multiple API process instances sharing the same
// Redis see the same counters, so a limit is enforced across the whole
// deployment rather than per-process (ADR-003: Redis is reserved for
// exactly this kind of ephemeral coordination state, never a system of
// record — losing these counters on a Redis restart is acceptable).
type RedisLimiter struct {
	client   *redis.Client
	prefix   string
	limit    int
	interval time.Duration
	log      *slog.Logger
}

// NewRedis builds a RedisLimiter allowing up to limit calls per interval
// per key, all keys namespaced under prefix (so e.g. the login and ai.chat
// limiters sharing one Redis instance never collide on the same key).
func NewRedis(client *redis.Client, prefix string, limit int, interval time.Duration, log *slog.Logger) *RedisLimiter {
	return &RedisLimiter{client: client, prefix: prefix, limit: limit, interval: interval, log: log}
}

// Allow reports whether a call under key is permitted right now, recording
// the call if so. Implemented as INCR followed by a one-time EXPIRE on the
// first increment of a window — the window resets itself; there's no
// separate sweep like Limiter's, since Redis's own TTL does that job.
//
// A Redis error fails OPEN (allows the call) rather than rejecting every
// request in the process for as long as Redis is unreachable: losing rate
// limiting during a Redis outage is a bounded, logged degradation, while
// failing closed would turn a Redis blip into a full login/signup/AI
// outage for every legitimate user — a worse failure mode for a control
// plane whose whole point is staying operable. The error is logged so the
// degradation is visible, not silent.
func (l *RedisLimiter) Allow(key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	fullKey := l.prefix + ":" + key
	count, err := l.client.Incr(ctx, fullKey).Result()
	if err != nil {
		l.log.Error("rate limiter: redis unavailable, failing open", "error", err, "prefix", l.prefix)
		return true
	}
	if count == 1 {
		// Only the caller that just created the key sets its expiry, so a
		// concurrent Incr from another instance can't race a fresh TTL
		// onto an already-counting window.
		if err := l.client.Expire(ctx, fullKey, l.interval).Err(); err != nil {
			l.log.Error("rate limiter: failed to set window expiry", "error", err, "prefix", l.prefix)
		}
	}
	return count <= int64(l.limit)
}
