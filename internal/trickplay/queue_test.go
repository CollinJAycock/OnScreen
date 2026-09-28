package trickplay

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// queueGen is a fake ItemGenerator that records calls, tracks peak
// concurrency, and can block, fail or panic per item.
type queueGen struct {
	mu       sync.Mutex
	calls    []uuid.UUID
	errFor   map[uuid.UUID]error
	panicFor map[uuid.UUID]bool

	active    atomic.Int32
	maxActive atomic.Int32

	// release, when non-nil, gates every Generate: it returns once a value
	// arrives (or ctx is cancelled, returning ctx.Err()).
	release chan struct{}
	// started receives each item as Generate begins (buffered).
	started chan uuid.UUID
}

func newQueueGen() *queueGen {
	return &queueGen{
		errFor:   map[uuid.UUID]error{},
		panicFor: map[uuid.UUID]bool{},
		started:  make(chan uuid.UUID, 100),
	}
}

func (g *queueGen) Generate(ctx context.Context, id uuid.UUID) error {
	n := g.active.Add(1)
	defer g.active.Add(-1)
	for {
		m := g.maxActive.Load()
		if n <= m || g.maxActive.CompareAndSwap(m, n) {
			break
		}
	}
	g.mu.Lock()
	g.calls = append(g.calls, id)
	err := g.errFor[id]
	boom := g.panicFor[id]
	g.mu.Unlock()
	g.started <- id
	if g.release != nil {
		select {
		case <-g.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if boom {
		panic("codec exploded")
	}
	return err
}

func (g *queueGen) callOrder() []uuid.UUID {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]uuid.UUID(nil), g.calls...)
}

// queueStore is a fake QueueStore recording bookkeeping writes.
type queueStore struct {
	mu      sync.Mutex
	failed  map[uuid.UUID]string
	skipped map[uuid.UUID]string
	reset   map[uuid.UUID]bool
}

func newQueueStore() *queueStore {
	return &queueStore{failed: map[uuid.UUID]string{}, skipped: map[uuid.UUID]string{}, reset: map[uuid.UUID]bool{}}
}

func (s *queueStore) MarkFailed(ctx context.Context, id uuid.UUID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed[id] = reason
	return nil
}

func (s *queueStore) MarkSkipped(ctx context.Context, id uuid.UUID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skipped[id] = reason
	return nil
}

func (s *queueStore) ResetInterrupted(ctx context.Context, id uuid.UUID) error {
	// Mirrors the real write: it must run on a live context even though the
	// run's context was the one cancelled.
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset[id] = true
	return nil
}

func ids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

// runQueue starts q.Run and returns a stop func that cancels it and waits
// for Run to return (failing the test if it doesn't).
func runQueue(t *testing.T, q *Queue) (context.Context, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		q.Run(ctx)
		close(done)
	}()
	return ctx, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Queue.Run did not return after cancel")
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestQueue_EnqueueDedupes(t *testing.T) {
	q := NewQueue(newQueueGen(), newQueueStore(), silentLogger())
	a, b := uuid.New(), uuid.New()
	if n := q.Enqueue(a, b, a); n != 2 {
		t.Fatalf("Enqueue(a,b,a) added %d, want 2", n)
	}
	if n := q.Enqueue(a, b); n != 0 {
		t.Errorf("re-Enqueue added %d, want 0", n)
	}
	if q.Len() != 2 {
		t.Errorf("Len = %d, want 2", q.Len())
	}
	if !q.Has(a) || q.Has(uuid.New()) {
		t.Error("Has mismatch")
	}
}

func TestQueue_EnqueueRespectsBacklogCap(t *testing.T) {
	q := NewQueue(newQueueGen(), newQueueStore(), silentLogger()).WithMaxPending(2)
	if n := q.Enqueue(ids(5)...); n != 2 {
		t.Fatalf("added %d past a cap of 2", n)
	}
	// An explicit single-item request is still honoured at the cap.
	extra := uuid.New()
	q.EnqueueFront(extra)
	if q.Len() != 3 || !q.Has(extra) {
		t.Errorf("EnqueueFront at cap: Len=%d Has=%v", q.Len(), q.Has(extra))
	}
}

