package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeChapterThumbSource struct {
	items    []uuid.UUID
	limit    int
	failOn   uuid.UUID
	thumbsOf int
	ran      []uuid.UUID
}

func (f *fakeChapterThumbSource) ChapterThumbCandidates(_ context.Context, limit int) ([]uuid.UUID, error) {
	f.limit = limit
	return f.items, nil
}

func (f *fakeChapterThumbSource) BackfillChapterThumbs(_ context.Context, id uuid.UUID) (int, error) {
	f.ran = append(f.ran, id)
	if id == f.failOn {
		return 0, errors.New("boom")
	}
	return f.thumbsOf, nil
}

func TestChapterThumbsHandler_Run(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	src := &fakeChapterThumbSource{items: []uuid.UUID{a, b, c}, failOn: b, thumbsOf: 12}
	msg, err := NewChapterThumbsHandler(src, nil).Run(context.Background(), json.RawMessage(`{"limit":50}`))
	if err != nil {
		t.Fatal(err)
	}
	if src.limit != 50 || len(src.ran) != 3 {
		t.Errorf("limit=%d ran=%d", src.limit, len(src.ran))
	}
	if msg != "items=2 thumbnails=24 failed=1" {
		t.Errorf("msg = %q", msg)
	}
}

func TestChapterThumbsHandler_EmptyAndBudget(t *testing.T) {
	msg, err := NewChapterThumbsHandler(&fakeChapterThumbSource{}, nil).Run(context.Background(), nil)
	if err != nil || msg != "no items awaiting chapter thumbnails" {
		t.Fatalf("empty: %q %v", msg, err)
	}

	src := &fakeChapterThumbSource{items: []uuid.UUID{uuid.New(), uuid.New()}}
	h := NewChapterThumbsHandler(src, nil)
	start := time.Now()
	calls := 0
	h.now = func() time.Time { // the budget runs out after the first item
		calls++
		if calls > 2 {
			return start.Add(time.Hour)
		}
		return start
	}
	msg, err = h.Run(context.Background(), json.RawMessage(`{"budget_minutes":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(src.ran) != 1 || !strings.HasSuffix(msg, "stopped=budget") {
		t.Errorf("ran %d, msg %q", len(src.ran), msg)
	}
}
