//go:build integration

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestWatchMarks_BackfillAndDown_Integration runs 00023 against history that
// predates it: the watch_progress rollup must be backfilled from watch_events
// (latest position + last completion), the trigger must keep it current
// afterwards, and the Down block must restore the watch_state matview it
// replaced, populated.
//
// Run with: go test -tags 'dev integration' -run WatchMarks ./internal/db/migrations/
func TestWatchMarks_BackfillAndDown_Integration(t *testing.T) {
	pool, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()

	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}

	if err := goose.DownToContext(ctx, db, ".", 22); err != nil {
		t.Fatalf("down to 22: %v", err)
	}

	var user, lib, movie string
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (username) VALUES ('wm-backfill') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('wm', 'movie', '{/tmp/wm}') RETURNING id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_items (library_id, type, title, sort_title) VALUES ($1, 'movie', 'M', 'M') RETURNING id`, lib).Scan(&movie); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-3 * time.Hour)
	ins := `INSERT INTO watch_events (user_id, media_id, event_type, position_ms, duration_ms, client_name, occurred_at)
	        VALUES ($1, $2, $3, $4, 600000, $5, $6)`
	must(ins, user, movie, "stop", 590_000, "TV", base)                  // finished
	must(ins, user, movie, "play", 60_000, "Phone", base.Add(time.Hour)) // rewatch started

	if err := goose.UpToContext(ctx, db, ".", 23); err != nil {
		t.Fatalf("up to 23: %v", err)
	}

	var status, client string
	var pos int64
	var resumable bool
	var completed *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT ws.status, ws.position_ms, ws.resumable, ws.last_client_name, wp.last_completed_at
		FROM user_watch_state ws JOIN watch_progress wp USING (user_id, media_id)
		WHERE ws.user_id = $1 AND ws.media_id = $2`, user, movie).
		Scan(&status, &pos, &resumable, &client, &completed); err != nil {
		t.Fatalf("read backfilled state: %v", err)
	}
	if status != "watched" || pos != 60_000 || !resumable || client != "Phone" || completed == nil {
		t.Errorf("backfill: status=%s pos=%d resumable=%v client=%s completed=%v", status, pos, resumable, client, completed)
	}

	// The trigger is live after the migration.
	must(ins, user, movie, "play", 120_000, "Phone", base.Add(2*time.Hour))
	if err := pool.QueryRow(ctx, `SELECT position_ms FROM watch_progress WHERE user_id = $1 AND media_id = $2`, user, movie).Scan(&pos); err != nil || pos != 120_000 {
		t.Errorf("trigger: position=%d err=%v, want 120000", pos, err)
	}

	// Down restores the matview, populated from the same history.
	if err := goose.DownToContext(ctx, db, ".", 22); err != nil {
		t.Fatalf("down to 22 after up: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM watch_state WHERE user_id = $1`, user).Scan(&n); err != nil || n != 1 {
		t.Errorf("restored watch_state rows=%d err=%v, want 1", n, err)
	}
	var exists bool
	_ = pool.QueryRow(ctx, `SELECT to_regclass('public.watch_progress') IS NOT NULL`).Scan(&exists)
	if exists {
		t.Error("watch_progress should be gone after Down")
	}
	// Inserts still work without the trigger function.
	must(ins, user, movie, "stop", 1_000, "Phone", base.Add(150*time.Minute))

	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("back up to head: %v", err)
	}
}
