//go:build integration

// Round-trips the watch_events queries — the highest-traffic write path
// in normal operation. Bugs here cause silent progress loss (the user
// hits resume and starts at 0:00) or duplicated history rows.
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// insertWatch is a test-only convenience for the typical event shape.
func insertWatch(t *testing.T, q *gen.Queries, userID, mediaID uuid.UUID, eventType string, positionMS int64, occurredAt time.Time) {
	t.Helper()
	_, err := q.InsertWatchEvent(context.Background(), gen.InsertWatchEventParams{
		UserID:     userID,
		MediaID:    mediaID,
		EventType:  eventType,
		PositionMs: positionMS,
		OccurredAt: pgtype.Timestamptz{Time: occurredAt, Valid: true},
	})
	if err != nil {
		t.Fatalf("InsertWatchEvent (%s): %v", eventType, err)
	}
}

// TestWatchEvents_Integration_StateMaterializesAfterRefresh proves the
// watch_state materialized view picks up the latest event after a
// REFRESH and that subsequent reads see the new position. This is the
// loop the resume button depends on.
func TestWatchEvents_Integration_StateMaterializesAfterRefresh(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "we-state-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "we-lib-"+uuid.New().String()[:8])
	item := seedMediaItem(ctx, t, q, lib, "Movie A")

	// Insert a play event at 12345 ms.
	insertWatch(t, q, user, item, "play", 12345, time.Now())

	// Refresh the materialized view so watch_state reflects the event.
	if err := q.RefreshWatchState(ctx); err != nil {
		t.Fatalf("RefreshWatchState: %v", err)
	}

	state, err := q.GetWatchState(ctx, gen.GetWatchStateParams{UserID: user, MediaID: item})
	if err != nil {
		t.Fatalf("GetWatchState: %v", err)
	}
	if state.PositionMs != 12345 {
		t.Errorf("position_ms = %d, want 12345", state.PositionMs)
	}
}

// TestWatchEvents_Integration_GetMissingStateReturnsErrNoRows proves
// the resume-position path's "no row" branch is reachable. The handler
// translates ErrNoRows to "fresh start at 0:00" rather than 500.
func TestWatchEvents_Integration_GetMissingStateReturnsErrNoRows(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	_, err := q.GetWatchState(ctx, gen.GetWatchStateParams{
		UserID:  uuid.New(), // never seeded
		MediaID: uuid.New(),
	})
	if err == nil {
		t.Fatal("expected ErrNoRows for missing state, got nil")
	}
	if err != pgx.ErrNoRows {
		t.Errorf("got %v, want pgx.ErrNoRows", err)
	}
}

// TestWatchEvents_Integration_LatestEventWins proves a series of events
// for the same item resolves to the latest position after refresh —
// load-bearing for "I scrubbed forward then closed the tab; resume
// should pick up where I scrubbed, not where I started."
func TestWatchEvents_Integration_LatestEventWins(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "we-latest-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "we-latest-lib-"+uuid.New().String()[:8])
	item := seedMediaItem(ctx, t, q, lib, "Movie B")

	now := time.Now()
	insertWatch(t, q, user, item, "play", 1000, now.Add(-3*time.Minute))
	insertWatch(t, q, user, item, "scrobble", 30000, now.Add(-2*time.Minute))
	insertWatch(t, q, user, item, "scrobble", 60000, now.Add(-1*time.Minute))
	insertWatch(t, q, user, item, "stop", 90000, now)

	if err := q.RefreshWatchState(ctx); err != nil {
		t.Fatalf("RefreshWatchState: %v", err)
	}

	state, err := q.GetWatchState(ctx, gen.GetWatchStateParams{UserID: user, MediaID: item})
	if err != nil {
		t.Fatalf("GetWatchState: %v", err)
	}
	if state.PositionMs != 90000 {
		t.Errorf("position_ms = %d, want 90000 (latest event wins)", state.PositionMs)
	}
}

