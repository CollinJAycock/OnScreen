package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeMetaSvc struct {
	mu             sync.Mutex
	listResult     []RefreshableLibrary
	listErr        error
	markedIDs      []uuid.UUID
	markErrFor     map[uuid.UUID]error
	listCallsCount atomic.Int64
}

func (f *fakeMetaSvc) ListLibrariesDueForMetadataRefresh(_ context.Context) ([]RefreshableLibrary, error) {
	f.listCallsCount.Add(1)
	return f.listResult, f.listErr
}

func (f *fakeMetaSvc) MarkLibraryMetadataRefreshed(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markedIDs = append(f.markedIDs, id)
	if e, ok := f.markErrFor[id]; ok {
		return e
	}
	return nil
}

type fakeEnricher struct {
	mu          sync.Mutex
	refreshed   []uuid.UUID
	refreshErrs map[uuid.UUID]error
}

func (e *fakeEnricher) RefreshLibrary(_ context.Context, id uuid.UUID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refreshed = append(e.refreshed, id)
	if e.refreshErrs != nil {
		if err, ok := e.refreshErrs[id]; ok {
			return err
		}
	}
	return nil
}

func TestMetadataRefresh_RefreshesAndMarksEachLibrary(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	svc := &fakeMetaSvc{listResult: []RefreshableLibrary{
		{ID: a, Name: "Movies"},
		{ID: b, Name: "Shows"},
	}}
	en := &fakeEnricher{}
	w := NewMetadataRefreshWorker(svc, en, time.Hour, newSilentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.tick(ctx) // call directly to avoid timing flakiness

	if len(en.refreshed) != 2 {
		t.Errorf("refreshed: got %d, want 2", len(en.refreshed))
	}
	if len(svc.markedIDs) != 2 {
		t.Errorf("marked: got %d, want 2", len(svc.markedIDs))
	}
}

func TestMetadataRefresh_SkipsMarkOnRefreshError(t *testing.T) {
	bad := uuid.New()
	good := uuid.New()
	en := &fakeEnricher{refreshErrs: map[uuid.UUID]error{bad: errors.New("agent down")}}
	svc := &fakeMetaSvc{listResult: []RefreshableLibrary{
		{ID: bad, Name: "Failing"},
		{ID: good, Name: "OK"},
	}}
	w := NewMetadataRefreshWorker(svc, en, time.Hour, newSilentLogger())
	w.tick(context.Background())

	// Both refreshed (we don't short-circuit on a single library failure).
	if len(en.refreshed) != 2 {
		t.Errorf("refreshed: got %d, want 2", len(en.refreshed))
	}
	// But only the good one should be marked.
	if len(svc.markedIDs) != 1 || svc.markedIDs[0] != good {
		t.Errorf("marked: got %v, want only %v", svc.markedIDs, good)
	}
}

func TestMetadataRefresh_ListErrorAbortsTick(t *testing.T) {
	svc := &fakeMetaSvc{listErr: errors.New("db gone")}
	en := &fakeEnricher{}
	w := NewMetadataRefreshWorker(svc, en, time.Hour, newSilentLogger())
	w.tick(context.Background())

	if len(en.refreshed) != 0 {
		t.Errorf("enricher called despite list error: %v", en.refreshed)
	}
}

func TestMetadataRefresh_RecoversFromMarkError(t *testing.T) {
	id := uuid.New()
	svc := &fakeMetaSvc{
		listResult: []RefreshableLibrary{{ID: id, Name: "L"}},
		markErrFor: map[uuid.UUID]error{id: errors.New("mark failed")},
	}
	en := &fakeEnricher{}
	w := NewMetadataRefreshWorker(svc, en, time.Hour, newSilentLogger())
	// Should NOT panic — error is logged then loop continues.
	w.tick(context.Background())

	if len(en.refreshed) != 1 {
		t.Errorf("refreshed: got %d, want 1", len(en.refreshed))
	}
}
