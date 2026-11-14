package main

import (
	"context"
	"sync"
	"time"
)

// scanGateRecheck is how often a waiting scan re-reads the limit, so raising
// Settings ▸ System ▸ library scan concurrency lets queued scans start
// without waiting for a running one to finish.
const scanGateRecheck = 5 * time.Second

// scanGate bounds how many full library scans run at once. Every due library
// used to start its scan immediately, each with its own file-concurrency pool,
// all on the database pool the per-request auth check uses: a server with
// several large libraries due at once ran them all in parallel and starved
// interactive requests. SCAN_LIBRARY_CONCURRENCY (System settings) was meant
// to bound that, but nothing read it. The limit is read on every acquire, so
// a change applies to the next scan that starts.
type scanGate struct {
	limit func() int

	mu      sync.Mutex
	running int
	// wake is closed and replaced whenever a slot frees, waking every waiter
	// to re-check.
	wake chan struct{}
}

func newScanGate(limit func() int) *scanGate {
	return &scanGate{limit: limit, wake: make(chan struct{})}
}

// acquire blocks until a scan slot is free or ctx ends. A limit below one
// counts as one: scans can be slowed, never switched off from here.
func (g *scanGate) acquire(ctx context.Context) error {
	t := time.NewTimer(scanGateRecheck)
	defer t.Stop()
	for {
		g.mu.Lock()
		n := 1
		if g.limit != nil {
			if l := g.limit(); l > 1 {
				n = l
			}
		}
		if g.running < n {
			g.running++
			g.mu.Unlock()
			return nil
		}
		wake := g.wake
		g.mu.Unlock()

		select {
		case <-wake:
		case <-t.C:
			t.Reset(scanGateRecheck)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// release frees the slot taken by a successful acquire.
func (g *scanGate) release() {
	g.mu.Lock()
	g.running--
	close(g.wake)
	g.wake = make(chan struct{})
	g.mu.Unlock()
}
