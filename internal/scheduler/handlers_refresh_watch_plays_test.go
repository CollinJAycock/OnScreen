package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// fakeRefresher records the REFRESH statement and answers the change probe
// with whatever fingerprint the test sets.
type fakeRefresher struct {
	gotSQL    string
	refreshes int
	err       error

	events   int64
	latest   time.Time
	probeErr error
}

func (f *fakeRefresher) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.gotSQL = sql
	f.refreshes++
	return pgconn.CommandTag{}, f.err
}

func (f *fakeRefresher) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (f *fakeRefresher) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if !strings.Contains(sql, "watch_events") {
		return fakeRow{err: errors.New("unexpected QueryRow: " + sql)}
	}
	return fakeRow{events: f.events, latest: f.latest, err: f.probeErr}
}

type fakeRow struct {
	events int64
	latest time.Time
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.events
	*dest[1].(*pgtype.Timestamptz) = pgtype.Timestamptz{Time: r.latest, Valid: true}
	return nil
}

func TestRefreshWatchPlaysHandler_RunsConcurrentRefresh(t *testing.T) {
	f := &fakeRefresher{}
	h := NewRefreshWatchPlaysHandler(f, nil)

	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out == "" {
		t.Error("expected a non-empty output summary")
	}
	if f.gotSQL != "REFRESH MATERIALIZED VIEW CONCURRENTLY public.watch_plays" {
		t.Errorf("unexpected SQL: %q", f.gotSQL)
	}
}

func TestRefreshWatchPlaysHandler_PropagatesError(t *testing.T) {
	f := &fakeRefresher{err: errors.New("boom")}
	h := NewRefreshWatchPlaysHandler(f, nil)

	if _, err := h.Run(context.Background(), nil); err == nil {
		t.Fatal("expected error to propagate")
	}
}

// An idle server must not rebuild the view every run; any new, imported or
// deleted terminal event must.
func TestRefreshWatchPlaysHandler_SkipsWhenSourceUnchanged(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	f := &fakeRefresher{events: 10, latest: at}
	h := NewRefreshWatchPlaysHandler(f, nil)

	run := func(wantRefreshes int) {
		t.Helper()
		if _, err := h.Run(ctx, nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if f.refreshes != wantRefreshes {
			t.Fatalf("refreshes = %d, want %d", f.refreshes, wantRefreshes)
		}
	}

	run(1) // first run after start always refreshes
	run(1) // nothing changed: skipped
	f.events, f.latest = 11, at.Add(time.Minute)
	run(2) // a new stop
	f.events = 12
	run(3) // an imported old play: count moves, newest doesn't
	f.events = 11
	run(4) // history deleted
	run(4)
}

// A failed refresh must not record the fingerprint, or the next run would
// skip a view that was never rebuilt.
func TestRefreshWatchPlaysHandler_RetriesAfterFailedRefresh(t *testing.T) {
	ctx := context.Background()
	f := &fakeRefresher{events: 5, latest: time.Unix(1_700_000_000, 0), err: errors.New("boom")}
	h := NewRefreshWatchPlaysHandler(f, nil)
	if _, err := h.Run(ctx, nil); err == nil {
		t.Fatal("expected error")
	}
	f.err = nil
	if _, err := h.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.refreshes != 2 {
		t.Fatalf("refreshes = %d, want 2 (failed run must not arm the skip)", f.refreshes)
	}
}

// A failing probe refreshes rather than freezing analytics.
func TestRefreshWatchPlaysHandler_ProbeErrorRefreshes(t *testing.T) {
	ctx := context.Background()
	f := &fakeRefresher{probeErr: errors.New("probe down")}
	h := NewRefreshWatchPlaysHandler(f, nil)
	for range 2 {
		if _, err := h.Run(ctx, nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	if f.refreshes != 2 {
		t.Fatalf("refreshes = %d, want 2", f.refreshes)
	}
}
