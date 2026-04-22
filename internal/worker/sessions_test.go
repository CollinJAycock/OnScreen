package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSessionSvc struct {
	calls atomic.Int64
	err   error
}

func (f *fakeSessionSvc) DeleteExpiredSessions(_ context.Context) error {
	f.calls.Add(1)
	return f.err
}

func newSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSessionCleanup_RunsImmediatelyAndOnTick(t *testing.T) {
	svc := &fakeSessionSvc{}
	w := NewSessionCleanupWorker(svc, 30*time.Millisecond, newSilentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)

	// Should fire once immediately, then again after at least one tick.
	time.Sleep(120 * time.Millisecond)
	cancel()

	got := svc.calls.Load()
	if got < 2 {
		t.Errorf("calls: got %d, want ≥2 (immediate + tick)", got)
	}
}

func TestSessionCleanup_ContinuesAfterError(t *testing.T) {
	svc := &fakeSessionSvc{err: errors.New("boom")}
	w := NewSessionCleanupWorker(svc, 20*time.Millisecond, newSilentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()

	if svc.calls.Load() < 2 {
		t.Errorf("worker stopped after error: only %d calls", svc.calls.Load())
	}
}

func TestSessionCleanup_StopsOnContextCancel(t *testing.T) {
	svc := &fakeSessionSvc{}
	w := NewSessionCleanupWorker(svc, 10*time.Millisecond, newSilentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit within 1s of context cancel")
	}
}
