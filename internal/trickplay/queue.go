package trickplay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ItemGenerator produces trickplay artifacts for one item. *Service and
// *Generator satisfy it.
type ItemGenerator interface {
	Generate(ctx context.Context, itemID uuid.UUID) error
}

// QueueStore is the bookkeeping the queue writes around a generation run,
// beyond what the Generator records itself. *PgStore satisfies it.
type QueueStore interface {
	MarkFailed(ctx context.Context, itemID uuid.UUID, reason string) error
	MarkSkipped(ctx context.Context, itemID uuid.UUID, reason string) error
	ResetInterrupted(ctx context.Context, itemID uuid.UUID) error
}

// BusyFunc reports whether generation should hold off right now — wired to
// "a transcode session is active" so sprite extraction (a full software
// decode of the file) never competes with a viewer's stream for CPU.
type BusyFunc func(ctx context.Context) bool

// Outcome classifies one queued generation.
type Outcome int

const (
	// OutcomeDone: sprites + index written, status 'done'.
	OutcomeDone Outcome = iota + 1
	// OutcomeFailed: ffmpeg (or the output write) failed; status 'failed'.
	// The automatic paths don't retry it.
	OutcomeFailed
	// OutcomeSkipped: the item has no usable file or no duration; status
	// 'skipped' so it stops being listed.
	OutcomeSkipped
	// OutcomeDuplicate: the item was already queued or being generated;
	// nothing was done by this call.
	OutcomeDuplicate
)

// ErrNotStarted is returned by Process when the generation slot (or an idle
// moment with no transcode running) didn't come up before the caller's
// start deadline. Nothing was written for the item.
var ErrNotStarted = errors.New("trickplay: generation could not start before the deadline")

const (
	// DefaultMaxPending caps the in-memory backlog. Enqueue drops IDs beyond
	// it; they are still "missing" in the database, so the next scan or the
	// nightly backfill lists them again once the backlog drains.
	DefaultMaxPending = 20000
	// maxCandidateBatch bounds one candidate listing (and so one enqueue).
	maxCandidateBatch = DefaultMaxPending
	// defaultBusyPoll is how often a paused queue re-checks for an idle box.
	defaultBusyPoll = 30 * time.Second
	// bookkeepingTimeout bounds the status writes made after a run, which
	// use a context detached from the (possibly cancelled) run context.
	bookkeepingTimeout = 5 * time.Second
)

// Queue is the process-wide trickplay generation worker: one bounded
// in-memory FIFO drained by a single goroutine (Run), plus a synchronous
// entry point (Process) for the scheduled backfill. Both share one
// generation slot, so at most one ffmpeg sprite job runs at a time no matter
// how many scans, admin requests and backfill runs pile up. IDs are deduped
// across the backlog and whatever is generating, so a flapping scan or a
// repeated "Generate now" can't queue the same item twice.
type Queue struct {
	gen        ItemGenerator
	store      QueueStore
	logger     *slog.Logger
	busy       BusyFunc
	busyPoll   time.Duration
	maxPending int

	slot chan struct{} // capacity 1: the single generation permit
	wake chan struct{} // capacity 1: nudges Run when work arrives

	mu         sync.Mutex
	pending    []uuid.UUID
	pendingSet map[uuid.UUID]struct{}
	inflight   map[uuid.UUID]struct{}
}

// NewQueue builds an idle queue. Call Run (typically in its own goroutine)
// to start draining it.
func NewQueue(gen ItemGenerator, store QueueStore, logger *slog.Logger) *Queue {
	if logger == nil {
		logger = slog.Default()
	}
	return &Queue{
		gen:        gen,
		store:      store,
		logger:     logger,
		busyPoll:   defaultBusyPoll,
		maxPending: DefaultMaxPending,
		slot:       make(chan struct{}, 1),
		wake:       make(chan struct{}, 1),
		pendingSet: make(map[uuid.UUID]struct{}),
		inflight:   make(map[uuid.UUID]struct{}),
	}
}

