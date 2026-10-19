package valkey

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLock_ExclusiveUntilRelease(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	a, ok, err := c.TryLock(ctx, "lk", time.Minute)
	if err != nil || !ok {
		t.Fatalf("first TryLock: ok=%v err=%v", ok, err)
	}
	if _, ok, err := c.TryLock(ctx, "lk", time.Minute); err != nil || ok {
		t.Fatalf("second TryLock while held: ok=%v err=%v, want false/nil", ok, err)
	}
	if err := a.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := a.Release(ctx); err != nil {
		t.Fatalf("second Release must be a no-op: %v", err)
	}
	b, ok, err := c.TryLock(ctx, "lk", time.Minute)
	if err != nil || !ok {
		t.Fatalf("TryLock after release: ok=%v err=%v", ok, err)
	}
	_ = b.Release(ctx)
}

// TestLock_ReleaseDoesNotEvictNextHolder pins the token check: a holder whose
// lease expired and was re-taken must not delete the new holder's key.
func TestLock_ReleaseDoesNotEvictNextHolder(t *testing.T) {
	c, mr := newTestClient(t)
	ctx := context.Background()

	old, ok, _ := c.TryLock(ctx, "lk", time.Hour)
	if !ok {
		t.Fatal("first TryLock failed")
	}
	// Simulate the lease expiring and another instance taking the key.
	mr.Del("lk")
	next, ok, _ := c.TryLock(ctx, "lk", time.Hour)
	if !ok {
		t.Fatal("second holder could not take the expired key")
	}
	if err := old.Release(ctx); err != nil {
		t.Fatalf("stale Release: %v", err)
	}
	if !mr.Exists("lk") {
		t.Fatal("stale holder's Release deleted the new holder's lock")
	}
	_ = next.Release(ctx)
	if mr.Exists("lk") {
		t.Fatal("owner's Release left the key behind")
	}
}

func TestLock_WaitsForHolder(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	held, ok, _ := c.TryLock(ctx, "lk", time.Minute)
	if !ok {
		t.Fatal("TryLock failed")
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = held.Release(context.Background())
	}()
	start := time.Now()
	l, err := c.Lock(ctx, "lk", time.Minute, 5*time.Second)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer l.Release(ctx)
	if time.Since(start) < 100*time.Millisecond {
		t.Error("Lock returned before the holder released")
	}
}

func TestLock_TimeoutWhileHeld(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	held, _, _ := c.TryLock(ctx, "lk", time.Minute)
	defer held.Release(ctx)
	if _, err := c.Lock(ctx, "lk", time.Minute, 120*time.Millisecond); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("Lock on a held key = %v, want ErrLockTimeout", err)
	}
}

// TestLock_RenewsWhileHeld: a critical section longer than the TTL keeps the
// lock because the lease extends itself every ttl/3.
func TestLock_RenewsWhileHeld(t *testing.T) {
	c, mr := newTestClient(t)
	ctx := context.Background()

	const ttl = 300 * time.Millisecond
	l, ok, _ := c.TryLock(ctx, "lk", ttl)
	if !ok {
		t.Fatal("TryLock failed")
	}
	defer l.Release(ctx)
	// miniredis only ages keys on FastForward: burn most of the TTL, then give
	// the renewal goroutine a real tick to push it back up.
	mr.FastForward(250 * time.Millisecond)
	time.Sleep(2 * ttl / 3)
	if got := mr.TTL("lk"); got <= 100*time.Millisecond {
		t.Fatalf("TTL after renewal window = %v, want it pushed back toward %v", got, ttl)
	}
	mr.FastForward(250 * time.Millisecond)
	if !mr.Exists("lk") {
		t.Fatal("held lease expired despite renewal")
	}
}

// TestLock_MutualExclusion runs many contenders through one key and checks no
// two were ever inside at once.
func TestLock_MutualExclusion(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	var inside, maxInside, entered int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := c.Lock(ctx, "lk", time.Minute, 10*time.Second)
			if err != nil {
				t.Errorf("Lock: %v", err)
				return
			}
			n := atomic.AddInt32(&inside, 1)
			for {
				m := atomic.LoadInt32(&maxInside)
				if n <= m || atomic.CompareAndSwapInt32(&maxInside, m, n) {
					break
				}
			}
			atomic.AddInt32(&entered, 1)
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&inside, -1)
			_ = l.Release(ctx)
		}()
	}
	wg.Wait()
	if entered != 8 || maxInside != 1 {
		t.Fatalf("entered=%d maxInside=%d, want 8/1", entered, maxInside)
	}
}
