package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeMissingSvc struct {
	calls       atomic.Int64
	gracesSeen  []time.Duration
	mu          chan time.Duration // buffered, lets tests drain seen graces deterministically
	returnCount int
	returnErr   error
}

func (f *fakeMissingSvc) PromoteExpiredMissing(_ context.Context, grace time.Duration) (int, error) {
	f.calls.Add(1)
	f.gracesSeen = append(f.gracesSeen, grace)
	if f.mu != nil {
		select {
		case f.mu <- grace:
		default:
		}
	}
	return f.returnCount, f.returnErr
}

type fixedGrace time.Duration

func (g fixedGrace) MissingFileGracePeriod() time.Duration { return time.Duration(g) }

func TestMissingFiles_PassesGracePeriodFromProvider(t *testing.T) {
	svc := &fakeMissingSvc{returnCount: 5, mu: make(chan time.Duration, 4)}
	w := NewMissingFilesWorker(svc, fixedGrace(72*time.Hour), 10*time.Second, newSilentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	select {
	case g := <-svc.mu:
		if g != 72*time.Hour {
			t.Errorf("grace: got %v, want 72h", g)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not run immediately")
	}
}

type rotatingGrace struct {
	values []time.Duration
	idx    atomic.Int64
}

func (g *rotatingGrace) MissingFileGracePeriod() time.Duration {
	i := g.idx.Add(1) - 1
	return g.values[int(i)%len(g.values)]
}

func TestMissingFiles_ReReadsGraceEachTick(t *testing.T) {
	// Simulates SIGHUP-driven config reload (ADR-027): worker must NOT cache.
	gp := &rotatingGrace{values: []time.Duration{1 * time.Hour, 24 * time.Hour}}
	svc := &fakeMissingSvc{}
	w := NewMissingFilesWorker(svc, gp, 20*time.Millisecond, newSilentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	time.Sleep(80 * time.Millisecond)
	cancel()

	if len(svc.gracesSeen) < 2 {
		t.Fatalf("not enough ticks observed: %d", len(svc.gracesSeen))
	}
	if svc.gracesSeen[0] == svc.gracesSeen[1] {
		t.Errorf("grace was cached; rotating provider should produce distinct values, got %v", svc.gracesSeen[:2])
	}
}

func TestMissingFiles_ContinuesAfterError(t *testing.T) {
	svc := &fakeMissingSvc{returnErr: errors.New("db down")}
	w := NewMissingFilesWorker(svc, fixedGrace(time.Hour), 15*time.Millisecond, newSilentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	time.Sleep(80 * time.Millisecond)
	cancel()

	if svc.calls.Load() < 2 {
		t.Errorf("worker died on error: %d calls", svc.calls.Load())
	}
}
