package trickplay

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// CandidateFilter selects items that still need sprites. See the
// ListTrickplayCandidates query for the eligibility rules.
type CandidateFilter struct {
	// LibraryID scopes the listing to one library; nil spans all.
	LibraryID *uuid.UUID
	// OnlyEnabled restricts to libraries with trickplay_enabled — the
	// automatic paths set it; an explicit admin request does not.
	OnlyEnabled bool
	// IncludeFailed also returns items whose last attempt failed.
	IncludeFailed bool
	// Limit caps the result (clamped to the queue's backlog cap).
	Limit int
}

// LibraryCounts is the per-library progress shown on the settings page.
// Pending covers both not-yet-started and in-progress items.
type LibraryCounts struct {
	Total   int64
	Done    int64
	Pending int64
	Failed  int64
}

// NewLibraryCounts derives Pending from the three counted buckets.
func NewLibraryCounts(total, done, failed int64) LibraryCounts {
	pending := total - done - failed
	if pending < 0 {
		pending = 0
	}
	return LibraryCounts{Total: total, Done: done, Pending: pending, Failed: failed}
}

// CandidateStore is the read side of automatic generation. *PgStore
// satisfies it.
type CandidateStore interface {
	ListCandidates(ctx context.Context, f CandidateFilter) ([]uuid.UUID, error)
	CountCandidates(ctx context.Context, libraryID *uuid.UUID, onlyEnabled bool) (int64, error)
	LibraryCounts(ctx context.Context, libraryID uuid.UUID) (LibraryCounts, error)
}

// Planner decides what to generate and feeds the Queue: the post-scan hook,
// the admin per-library endpoints, and the scheduled backfill all go through
// it so every path shares one worker, one dedupe set and one eligibility
// rule.
type Planner struct {
	queue  *Queue
	store  CandidateStore
	logger *slog.Logger
}

// NewPlanner wires a planner over an existing queue and candidate store.
func NewPlanner(queue *Queue, store CandidateStore, logger *slog.Logger) *Planner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Planner{queue: queue, store: store, logger: logger}
}

// AfterScan queues the library's items that still need sprites, if the
// library has trickplay enabled (the candidate query enforces the toggle, so
// a disabled library lists nothing). Returns how many items were newly
// queued. Never waits on generation — the scan that called it finishes
// immediately.
func (p *Planner) AfterScan(ctx context.Context, libraryID uuid.UUID) (int, error) {
	ids, err := p.store.ListCandidates(ctx, CandidateFilter{
		LibraryID:   &libraryID,
		OnlyEnabled: true,
		Limit:       p.queue.MaxPending(),
	})
	if err != nil {
		return 0, fmt.Errorf("trickplay after scan: %w", err)
	}
	n := p.queue.Enqueue(ids...)
	if n > 0 {
		p.logger.InfoContext(ctx, "trickplay: queued items after scan",
			"library_id", libraryID, "queued", n, "backlog", p.queue.Len())
	}
	return n, nil
}

// QueueLibrary queues every item in the library that still needs sprites,
// regardless of the library's toggle — it backs the admin "Generate now"
// button, an explicit request. includeFailed also retries failed items.
// Idempotent: items already queued or generating aren't added twice.
// Returns how many items were newly queued.
func (p *Planner) QueueLibrary(ctx context.Context, libraryID uuid.UUID, includeFailed bool) (int, error) {
	ids, err := p.store.ListCandidates(ctx, CandidateFilter{
		LibraryID:     &libraryID,
		IncludeFailed: includeFailed,
		Limit:         p.queue.MaxPending(),
	})
	if err != nil {
		return 0, fmt.Errorf("trickplay queue library: %w", err)
	}
	n := p.queue.Enqueue(ids...)
	p.logger.InfoContext(ctx, "trickplay: library generation requested",
		"library_id", libraryID, "candidates", len(ids), "queued", n)
	return n, nil
}

// LibraryCounts returns the progress counts for one library.
func (p *Planner) LibraryCounts(ctx context.Context, libraryID uuid.UUID) (LibraryCounts, error) {
	return p.store.LibraryCounts(ctx, libraryID)
}

// QueueItem puts one item at the head of the queue (per-item admin
// regenerate), so it shares the single worker with everything else.
func (p *Planner) QueueItem(itemID uuid.UUID) { p.queue.EnqueueFront(itemID) }

// ListBackfillCandidates lists up to limit items needing sprites across all
// trickplay-enabled libraries, oldest first. Failed items are not retried.
func (p *Planner) ListBackfillCandidates(ctx context.Context, limit int) ([]uuid.UUID, error) {
	return p.store.ListCandidates(ctx, CandidateFilter{OnlyEnabled: true, Limit: limit})
}

// CountBackfillCandidates counts what the backfill still has to do.
func (p *Planner) CountBackfillCandidates(ctx context.Context) (int64, error) {
	return p.store.CountCandidates(ctx, nil, true)
}

// Process generates one item synchronously through the shared worker slot.
// See Queue.Process.
func (p *Planner) Process(ctx context.Context, itemID uuid.UUID, startBy time.Time) (Outcome, error) {
	return p.queue.Process(ctx, itemID, startBy)
}
