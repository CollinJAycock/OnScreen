//go:build integration

// Run with: go test -tags 'dev integration' -run WatchProgress ./internal/db/gen/
package gen_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestWatchProgress_Integration_RepeatMarkSkipsUnchangedRows pins the write
// amplification guard on MarkWatchStateForTarget: a repeated mark on a show
// must not rewrite episode rows that already carry that mark, while a row
// with watch activity newer than its mark is still re-marked (so "mark
// unwatched" resets an episode finished after the last mark) and episodes
// with no row yet are still inserted.
func TestWatchProgress_Integration_RepeatMarkSkipsUnchangedRows(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "wp-noop-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "wp-noop-lib-"+uuid.New().String()[:8])
	show := seedChild(ctx, t, q, lib, uuid.Nil, "show", "Noop Show", -1, "")
	s1 := seedChild(ctx, t, q, lib, show, "season", "Season 1", 1, "")
	e1 := seedChild(ctx, t, q, lib, s1, "episode", "E1", 1, "")
	e2 := seedChild(ctx, t, q, lib, s1, "episode", "E2", 2, "")
	seedChild(ctx, t, q, lib, s1, "episode", "E3", 3, "")

	markedAt := func(media uuid.UUID) time.Time {
		t.Helper()
		var at pgtype.Timestamptz
		if err := pool.QueryRow(ctx,
			"SELECT marked_at FROM watch_progress WHERE user_id = $1 AND media_id = $2",
			user, media).Scan(&at); err != nil {
			t.Fatalf("read marked_at: %v", err)
		}
		return at.Time
	}

	if n := mark(ctx, t, q, user, show, "show", "watched", nil); n != 3 {
		t.Fatalf("first show mark rows = %d, want 3", n)
	}
	first := markedAt(e1)
	time.Sleep(5 * time.Millisecond)
	if n := mark(ctx, t, q, user, show, "show", "watched", nil); n != 0 {
		t.Errorf("repeat show mark rows = %d, want 0 (nothing changed)", n)
	}
	if got := markedAt(e1); !got.Equal(first) {
		t.Errorf("repeat mark rewrote marked_at: %v → %v", first, got)
	}
	// The same guard holds for a single episode and for the season.
	if n := mark(ctx, t, q, user, e1, "episode", "watched", nil); n != 0 {
		t.Errorf("repeat episode mark rows = %d, want 0", n)
	}
	if n := mark(ctx, t, q, user, s1, "season", "watched", nil); n != 0 {
		t.Errorf("repeat season mark rows = %d, want 0", n)
	}

	// A real change still writes every row; repeating it is again a no-op.
	if n := mark(ctx, t, q, user, show, "show", "unwatched", nil); n != 3 {
		t.Fatalf("flip to unwatched rows = %d, want 3", n)
	}
	if n := mark(ctx, t, q, user, show, "show", "unwatched", nil); n != 0 {
		t.Errorf("repeat unwatched rows = %d, want 0", n)
	}

	// Watching E2 to the end after the unwatched mark makes it watched; the
	// same "mark unwatched" must still reset it — and only it.
	time.Sleep(5 * time.Millisecond)
	play(ctx, t, q, user, e2, "stop", 590_000, 600_000, dbNow(ctx, t, pool))
	if got := state(ctx, t, q, user, e2).Status; got != "watched" {
		t.Fatalf("E2 finished after the mark: %s, want watched", got)
	}
	time.Sleep(5 * time.Millisecond)
	if n := mark(ctx, t, q, user, show, "show", "unwatched", nil); n != 1 {
		t.Errorf("re-mark after activity rows = %d, want 1 (only E2)", n)
	}
	if got := state(ctx, t, q, user, e2).Status; got != "unwatched" {
		t.Errorf("E2 after re-marking unwatched: %s, want unwatched", got)
	}

	// An episode added later has no row yet: the repeat mark inserts it.
	seedChild(ctx, t, q, lib, s1, "episode", "E4", 4, "")
	if n := mark(ctx, t, q, user, show, "show", "unwatched", nil); n != 1 {
		t.Errorf("mark after a new episode rows = %d, want 1 (the new row)", n)
	}
}
