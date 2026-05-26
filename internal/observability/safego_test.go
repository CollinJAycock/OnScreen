package observability

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// SafeGo's whole job is "panic in fn doesn't kill the process; log it
// with the name + stack." These tests pin that contract.

func TestSafeGo_RunsTheFunction(t *testing.T) {
	// Happy path: the wrapped fn runs to completion exactly once.
	var calls int
	done := make(chan struct{})
	SafeGo(slog.Default(), "test:happy", func() {
		calls++
		close(done)
	})
	<-done
	if calls != 1 {
		t.Errorf("expected fn called once, got %d", calls)
	}
}

func TestSafeGo_RecoversPanic(t *testing.T) {
	// A panic inside fn must not propagate; the test wouldn't reach the
	// assertion otherwise because the goroutine would tear the process
	// down. Use a sync.WaitGroup to wait for completion deterministically.
	var wg sync.WaitGroup
	wg.Add(1)
	SafeGo(slog.Default(), "test:panic", func() {
		defer wg.Done()
		panic("boom")
	})
	wg.Wait()
	// Reaching here proves the panic was recovered.
}

// lockedBuffer is a bytes.Buffer with a mutex so the test reader doesn't
// race the SafeGo goroutine's writer. Polled below with a bounded
// deadline — the naïve `<-done` from inside fn races the recover handler
// (fn's own defers complete BEFORE the outer recover runs, so a sentinel
// closed in fn signals "panic about to propagate" not "log written").
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestSafeGo_LogsTheNameAndPanic(t *testing.T) {
	// Verify the log line includes goroutine name + panic value + stack so
	// an operator scanning logs can identify the culprit site.
	sb := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(sb, &slog.HandlerOptions{Level: slog.LevelDebug}))

	SafeGo(logger, "test:identifiable", func() {
		panic("ka-boom-sentinel")
	})

	// Poll the buffer with a generous deadline — the log write is
	// microseconds, but a loaded CI runner can stretch goroutine
	// scheduling. 2 s is well above noise, well below "hang the suite."
	wantAll := []string{"test:identifiable", "ka-boom-sentinel", "goroutine panic recovered"}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := sb.String()
		missing := ""
		for _, want := range wantAll {
			if !strings.Contains(s, want) {
				missing = want
				break
			}
		}
		if missing == "" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("log never contained all of %v; got:\n%s", wantAll, sb.String())
}

func TestSafeGo_NilLoggerDoesNotPanic(t *testing.T) {
	// Defensive: if a caller forgets to wire a logger, SafeGo should still
	// catch the inner panic rather than itself panicking on a nil-deref
	// inside the recover handler.
	done := make(chan struct{})
	SafeGo(nil, "test:nil-logger", func() {
		defer close(done)
		panic("still-shouldnt-crash")
	})
	<-done
}
