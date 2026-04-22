package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var _ LibraryLister = LibraryListerFunc(nil)

type fakeEnqueuer struct {
	enqueued []uuid.UUID
	err      error
}

func (f *fakeEnqueuer) EnqueueScan(_ context.Context, id uuid.UUID) error {
	if f.err != nil {
		return f.err
	}
	f.enqueued = append(f.enqueued, id)
	return nil
}

func libIDs(ids ...uuid.UUID) LibraryListerFunc {
	return func(context.Context) ([]uuid.UUID, error) { return ids, nil }
}

func TestScanHandlerAllLibraries(t *testing.T) {
	id1, id2 := uuid.New(), uuid.New()
	enq := &fakeEnqueuer{}
	h := NewScanHandler(enq, libIDs(id1, id2))

	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "2 libraries") {
		t.Fatalf("output: %q", out)
	}
	if len(enq.enqueued) != 2 || enq.enqueued[0] != id1 || enq.enqueued[1] != id2 {
		t.Fatalf("enqueued: %v", enq.enqueued)
	}
}

func TestScanHandlerExplicitAll(t *testing.T) {
	id := uuid.New()
	enq := &fakeEnqueuer{}
	h := NewScanHandler(enq, libIDs(id))
	if _, err := h.Run(context.Background(), json.RawMessage(`{"library_id":"all"}`)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(enq.enqueued) != 1 {
		t.Fatalf("expected 1 enqueued, got %d", len(enq.enqueued))
	}
}

func TestScanHandlerSpecificLibrary(t *testing.T) {
	id := uuid.New()
	enq := &fakeEnqueuer{}
	h := NewScanHandler(enq, libIDs())
	cfg, _ := json.Marshal(ScanConfig{LibraryID: id.String()})
	out, err := h.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(enq.enqueued) != 1 || enq.enqueued[0] != id {
		t.Fatalf("enqueued: %v", enq.enqueued)
	}
	if !strings.Contains(out, id.String()) {
		t.Fatalf("output: %q", out)
	}
}

func TestScanHandlerInvalidLibraryID(t *testing.T) {
	h := NewScanHandler(&fakeEnqueuer{}, libIDs())
	if _, err := h.Run(context.Background(), json.RawMessage(`{"library_id":"not-a-uuid"}`)); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestScanHandlerEnqueueErrorIncludesPartialCount(t *testing.T) {
	enq := &fakeEnqueuer{err: errors.New("queue full")}
	h := NewScanHandler(enq, libIDs(uuid.New(), uuid.New()))
	out, err := h.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from enqueue")
	}
	if !strings.Contains(out, "0 of 2") {
		t.Fatalf("expected partial count in output, got %q", out)
	}
}

func TestScanHandlerBadJSON(t *testing.T) {
	h := NewScanHandler(&fakeEnqueuer{}, libIDs())
	if _, err := h.Run(context.Background(), json.RawMessage(`{not json`)); err == nil {
		t.Fatal("expected JSON error")
	}
}

func TestScanHandlerListerError(t *testing.T) {
	lister := LibraryListerFunc(func(context.Context) ([]uuid.UUID, error) {
		return nil, errors.New("lister down")
	})
	enq := &fakeEnqueuer{}
	h := NewScanHandler(enq, lister)
	if _, err := h.Run(context.Background(), nil); err == nil {
		t.Fatal("expected error from lister")
	}
	if len(enq.enqueued) != 0 {
		t.Fatalf("no enqueue expected on lister error, got %d", len(enq.enqueued))
	}
}

func TestScanHandlerEmptyConfigTreatedAsAll(t *testing.T) {
	id := uuid.New()
	enq := &fakeEnqueuer{}
	h := NewScanHandler(enq, libIDs(id))
	// Empty JSON object (not just nil) should also be "all".
	if _, err := h.Run(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(enq.enqueued) != 1 {
		t.Fatalf("expected 1 enqueued, got %d", len(enq.enqueued))
	}
}

func TestScanHandlerAllLibrariesEmpty(t *testing.T) {
	enq := &fakeEnqueuer{}
	h := NewScanHandler(enq, libIDs())
	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "0 libraries") {
		t.Fatalf("output: %q", out)
	}
}

func TestLibraryListerFuncAdapter(t *testing.T) {
	want := []uuid.UUID{uuid.New(), uuid.New()}
	var f LibraryLister = LibraryListerFunc(func(context.Context) ([]uuid.UUID, error) {
		return want, nil
	})
	got, err := f.ListLibraryIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}
