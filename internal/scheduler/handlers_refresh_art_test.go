package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

type stubMissingArtLister struct {
	items []media.Item
	err   error
	limit int32
}

func (s *stubMissingArtLister) ListItemsMissingArt(_ context.Context, limit int32) ([]media.Item, error) {
	s.limit = limit
	return s.items, s.err
}

type stubMissingArtEnricher struct {
	errForIDs map[uuid.UUID]error
	called    []uuid.UUID
}

func (s *stubMissingArtEnricher) EnrichItem(_ context.Context, id uuid.UUID) error {
	s.called = append(s.called, id)
	if e, ok := s.errForIDs[id]; ok {
		return e
	}
	return nil
}

func TestRefreshMissingArtHandler_NoItems(t *testing.T) {
	h := NewRefreshMissingArtHandler(&stubMissingArtLister{}, &stubMissingArtEnricher{}, slog.Default())
	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out, "no items missing art") {
		t.Errorf("output: got %q, want substring 'no items missing art'", out)
	}
}

func TestRefreshMissingArtHandler_DefaultLimit(t *testing.T) {
	lister := &stubMissingArtLister{}
	h := NewRefreshMissingArtHandler(lister, &stubMissingArtEnricher{}, slog.Default())
	if _, err := h.Run(context.Background(), nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if lister.limit != 200 {
		t.Errorf("default limit: got %d, want 200", lister.limit)
	}
}

func TestRefreshMissingArtHandler_ConfigLimitClampsTo1000(t *testing.T) {
	lister := &stubMissingArtLister{}
	h := NewRefreshMissingArtHandler(lister, &stubMissingArtEnricher{}, slog.Default())
	if _, err := h.Run(context.Background(), []byte(`{"limit":50000}`)); err != nil {
		t.Fatalf("err: %v", err)
	}
	if lister.limit != 1000 {
		t.Errorf("clamp: got %d, want 1000", lister.limit)
	}
}

func TestRefreshMissingArtHandler_CountsRefreshAndFailure(t *testing.T) {
	good1 := uuid.New()
	good2 := uuid.New()
	bad := uuid.New()
	lister := &stubMissingArtLister{items: []media.Item{
		{ID: good1, Title: "Good 1"},
		{ID: bad, Title: "Bad"},
		{ID: good2, Title: "Good 2"},
	}}
	enr := &stubMissingArtEnricher{
		errForIDs: map[uuid.UUID]error{bad: errors.New("tmdb down")},
	}
	h := NewRefreshMissingArtHandler(lister, enr, slog.Default())
	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out, "candidates=3") || !strings.Contains(out, "refreshed=2") || !strings.Contains(out, "failed=1") {
		t.Errorf("output: got %q, want candidates=3 refreshed=2 failed=1", out)
	}
	if len(enr.called) != 3 {
		t.Errorf("EnrichItem calls: got %d, want 3", len(enr.called))
	}
}

func TestRefreshMissingArtHandler_ListerErrorBubbles(t *testing.T) {
	lister := &stubMissingArtLister{err: errors.New("db down")}
	h := NewRefreshMissingArtHandler(lister, &stubMissingArtEnricher{}, slog.Default())
	if _, err := h.Run(context.Background(), nil); err == nil {
		t.Fatal("expected err when lister fails")
	}
}
