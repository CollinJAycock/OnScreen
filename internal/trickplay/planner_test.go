package trickplay

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// fakeCandidates is a CandidateStore that models the toggle + failed-row
// rules of the real query over an in-memory item table.
type fakeCandidates struct {
	enabled map[uuid.UUID]bool        // library → trickplay_enabled
	items   map[uuid.UUID][]uuid.UUID // library → items needing sprites
	failed  map[uuid.UUID][]uuid.UUID // library → items whose last run failed
	counts  LibraryCounts
	err     error
	last    CandidateFilter
}

func (f *fakeCandidates) ListCandidates(_ context.Context, cf CandidateFilter) ([]uuid.UUID, error) {
	f.last = cf
	if f.err != nil {
		return nil, f.err
	}
	var out []uuid.UUID
	for lib, items := range f.items {
		if cf.LibraryID != nil && *cf.LibraryID != lib {
			continue
		}
		if cf.OnlyEnabled && !f.enabled[lib] {
			continue
		}
		out = append(out, items...)
		if cf.IncludeFailed {
			out = append(out, f.failed[lib]...)
		}
	}
	if cf.Limit > 0 && len(out) > cf.Limit {
		out = out[:cf.Limit]
	}
	return out, nil
}

func (f *fakeCandidates) CountCandidates(_ context.Context, _ *uuid.UUID, _ bool) (int64, error) {
	return 42, nil
}

func (f *fakeCandidates) LibraryCounts(_ context.Context, _ uuid.UUID) (LibraryCounts, error) {
	return f.counts, f.err
}

func TestPlanner_AfterScanOnlyQueuesEnabledLibraries(t *testing.T) {
	on, off := uuid.New(), uuid.New()
	store := &fakeCandidates{
		enabled: map[uuid.UUID]bool{on: true, off: false},
		items:   map[uuid.UUID][]uuid.UUID{on: ids(3), off: ids(4)},
	}
	q := NewQueue(newQueueGen(), newQueueStore(), silentLogger())
	p := NewPlanner(q, store, silentLogger())

	n, err := p.AfterScan(context.Background(), off)
	if err != nil || n != 0 {
		t.Fatalf("AfterScan(disabled) = %d, %v; want 0, nil", n, err)
	}
	if !store.last.OnlyEnabled || store.last.LibraryID == nil || *store.last.LibraryID != off || store.last.IncludeFailed {
		t.Errorf("AfterScan filter = %+v; want this library, enabled-only, no failed retries", store.last)
	}
	n, err = p.AfterScan(context.Background(), on)
	if err != nil || n != 3 {
		t.Fatalf("AfterScan(enabled) = %d, %v; want 3", n, err)
	}
	// A second scan of the same library re-lists the same items (nothing
	// generated yet) but queues nothing new.
	if n, _ := p.AfterScan(context.Background(), on); n != 0 {
		t.Errorf("repeat AfterScan queued %d duplicates", n)
	}
	if q.Len() != 3 {
		t.Errorf("backlog = %d, want 3", q.Len())
	}
	if store.last.Limit != q.MaxPending() {
		t.Errorf("listing limit = %d, want the backlog cap %d", store.last.Limit, q.MaxPending())
	}
}

func TestPlanner_AfterScanPropagatesError(t *testing.T) {
	store := &fakeCandidates{err: errors.New("db down")}
	p := NewPlanner(NewQueue(newQueueGen(), newQueueStore(), silentLogger()), store, silentLogger())
	if _, err := p.AfterScan(context.Background(), uuid.New()); err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanner_QueueLibraryIgnoresToggleAndIsIdempotent(t *testing.T) {
	off := uuid.New()
	failed := ids(2)
	store := &fakeCandidates{
		enabled: map[uuid.UUID]bool{off: false},
		items:   map[uuid.UUID][]uuid.UUID{off: ids(3)},
		failed:  map[uuid.UUID][]uuid.UUID{off: failed},
	}
	q := NewQueue(newQueueGen(), newQueueStore(), silentLogger())
	p := NewPlanner(q, store, silentLogger())

	n, err := p.QueueLibrary(context.Background(), off, false)
	if err != nil || n != 3 {
		t.Fatalf("QueueLibrary = %d, %v; want 3", n, err)
	}
	if store.last.OnlyEnabled {
		t.Error("an explicit admin request must not be gated on the toggle")
	}
	if n, _ := p.QueueLibrary(context.Background(), off, false); n != 0 {
		t.Errorf("second QueueLibrary queued %d, want 0", n)
	}
	n, _ = p.QueueLibrary(context.Background(), off, true)
	if n != 2 || !q.Has(failed[0]) {
		t.Errorf("include_failed queued %d, want the 2 failed items", n)
	}
}

func TestPlanner_BackfillListingIsEnabledOnlyAcrossLibraries(t *testing.T) {
	store := &fakeCandidates{}
	p := NewPlanner(NewQueue(newQueueGen(), newQueueStore(), silentLogger()), store, silentLogger())
	if _, err := p.ListBackfillCandidates(context.Background(), 25); err != nil {
		t.Fatal(err)
	}
	if !store.last.OnlyEnabled || store.last.LibraryID != nil || store.last.IncludeFailed || store.last.Limit != 25 {
		t.Errorf("backfill filter = %+v", store.last)
	}
	if n, _ := p.CountBackfillCandidates(context.Background()); n != 42 {
		t.Errorf("CountBackfillCandidates = %d", n)
	}
}

func TestPlanner_QueueItemGoesToTheFront(t *testing.T) {
	q := NewQueue(newQueueGen(), newQueueStore(), silentLogger())
	p := NewPlanner(q, &fakeCandidates{}, silentLogger())
	a, b := uuid.New(), uuid.New()
	q.Enqueue(a)
	p.QueueItem(b)
	if id, _ := q.next(); id != b {
		t.Errorf("head of queue = %s, want the per-item request %s", id, b)
	}
}

func TestNewLibraryCounts_DerivesPending(t *testing.T) {
	c := NewLibraryCounts(10, 6, 1)
	if c.Pending != 3 || c.Total != 10 || c.Done != 6 || c.Failed != 1 {
		t.Errorf("counts = %+v", c)
	}
	if c := NewLibraryCounts(1, 2, 0); c.Pending != 0 {
		t.Errorf("pending must not go negative: %+v", c)
	}
}