func TestQueue_RunProcessesAllOneAtATime(t *testing.T) {
	g := newQueueGen()
	q := NewQueue(g, newQueueStore(), silentLogger())
	work := ids(6)
	q.Enqueue(work[:3]...)
	_, stop := runQueue(t, q)
	defer stop()

	// Backfill-style synchronous calls race the worker for the same slot.
	var wg sync.WaitGroup
	for _, id := range work[3:] {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			out, err := q.Process(context.Background(), id, time.Time{})
			if err != nil || out != OutcomeDone {
				t.Errorf("Process(%s) = %v, %v", id, out, err)
			}
		}(id)
	}
	wg.Wait()
	waitFor(t, "all six generated", func() bool { return len(g.callOrder()) == 6 })
	if m := g.maxActive.Load(); m != 1 {
		t.Errorf("peak concurrent generations = %d, want 1", m)
	}
	seen := map[uuid.UUID]int{}
	for _, id := range g.callOrder() {
		seen[id]++
	}
	for _, id := range work {
		if seen[id] != 1 {
			t.Errorf("item %s generated %d times, want 1", id, seen[id])
		}
	}
}

func TestQueue_InFlightItemIsDeduped(t *testing.T) {
	g := newQueueGen()
	g.release = make(chan struct{})
	q := NewQueue(g, newQueueStore(), silentLogger())
	a := uuid.New()
	q.Enqueue(a)
	_, stop := runQueue(t, q)
	defer stop()

	<-g.started // a is generating now
	if n := q.Enqueue(a); n != 0 {
		t.Errorf("Enqueue of the in-flight item added %d, want 0", n)
	}
	if out, err := q.Process(context.Background(), a, time.Time{}); err != nil || out != OutcomeDuplicate {
		t.Errorf("Process of in-flight item = %v, %v; want OutcomeDuplicate", out, err)
	}
	q.EnqueueFront(a) // no-op while running
	if q.Len() != 0 {
		t.Errorf("EnqueueFront queued the running item again (Len=%d)", q.Len())
	}
	g.release <- struct{}{}
	waitFor(t, "a to finish", func() bool { return !q.Has(a) })
	if len(g.callOrder()) != 1 {
		t.Errorf("a generated %d times", len(g.callOrder()))
	}
}

