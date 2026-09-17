package ratelimit_test

import (
	"testing"
	"time"

	"github.com/nodera/nodera/internal/platform/ratelimit"
)

func TestLimiter_AllowsUpToLimitThenBlocks(t *testing.T) {
	l := ratelimit.New(3, time.Minute)

	for i := 0; i < 3; i++ {
		if !l.Allow("key-a") {
			t.Fatalf("expected call %d to be allowed", i+1)
		}
	}
	if l.Allow("key-a") {
		t.Fatal("expected the 4th call within the window to be blocked")
	}
}

func TestLimiter_KeysAreIndependent(t *testing.T) {
	l := ratelimit.New(1, time.Minute)

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

func TestLimiter_WindowResetsAfterInterval(t *testing.T) {
	l := ratelimit.New(1, 30*time.Millisecond)

	if !l.Allow("key-a") {
		t.Fatal("expected first call to be allowed")
	}
	if l.Allow("key-a") {
		t.Fatal("expected second call within the window to be blocked")
	}

	time.Sleep(50 * time.Millisecond)

	if !l.Allow("key-a") {
		t.Fatal("expected a call after the window expired to be allowed again")
	}
}
