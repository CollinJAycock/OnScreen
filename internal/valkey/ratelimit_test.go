package valkey

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestLimiter returns a RateLimiter pointed at an in-process miniredis,
// plus a teardown that closes both. failOpenCounter increments the returned
// counter so tests can assert fail-open behaviour.
func newTestLimiter(t *testing.T) (*RateLimiter, *miniredis.Miniredis, *atomic.Int64) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	var counter atomic.Int64
	limiter := NewRateLimiter(
		&Client{rdb: rdb},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		func() { counter.Add(1) },
	)
	return limiter, mr, &counter
}

func TestAllow_UnderLimitReturnsAllowed(t *testing.T) {
	r, _, _ := newTestLimiter(t)
	ctx := context.Background()

	allowed, remaining, resetAt, err := r.Allow(ctx, "k", 5, time.Second)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !allowed {
		t.Errorf("first request should be allowed")
	}
	if remaining != 4 {
		t.Errorf("remaining: got %d, want 4", remaining)
	}
	if resetAt.IsZero() {
		t.Errorf("resetAt should be non-zero on success")
	}
}

func TestAllow_ExhaustsAtLimit(t *testing.T) {
	r, _, _ := newTestLimiter(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		allowed, _, _, err := r.Allow(ctx, "k", 3, time.Second)
		if err != nil || !allowed {
			t.Fatalf("call %d: allowed=%v err=%v", i, allowed, err)
		}
	}
	allowed, remaining, _, err := r.Allow(ctx, "k", 3, time.Second)
	if err != nil {
		t.Fatalf("4th call err: %v", err)
	}
	if allowed {
		t.Errorf("4th call should be denied (limit=3)")
	}
	if remaining != 0 {
		t.Errorf("remaining at limit: got %d, want 0", remaining)
	}
}

func TestAllow_KeysAreIsolated(t *testing.T) {
	r, _, _ := newTestLimiter(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if a, _, _, _ := r.Allow(ctx, "alice", 2, time.Second); !a {
			t.Fatalf("alice call %d should pass", i)
		}
	}
	if a, _, _, _ := r.Allow(ctx, "alice", 2, time.Second); a {
		t.Errorf("alice should be exhausted")
	}
	if a, _, _, _ := r.Allow(ctx, "bob", 2, time.Second); !a {
		t.Errorf("bob should be unaffected by alice's quota")
	}
}

func TestAllow_WindowSlidesOldEntriesOut(t *testing.T) {
	r, mr, _ := newTestLimiter(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if a, _, _, _ := r.Allow(ctx, "k", 2, 100*time.Millisecond); !a {
			t.Fatalf("call %d should pass", i)
		}
	}
	if a, _, _, _ := r.Allow(ctx, "k", 2, 100*time.Millisecond); a {
		t.Errorf("3rd call should be denied while window is full")
	}

	// Slide miniredis time forward past the window — old entries should be
	// purged by ZREMRANGEBYSCORE on the next call.
	mr.FastForward(200 * time.Millisecond)

	if a, _, _, _ := r.Allow(ctx, "k", 2, 100*time.Millisecond); !a {
		t.Errorf("after window expiry, request should be allowed again")
	}
}

func TestAllow_FailsOpenWhenValkeyDown(t *testing.T) {
	r, mr, counter := newTestLimiter(t)
	mr.Close() // simulate Valkey unavailable

	allowed, remaining, resetAt, err := r.Allow(context.Background(), "k", 5, time.Second)
	if err != nil {
		t.Errorf("fail-open should swallow error, got %v", err)
	}
	if !allowed {
		t.Errorf("must fail OPEN (allow request) per ADR-015")
	}
	if remaining != 0 {
		t.Errorf("remaining on fail-open: got %d, want 0", remaining)
	}
	if !resetAt.IsZero() {
		t.Errorf("resetAt should be zero on fail-open")
	}
	if counter.Load() != 1 {
		t.Errorf("failOpenCounter: got %d, want 1", counter.Load())
	}
}

func TestAllow_PropagatesContextCancellation(t *testing.T) {
	r, _, counter := newTestLimiter(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	allowed, _, _, err := r.Allow(ctx, "k", 5, time.Second)
	if err == nil {
		t.Fatal("expected ctx error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err: got %v, want context.Canceled", err)
	}
	if allowed {
		t.Errorf("ctx-cancelled call should NOT be allowed (don't fail open on caller cancel)")
	}
	if counter.Load() != 0 {
		t.Errorf("failOpenCounter must NOT increment on ctx cancel; got %d", counter.Load())
	}
}

func TestAllow_NilFailOpenCounterIsSafe(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	r := NewRateLimiter(&Client{rdb: rdb}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	mr.Close() // force fail-open path
	allowed, _, _, err := r.Allow(context.Background(), "k", 1, time.Second)
	if err != nil || !allowed {
		t.Fatalf("nil counter should still allow on fail-open: allowed=%v err=%v", allowed, err)
	}
}