// WithBusy makes generation wait while fn reports busy, re-checking every
// poll (a non-positive poll keeps the default). nil disables the check.
func (q *Queue) WithBusy(fn BusyFunc, poll time.Duration) *Queue {
	q.busy = fn
	if poll > 0 {
		q.busyPoll = poll
	}
	return q
}

// WithMaxPending overrides the backlog cap (n <= 0 keeps the default).
func (q *Queue) WithMaxPending(n int) *Queue {
	if n > 0 {
		q.maxPending = n
	}
	return q
}

// MaxPending reports the backlog cap.
func (q *Queue) MaxPending() int { return q.maxPending }

// Enqueue appends ids to the backlog, skipping any already queued or being
// generated, and returns how many were added. IDs beyond the backlog cap
// are dropped (see DefaultMaxPending). Never blocks on generation.
func (q *Queue) Enqueue(ids ...uuid.UUID) int {
	q.mu.Lock()
	added := 0
	for _, id := range ids {
		if q.knownLocked(id) {
			continue
		}
		if len(q.pending) >= q.maxPending {
			break
		}
		q.pending = append(q.pending, id)
		q.pendingSet[id] = struct{}{}
		added++
	}
	q.mu.Unlock()
	if added > 0 {
		q.signal()
	}
	return added
}

// EnqueueFront puts id at the head of the backlog — an admin asking for one
// specific item shouldn't wait behind a whole library. Moves it forward if
// it is already queued; no-op if it is generating right now. Allowed past
// the backlog cap: it's one explicit request, not a flood.
func (q *Queue) EnqueueFront(id uuid.UUID) {
	q.mu.Lock()
	if _, running := q.inflight[id]; running {
		q.mu.Unlock()
		return
	}
	if _, queued := q.pendingSet[id]; queued {
		for i, p := range q.pending {
			if p == id {
				q.pending = append(q.pending[:i], q.pending[i+1:]...)
				break
			}
		}
	}
	q.pending = append([]uuid.UUID{id}, q.pending...)
	q.pendingSet[id] = struct{}{}
	q.mu.Unlock()
	q.signal()
}

// Len reports how many items are waiting (not counting one generating now).
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// Has reports whether id is queued or being generated.
func (q *Queue) Has(id uuid.UUID) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.knownLocked(id)
}

// Run drains the backlog one item at a time until ctx is cancelled. On
// cancellation an in-flight ffmpeg is killed (the generator runs it under
// ctx), its half-written 'pending' row is reset so the item is picked up
// again after restart, and Run returns. Items still in the backlog are
// dropped — they remain "missing" in the database, so the next scan or
// backfill re-lists them.
func (q *Queue) Run(ctx context.Context) {
	for {
		id, ok := q.next()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-q.wake:
			}
			continue
		}
		if err := q.acquire(ctx); err != nil {
			q.finish(id)
			return
		}
		_, _ = q.generate(ctx, id)
		q.releaseSlot()
		q.finish(id)
		if ctx.Err() != nil {
			return
		}
	}
}

// Process generates one item on the caller's goroutine, sharing the worker's
// generation slot. It waits for the slot (and for an idle moment when a busy
// check is configured) only until startBy — a zero startBy waits as long as
// ctx allows — and returns ErrNotStarted if that passes first. Once started,
// generation runs to completion under ctx; startBy never kills a running
// ffmpeg. Items already queued or generating return OutcomeDuplicate.
func (q *Queue) Process(ctx context.Context, id uuid.UUID, startBy time.Time) (Outcome, error) {
	if !q.claim(id) {
		return OutcomeDuplicate, nil
	}
	defer q.finish(id)

	waitCtx := ctx
	if !startBy.IsZero() {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithDeadline(ctx, startBy)
		defer cancel()
	}
	if err := q.acquire(waitCtx); err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, ErrNotStarted
	}
	defer q.releaseSlot()
	return q.generate(ctx, id)
}

