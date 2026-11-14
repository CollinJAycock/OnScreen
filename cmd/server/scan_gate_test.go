package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// At most limit scans hold the gate at once, and every waiter runs once a
// slot frees.
func TestScanGate_BoundsConcurrentScans(t *testing.T) {
	g := newScanGate(func() int { return 2 })
	var running, peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.acquire(context.Background()); err != nil {
				t.Error(err)
				return
			}
			defer g.release()
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			running.Add(-1)
		}()
	}
	wg.Wait()
	if p := peak.Load(); p != 2 {
		t.Errorf("peak concurrent scans = %d, want 2", p)
	}
}

// A limit of zero (or an unset provider) still lets one scan through.
func TestScanGate_NonPositiveLimitIsOne(t *testing.T) {
	for _, g := range []*scanGate{newScanGate(func() int { return 0 }), newScanGate(nil)} {
		if err := g.acquire(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		if err := g.acquire(ctx); err == nil {
			t.Error("second acquire under a limit of one succeeded")
			g.release()
		}
		cancel()
		g.release()
	}
}

// A waiter gives up when its context ends (server shutdown).
func TestScanGate_WaiterHonoursContext(t *testing.T) {
	g := newScanGate(func() int { return 1 })
	if err := g.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer g.release()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.acquire(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("acquire returned nil after its context was cancelled")
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not return after cancel")
	}
}