func TestQueue_EnqueueFrontJumpsTheLine(t *testing.T) {
	g := newQueueGen()
	q := NewQueue(g, newQueueStore(), silentLogger())
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	q.Enqueue(a, b, c)
	q.EnqueueFront(c)
	if q.Len() != 3 {
		t.Fatalf("moving c forward changed Len to %d", q.Len())
	}
	_, stop := runQueue(t, q)
	defer stop()
	waitFor(t, "all three", func() bool { return len(g.callOrder()) == 3 })
	got := g.callOrder()
	want := []uuid.UUID{c, a, b}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestQueue_ShutdownResetsInterruptedItemAndStops(t *testing.T) {
	g := newQueueGen()
	g.release = make(chan struct{}) // never released: only ctx ends it
	store := newQueueStore()
	q := NewQueue(g, store, silentLogger())
	work := ids(3)
	q.Enqueue(work...)
	_, stop := runQueue(t, q)

	first := <-g.started
	stop() // cancels ctx and waits for Run to return

	if !store.reset[first] {
		t.Error("interrupted item's pending row was not reset")
	}
	if len(store.failed) != 0 {
		t.Errorf("shutdown must not brand the item failed: %v", store.failed)
	}
	if n := len(g.callOrder()); n != 1 {
		t.Errorf("generated %d items after shutdown began, want only the in-flight one", n)
	}
	if q.Has(first) {
		t.Error("interrupted item still marked in flight")
	}
}

func TestQueue_RunReturnsWhenIdleAndCancelled(t *testing.T) {
	q := NewQueue(newQueueGen(), newQueueStore(), silentLogger())
	_, stop := runQueue(t, q)
	stop() // fails the test if Run doesn't return
}

func TestQueue_OutcomesAndBookkeeping(t *testing.T) {
	g := newQueueGen()
	store := newQueueStore()
	q := NewQueue(g, store, silentLogger())
	ok, nofile, zero, bad, boom := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	g.errFor[nofile] = ErrNoFile
	g.errFor[zero] = fmt.Errorf("%w: %s", ErrZeroDuration, zero)
	g.errFor[bad] = errors.New("ffmpeg: invalid data")
	g.panicFor[boom] = true

	cases := []struct {
		id   uuid.UUID
		want Outcome
	}{
		{ok, OutcomeDone},
		{nofile, OutcomeSkipped},
		{zero, OutcomeSkipped},
		{bad, OutcomeFailed},
		{boom, OutcomeFailed},
	}
	for _, tc := range cases {
		out, err := q.Process(context.Background(), tc.id, time.Time{})
		if err != nil || out != tc.want {
			t.Errorf("Process(%s) = %v, %v; want %v", tc.id, out, err, tc.want)
		}
	}
	if _, ok := store.skipped[nofile]; !ok {
		t.Error("no-file item not marked skipped")
	}
	if _, ok := store.skipped[zero]; !ok {
		t.Error("zero-duration item not marked skipped")
	}
	if _, ok := store.failed[bad]; ok {
		t.Error("the generator records ordinary failures itself; the queue must not double-write")
	}
	if reason, ok := store.failed[boom]; !ok || reason == "" {
		t.Error("panicking item not marked failed")
	}
	// The slot was released after the panic: another item still runs.
	if out, err := q.Process(context.Background(), uuid.New(), time.Time{}); err != nil || out != OutcomeDone {
		t.Errorf("after a panic, Process = %v, %v", out, err)
	}
}

func TestQueue_WorkerSurvivesPanic(t *testing.T) {
	g := newQueueGen()
	q := NewQueue(g, newQueueStore(), silentLogger())
	boom, next := uuid.New(), uuid.New()
	g.panicFor[boom] = true
	q.Enqueue(boom, next)
	_, stop := runQueue(t, q)
	defer stop()
	waitFor(t, "the item after the panic", func() bool { return len(g.callOrder()) == 2 })
}

func TestQueue_WaitsWhileBusy(t *testing.T) {
	g := newQueueGen()
	var busy atomic.Bool
	busy.Store(true)
	var checks atomic.Int32
	q := NewQueue(g, newQueueStore(), silentLogger()).
		WithBusy(func(context.Context) bool { checks.Add(1); return busy.Load() }, 5*time.Millisecond)
	a := uuid.New()
	q.Enqueue(a)
	_, stop := runQueue(t, q)
	defer stop()

	waitFor(t, "a few busy checks", func() bool { return checks.Load() >= 3 })
	if len(g.callOrder()) != 0 {
		t.Fatal("generated while a transcode was running")
	}
	busy.Store(false)
	waitFor(t, "generation after idle", func() bool { return len(g.callOrder()) == 1 })
}

func TestQueue_ProcessGivesUpAtStartDeadline(t *testing.T) {
	g := newQueueGen()
	q := NewQueue(g, newQueueStore(), silentLogger()).
		WithBusy(func(context.Context) bool { return true }, 5*time.Millisecond)
	a := uuid.New()
	out, err := q.Process(context.Background(), a, time.Now().Add(30*time.Millisecond))
	if !errors.Is(err, ErrNotStarted) || out != 0 {
		t.Fatalf("Process = %v, %v; want ErrNotStarted", out, err)
	}
	if len(g.callOrder()) != 0 {
		t.Error("generated despite never getting an idle slot")
	}
	if q.Has(a) {
		t.Error("abandoned item still claimed")
	}
	// The slot was released: a later call with no busy check can run.
	q.busy = nil
	if out, err := q.Process(context.Background(), a, time.Time{}); err != nil || out != OutcomeDone {
		t.Errorf("retry = %v, %v", out, err)
	}
}

func TestQueue_ProcessHonoursCallerCancellation(t *testing.T) {
	g := newQueueGen()
	g.release = make(chan struct{})
	store := newQueueStore()
	q := NewQueue(g, store, silentLogger())
	ctx, cancel := context.WithCancel(context.Background())
	a := uuid.New()
	done := make(chan error, 1)
	go func() {
		_, err := q.Process(ctx, a, time.Time{})
		done <- err
	}()
	<-g.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Process after cancel = %v, want context.Canceled", err)
	}
	if !store.reset[a] {
		t.Error("cancelled Process did not reset the pending row")
	}
}
