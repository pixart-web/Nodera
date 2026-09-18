package ratelimit_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nodera/nodera/internal/platform/ratelimit"
)

// requireRedisClient returns a connected Redis client for integration
// tests, or skips (not fails) if NODERA_TEST_REDIS_URL is not configured —
// same pattern as testhelpers.RequirePool for Postgres, so `go test ./...`
// still passes with no Redis available.
func requireRedisClient(t *testing.T) *redis.Client {
	t.Helper()

	url := os.Getenv("NODERA_TEST_REDIS_URL")
	if url == "" {
		t.Skip("NODERA_TEST_REDIS_URL not set; skipping Redis-backed rate limiter test")
	}

	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("invalid NODERA_TEST_REDIS_URL: %v", err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("failed to connect to test redis: %v", err)
	}
	return client
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// uniquePrefix keeps each test's keys isolated from every other test and
// from any previous run, without needing to FLUSHDB a shared instance.
func uniquePrefix(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test:%s:%d", t.Name(), time.Now().UnixNano())
}

func TestRedisLimiter_AllowsUpToLimitThenBlocks(t *testing.T) {
	client := requireRedisClient(t)
	l := ratelimit.NewRedis(client, uniquePrefix(t), 3, time.Minute, silentLogger())

	for i := 0; i < 3; i++ {
		if !l.Allow("key-a") {
			t.Fatalf("expected call %d to be allowed", i+1)
		}
	}
	if l.Allow("key-a") {
		t.Fatal("expected the 4th call within the window to be blocked")
	}
}

func TestRedisLimiter_KeysAreIndependent(t *testing.T) {
	client := requireRedisClient(t)
	l := ratelimit.NewRedis(client, uniquePrefix(t), 1, time.Minute, silentLogger())

	if !l.Allow("key-a") {
		t.Fatal("expected first call for key-a to be allowed")
	}
	if l.Allow("key-a") {
		t.Fatal("expected second call for key-a to be blocked")
	}
	if !l.Allow("key-b") {
		t.Fatal("expected key-b to have its own independent budget")
	}
}

func TestRedisLimiter_WindowResetsAfterInterval(t *testing.T) {
	client := requireRedisClient(t)
	l := ratelimit.NewRedis(client, uniquePrefix(t), 1, 1*time.Second, silentLogger())

	if !l.Allow("key-a") {
		t.Fatal("expected first call to be allowed")
	}
	if l.Allow("key-a") {
		t.Fatal("expected second call within the window to be blocked")
	}
	time.Sleep(1200 * time.Millisecond)
	if !l.Allow("key-a") {
		t.Fatal("expected a call after the window expired to be allowed again")
	}
}

// TestRedisLimiter_SharedAcrossClients is the whole point of this type
// over Limiter: two independent clients (standing in for two API process
// instances) pointed at the same Redis see the same counter.
func TestRedisLimiter_SharedAcrossClients(t *testing.T) {
	url := os.Getenv("NODERA_TEST_REDIS_URL")
	if url == "" {
		t.Skip("NODERA_TEST_REDIS_URL not set; skipping Redis-backed rate limiter test")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("invalid NODERA_TEST_REDIS_URL: %v", err)
	}

	clientA := redis.NewClient(opts)
	t.Cleanup(func() { _ = clientA.Close() })
	clientB := redis.NewClient(opts)
	t.Cleanup(func() { _ = clientB.Close() })

	prefix := uniquePrefix(t)
	limiterA := ratelimit.NewRedis(clientA, prefix, 2, time.Minute, silentLogger())
	limiterB := ratelimit.NewRedis(clientB, prefix, 2, time.Minute, silentLogger())

	if !limiterA.Allow("shared-key") {
		t.Fatal("expected the 1st call (via instance A) to be allowed")
	}
	if !limiterB.Allow("shared-key") {
		t.Fatal("expected the 2nd call (via instance B) to be allowed")
	}
	if limiterA.Allow("shared-key") {
		t.Fatal("expected the 3rd call (via instance A) to be blocked — instance B's call already spent the shared budget")
	}
}
