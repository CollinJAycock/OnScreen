package valkey

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A concurrent burst must consume one slot per attempt. The old gate only
// READ the counter and incremented after the (slow) credential check, so every
// goroutine in a burst saw the same under-cap count and all got through.
func TestCheckFailures_ConcurrentBurstConsumesSlots(t *testing.T) {
	r, _, _ := newTestLimiter(t)
	ctx := context.Background()
	const limit, attempts = 10, 60

	var allowed atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if ok, err := r.CheckFailures(ctx, "burst", limit); err == nil && ok {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := allowed.Load(); got != limit {
		t.Fatalf("concurrent burst: %d attempts admitted, want exactly %d (the cap)", got, limit)
	}
}

// Every admitted attempt that fails commits; once the cap of failures is
// reached further attempts are refused until a success resets the counter.
func TestCheckFailures_CommittedFailuresLockOut(t *testing.T) {
	r, _, _ := newTestLimiter(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if ok, _ := r.CheckFailures(ctx, "k", 3); !ok {
			t.Fatalf("attempt %d should be admitted", i)
		}
		r.IncrFailure(ctx, "k", time.Minute)
	}
	if ok, _ := r.CheckFailures(ctx, "k", 3); ok {
		t.Fatal("4th attempt after 3 failures must be refused")
	}
	r.ResetFailures(ctx, "k")
	if ok, _ := r.CheckFailures(ctx, "k", 3); !ok {
		t.Fatal("after ResetFailures the caller must be admitted again")
	}
}

// Successes (Check + Reset) must never accumulate toward the lockout — that
// was the original reason the gate didn't simply increment.
func TestCheckFailures_SuccessesNeverLockOut(t *testing.T) {
	r, _, _ := newTestLimiter(t)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		if ok, _ := r.CheckFailures(ctx, "k", 3); !ok {
			t.Fatalf("successful attempt %d was refused — successes are being counted", i)
		}
		r.ResetFailures(ctx, "k")
	}
}

// An admitted attempt that ends in neither IncrFailure nor ResetFailures (an
// infra error path) must not hold its slot forever.
func TestCheckFailures_AbandonedReservationExpires(t *testing.T) {
	r, _, _ := newTestLimiter(t)
	ctx := context.Background()
	now := time.Now()
	r.now = func() time.Time { return now }

	if ok, _ := r.CheckFailures(ctx, "k", 1); !ok {
		t.Fatal("first attempt should be admitted")
	}
	if ok, _ := r.CheckFailures(ctx, "k", 1); ok {
		t.Fatal("slot is reserved by the in-flight attempt; second must be refused")
	}
	now = now.Add(failurePendingTTL + time.Second)
	if ok, _ := r.CheckFailures(ctx, "k", 1); !ok {
		t.Fatal("abandoned reservation should have expired and freed the slot")
	}
}

// INCR and its TTL are one atomic step: a committed counter always expires.
func TestIncrFailure_CounterAlwaysHasTTL(t *testing.T) {
	r, mr, _ := newTestLimiter(t)
	ctx := context.Background()
	if ok, _ := r.CheckFailures(ctx, "k", 5); !ok {
		t.Fatal("should be admitted")
	}
	r.IncrFailure(ctx, "k", 15*time.Minute)
	if ttl := mr.TTL("k"); ttl <= 0 || ttl > 15*time.Minute {
		t.Fatalf("failure counter TTL = %v, want (0, 15m]", ttl)
	}
	// The reservation was converted, not double-counted.
	if n, _ := mr.ZMembers(failurePendingKey("k")); len(n) != 0 {
		t.Fatalf("in-flight reservations after commit = %d, want 0", len(n))
	}
	if v, _ := mr.Get("k"); v != "1" {
		t.Fatalf("committed failures = %q, want 1", v)
	}
}

func TestCheckFailures_FailsOpenWhenValkeyDown(t *testing.T) {
	r, mr, counter := newTestLimiter(t)
	mr.Close()
	ok, err := r.CheckFailures(context.Background(), "k", 1)
	if err != nil || !ok {
		t.Fatalf("CheckFailures must fail open (ADR-015): ok=%v err=%v", ok, err)
	}
	if counter.Load() != 1 {
		t.Errorf("failOpenCounter = %d, want 1", counter.Load())
	}
}
