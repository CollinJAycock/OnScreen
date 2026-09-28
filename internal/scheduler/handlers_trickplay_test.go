package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/trickplay"
)

// fakeClock is a manually advanced clock for budget tests.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

type stubTrickplaySource struct {
	items     []uuid.UUID
	listErr   error
	limit     int
	remaining int64
	outcomes  map[uuid.UUID]trickplay.Outcome
	errs      map[uuid.UUID]error
	processed []uuid.UUID
	startBys  []time.Time
	// perItem advances the clock by this much per Process call.
	perItem time.Duration
	clock   *fakeClock
}

func (s *stubTrickplaySource) ListBackfillCandidates(_ context.Context, limit int) ([]uuid.UUID, error) {
	s.limit = limit
	return s.items, s.listErr
}

func (s *stubTrickplaySource) CountBackfillCandidates(context.Context) (int64, error) {
	return s.remaining, nil
}

func (s *stubTrickplaySource) Process(_ context.Context, id uuid.UUID, startBy time.Time) (trickplay.Outcome, error) {
	s.processed = append(s.processed, id)
	s.startBys = append(s.startBys, startBy)
	if s.clock != nil {
		s.clock.t = s.clock.t.Add(s.perItem)
	}
	if err := s.errs[id]; err != nil {
		return 0, err
	}
	if out, ok := s.outcomes[id]; ok {
		return out, nil
	}
	return trickplay.OutcomeDone, nil
}

func newBackfill(src *stubTrickplaySource, clock *fakeClock) *TrickplayBackfillHandler {
	h := NewTrickplayBackfillHandler(src, slog.Default())
	if clock != nil {
		h.now = clock.now
	}
	return h
}

func uuids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

func TestTrickplayBackfill_NoItems(t *testing.T) {
	h := newBackfill(&stubTrickplaySource{}, nil)
	out, err := h.Run(context.Background(), nil)
	if err != nil || !strings.Contains(out, "no items awaiting") {
		t.Fatalf("Run = %q, %v", out, err)
	}
}

func TestTrickplayBackfill_DefaultAndClampedLimit(t *testing.T) {
	src := &stubTrickplaySource{}
	h := newBackfill(src, nil)
	if _, err := h.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if src.limit != defaultTrickplayBackfillLimit {
		t.Errorf("default limit = %d", src.limit)
	}
	if _, err := h.Run(context.Background(), []byte(`{"limit":999999}`)); err != nil {
		t.Fatal(err)
	}
	if src.limit != maxTrickplayBackfillLimit {
		t.Errorf("clamped limit = %d", src.limit)
	}
	if _, err := h.Run(context.Background(), []byte(`{nope`)); err == nil {
		t.Error("bad config should error")
	}
}

func TestTrickplayBackfill_StopsStartingItemsAtBudget(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 9, 28, 3, 29, 0, 0, time.UTC)}
	start := clock.t
	// 20 minutes per item against the default 45-minute budget: items start
	// at +0, +20 and +40; the fourth would start at +60, past the budget.
	src := &stubTrickplaySource{items: uuids(10), perItem: 20 * time.Minute, clock: clock, remaining: 7}
	out, err := newBackfill(src, clock).Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(src.processed) != 3 {
		t.Errorf("processed %d items, want 3 within a 45-minute budget", len(src.processed))
	}
	wantDeadline := start.Add(45 * time.Minute)
	for i, sb := range src.startBys {
		if !sb.Equal(wantDeadline) {
			t.Errorf("item %d startBy = %v, want the budget deadline %v", i, sb, wantDeadline)
		}
	}
	for _, want := range []string{"generated=3", "remaining=7", "stopped=budget"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}

func TestTrickplayBackfill_ConfigBudget(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	src := &stubTrickplaySource{items: uuids(10), perItem: 20 * time.Minute, clock: clock}
	if _, err := newBackfill(src, clock).Run(context.Background(), []byte(`{"budget_minutes":90}`)); err != nil {
		t.Fatal(err)
	}
	if len(src.processed) != 5 { // +0 +20 +40 +60 +80
		t.Errorf("processed %d with a 90-minute budget, want 5", len(src.processed))
	}
}

func TestTrickplayBackfill_CountsOutcomes(t *testing.T) {
	items := uuids(5)
	src := &stubTrickplaySource{
		items: items,
		outcomes: map[uuid.UUID]trickplay.Outcome{
			items[1]: trickplay.OutcomeFailed,
			items[2]: trickplay.OutcomeSkipped,
			items[3]: trickplay.OutcomeDuplicate,
		},
		errs: map[uuid.UUID]error{items[4]: errors.New("db hiccup")},
	}
	out, err := newBackfill(src, nil).Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"generated=1", "failed=2", "skipped=1", "already_queued=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
	if strings.Contains(out, "stopped=") {
		t.Errorf("a run that finished its list should not report a stop: %q", out)
	}
}

func TestTrickplayBackfill_StopsWhenBusyPastBudget(t *testing.T) {
	items := uuids(4)
	src := &stubTrickplaySource{
		items: items,
		errs:  map[uuid.UUID]error{items[1]: trickplay.ErrNotStarted},
	}
	out, err := newBackfill(src, nil).Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(src.processed) != 2 {
		t.Errorf("processed %d, want to stop right after the item that couldn't start", len(src.processed))
	}
	if !strings.Contains(out, "stopped=busy") || !strings.Contains(out, "generated=1") {
		t.Errorf("output = %q", out)
	}
}

func TestTrickplayBackfill_StopsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	items := uuids(3)
	src := &stubTrickplaySource{items: items, errs: map[uuid.UUID]error{items[0]: context.Canceled}}
	// Cancel as the first item "runs".
	cancel()
	out, err := newBackfill(src, nil).Run(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(src.processed) != 0 || !strings.Contains(out, "stopped=shutdown") {
		t.Errorf("processed=%d out=%q; a cancelled run must not start items", len(src.processed), out)
	}
}

func TestTrickplayBackfill_ListErrorFails(t *testing.T) {
	src := &stubTrickplaySource{listErr: errors.New("db down")}
	if _, err := newBackfill(src, nil).Run(context.Background(), nil); err == nil {
		t.Fatal("expected error")
	}
}