// knownLocked reports whether id is queued or generating. Caller holds mu.
func (q *Queue) knownLocked(id uuid.UUID) bool {
	if _, ok := q.pendingSet[id]; ok {
		return true
	}
	_, ok := q.inflight[id]
	return ok
}

// next pops the head of the backlog and marks it in flight.
func (q *Queue) next() (uuid.UUID, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return uuid.Nil, false
	}
	id := q.pending[0]
	q.pending = q.pending[1:]
	if len(q.pending) == 0 {
		q.pending = nil // let the drained backing array go
	}
	delete(q.pendingSet, id)
	q.inflight[id] = struct{}{}
	return id, true
}

// claim marks id in flight for Process; false if it's already known.
func (q *Queue) claim(id uuid.UUID) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.knownLocked(id) {
		return false
	}
	q.inflight[id] = struct{}{}
	return true
}

func (q *Queue) finish(id uuid.UUID) {
	q.mu.Lock()
	delete(q.inflight, id)
	q.mu.Unlock()
}

func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// acquire takes the generation slot, then waits for the busy check to clear.
// Holding the slot while paused is deliberate: nothing else may generate
// during a transcode either.
func (q *Queue) acquire(ctx context.Context) error {
	select {
	case q.slot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if q.busy == nil {
		return nil
	}
	logged := false
	for q.busy(ctx) {
		if !logged {
			q.logger.InfoContext(ctx, "trickplay generation paused: transcode in progress")
			logged = true
		}
		t := time.NewTimer(q.busyPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			q.releaseSlot()
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

func (q *Queue) releaseSlot() { <-q.slot }

// generate runs one item and records the queue-level bookkeeping the
// Generator doesn't: skipped items, shutdown resets, and panics.
func (q *Queue) generate(ctx context.Context, id uuid.UUID) (out Outcome, err error) {
	defer func() {
		if v := recover(); v != nil {
			q.logger.ErrorContext(ctx, "trickplay generation panicked",
				"item_id", id, "panic", v, "stack", string(debug.Stack()))
			q.bookkeep(ctx, id, "mark failed", func(c context.Context) error {
				return q.store.MarkFailed(c, id, fmt.Sprintf("panic: %v", v))
			})
			out, err = OutcomeFailed, nil
		}
	}()

	genErr := q.gen.Generate(ctx, id)
	switch {
	case genErr == nil:
		return OutcomeDone, nil
	case ctx.Err() != nil:
		// Shutdown killed ffmpeg before the generator could record a
		// failure (its write used the cancelled ctx), leaving 'pending'.
		// Put the item back to not-started so it's retried after restart
		// instead of looking orphaned for hours.
		q.bookkeep(ctx, id, "reset interrupted", func(c context.Context) error {
			return q.store.ResetInterrupted(c, id)
		})
		return 0, ctx.Err()
	case errors.Is(genErr, ErrNoFile), errors.Is(genErr, ErrZeroDuration):
		q.bookkeep(ctx, id, "mark skipped", func(c context.Context) error {
			return q.store.MarkSkipped(c, id, genErr.Error())
		})
		return OutcomeSkipped, nil
	default:
		q.logger.WarnContext(ctx, "trickplay generation failed", "item_id", id, "err", genErr)
		return OutcomeFailed, nil
	}
}

// bookkeep runs a status write on a short context detached from ctx, so it
// still lands when ctx is the one that just got cancelled.
func (q *Queue) bookkeep(ctx context.Context, id uuid.UUID, what string, fn func(context.Context) error) {
	if q.store == nil {
		return
	}
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), bookkeepingTimeout)
	defer cancel()
	if err := fn(c); err != nil {
		q.logger.WarnContext(ctx, "trickplay queue bookkeeping failed",
			"item_id", id, "op", what, "err", err)
	}
}