// TestWatchEvents_Integration_HistoryCollapsesDuplicates proves the
// history query's window-function dedup: a stop event followed by a
// scrobble within 30 minutes for the same item returns one row, not two.
// Without this, the user's history page shows duplicate cards every
// time the player emits both an explicit stop and an onDestroy stop.
func TestWatchEvents_Integration_HistoryCollapsesDuplicates(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "we-hist-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "we-hist-lib-"+uuid.New().String()[:8])
	item := seedMediaItem(ctx, t, q, lib, "Movie C")

	base := time.Now()
	// Two stop-class events 5 minutes apart — should collapse into one.
	insertWatch(t, q, user, item, "stop", 50000, base.Add(-5*time.Minute))
	insertWatch(t, q, user, item, "scrobble", 60000, base)

	rows, err := q.ListWatchHistory(ctx, gen.ListWatchHistoryParams{
		UserID: user, Limit: 10, Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListWatchHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("history: got %d rows, want 1 (consecutive events should collapse)", len(rows))
	}
}

// TestWatchEvents_Integration_HistorySplitsAfter30MinGap proves the
// inverse: events more than 30 minutes apart are kept as separate rows.
// "I watched at 10am, watched again at 5pm" should show two history
// entries, not one collapsed mega-session.
func TestWatchEvents_Integration_HistorySplitsAfter30MinGap(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "we-split-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "we-split-lib-"+uuid.New().String()[:8])
	item := seedMediaItem(ctx, t, q, lib, "Movie D")

	base := time.Now()
	insertWatch(t, q, user, item, "stop", 1000, base.Add(-7*time.Hour))
	insertWatch(t, q, user, item, "stop", 90000, base) // 7h later — distinct session

	rows, err := q.ListWatchHistory(ctx, gen.ListWatchHistoryParams{
		UserID: user, Limit: 10, Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListWatchHistory: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("history: got %d rows, want 2 (>30min gap should NOT collapse)", len(rows))
	}
}

// TestWatchEvents_Integration_PartitionRoutingByMonth proves a watch
// event with an occurred_at in a future month routes to the correct
// month's partition (the partition function is set to auto-create
// missing partitions; if it's broken inserts will fail). This is the
// regression guard for the partition-creation worker.
func TestWatchEvents_Integration_PartitionRoutingByMonth(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "we-part-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "we-part-lib-"+uuid.New().String()[:8])
	item := seedMediaItem(ctx, t, q, lib, "Movie E")

	// Insert at the start of NEXT month — exercises the partition
	// auto-creation path. If the worker / migration didn't create the
	// partition, this insert fails with "no partition of relation".
	now := time.Now().UTC()
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)

	insertWatch(t, q, user, item, "play", 1, nextMonth)
}

// TestWatchEvents_Integration_ListStateMultipleItems proves the
// ListWatchStateForUser query returns one row per (user, media)
// combination, sorted by recency. The hub's "Continue Watching" rail
// reads this in lieu of the materialized view directly.
func TestWatchEvents_Integration_ListStateMultipleItems(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "we-lst-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "we-lst-lib-"+uuid.New().String()[:8])
	itemA := seedMediaItem(ctx, t, q, lib, "Movie A")
	itemB := seedMediaItem(ctx, t, q, lib, "Movie B")

	now := time.Now()
	insertWatch(t, q, user, itemA, "stop", 1000, now.Add(-5*time.Minute))
	insertWatch(t, q, user, itemB, "stop", 2000, now)

	if err := q.RefreshWatchState(ctx); err != nil {
		t.Fatalf("RefreshWatchState: %v", err)
	}

	rows, err := q.ListWatchStateForUser(ctx, user)
	if err != nil {
		t.Fatalf("ListWatchStateForUser: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (one per media item)", len(rows))
	}
	// Sorted by last_watched_at DESC — Movie B (more recent) first.
	if rows[0].MediaID != itemB {
		t.Errorf("first row mediaID = %s, want %s (most recent)", rows[0].MediaID, itemB)
	}
}
