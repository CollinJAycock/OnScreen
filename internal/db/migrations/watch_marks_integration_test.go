//go:build integration

package migrations_test

import (
	"context"
	"strings"
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

// TestWatchMarks_NullDurationKeepsStored_Integration runs 00035: an event
// without a duration keeps the duration watch_progress already has instead of
// clearing it, and completes nothing even past 90% of that duration, while an
// event carrying its own duration still completes the item. The Down block
// must restore 00023's trigger function exactly.
//
// Run with: go test -tags 'dev integration' -run WatchMarks ./internal/db/migrations/
func TestWatchMarks_NullDurationKeepsStored_Integration(t *testing.T) {
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

	fnSrc := func() string {
		t.Helper()
		var src string
		if err := pool.QueryRow(ctx,
			`SELECT prosrc FROM pg_proc WHERE proname = 'watch_progress_apply_event'`).Scan(&src); err != nil {
			t.Fatalf("read trigger function: %v", err)
		}
		// The body keeps the checkout's line endings; compare the text.
		return strings.ReplaceAll(src, "\r", "")
	}
	// 00023's function as 00023 itself creates it, to hold the Down block to.
	if err := goose.DownToContext(ctx, db, ".", 22); err != nil {
		t.Fatalf("down to 22: %v", err)
	}
	if err := goose.UpToContext(ctx, db, ".", 34); err != nil {
		t.Fatalf("up to 34: %v", err)
	}
	original := fnSrc()
	if err := goose.UpToContext(ctx, db, ".", 35); err != nil {
		t.Fatalf("up to 35: %v", err)
	}
	if fnSrc() == original {
		t.Fatal("00035 left the trigger function unchanged")
	}

	var user, lib, movie string
	if err := pool.QueryRow(ctx, `INSERT INTO users (username) VALUES ('wm-duration') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('wm', 'movie', '{/tmp/wm}') RETURNING id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_items (library_id, type, title, sort_title) VALUES ($1, 'movie', 'M', 'M') RETURNING id`, lib).Scan(&movie); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-3 * time.Hour)
	event := func(typ string, pos int64, dur *int64, at time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO watch_events (user_id, media_id, event_type, position_ms, duration_ms, client_name, occurred_at)
			VALUES ($1, $2, $3, $4, $5, 'Phone', $6)`, user, movie, typ, pos, dur, at); err != nil {
			t.Fatalf("insert %s event: %v", typ, err)
		}
	}
	type progress struct {
		pos       int64
		dur       *int64
		completed *time.Time
		status    string
	}
	read := func() progress {
		t.Helper()
		var p progress
		if err := pool.QueryRow(ctx, `
			SELECT wp.position_ms, wp.duration_ms, wp.last_completed_at, ws.status
			FROM watch_progress wp JOIN user_watch_state ws USING (user_id, media_id)
			WHERE wp.user_id = $1 AND wp.media_id = $2`, user, movie).
			Scan(&p.pos, &p.dur, &p.completed, &p.status); err != nil {
			t.Fatalf("read progress: %v", err)
		}
		return p
	}
	full := int64(600_000)

	event("play", 60_000, &full, base)

	// No duration, 95% of the stored one: the position moves, the duration
	// stays, and nothing completes.
	event("play", 570_000, nil, base.Add(time.Minute))
	if p := read(); p.pos != 570_000 || p.dur == nil || *p.dur != full || p.completed != nil || p.status != "in_progress" {
		t.Errorf("NULL-duration event: pos=%d dur=%v completed=%v status=%s, want 570000 / 600000 / nil / in_progress",
			p.pos, p.dur, p.completed, p.status)
	}

	// Its own duration, past 90%: completes as before.
	event("stop", 560_000, &full, base.Add(2*time.Minute))
	if p := read(); p.dur == nil || *p.dur != full || p.completed == nil || p.status != "watched" {
		t.Errorf("event with a duration: dur=%v completed=%v status=%s, want 600000 / set / watched", p.dur, p.completed, p.status)
	}

	// Down restores 00023's function: a NULL-duration event clears it again.
	if err := goose.DownToContext(ctx, db, ".", 34); err != nil {
		t.Fatalf("down to 34: %v", err)
	}
	if got := fnSrc(); got != original {
		t.Errorf("Down did not restore 00023's function:\n got: %s\nwant: %s", got, original)
	}
	event("play", 30_000, nil, base.Add(3*time.Minute))
	if p := read(); p.dur != nil {
		t.Errorf("after Down: duration=%d, want NULL (00023 behaviour)", *p.dur)
	}

	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("back up to head: %v", err)
	}
}
