//go:build integration

// Round-trips against a real Postgres testcontainer for migration 00024
// (libraries.trickplay_enabled) and the automatic-trickplay queue queries in
// trickplay_queue.sql: candidate eligibility (toggle, file, duration,
// damaged, failed/stale-pending rows), the per-library progress counts, and
// the skipped / interrupted-reset bookkeeping writes.
//
// Run with: go test -tags 'dev integration' ./internal/db/gen/ -run Trickplay
package gen_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/testdb"
)

func columnExists(ctx context.Context, t *testing.T, pool *pgxpool.Pool, table, column string) bool {
	t.Helper()
	var ok bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2)`,
		table, column).Scan(&ok); err != nil {
		t.Fatalf("column lookup: %v", err)
	}
	return ok
}

func TestTrickplayMigration_Integration_BackfillsVideoLibraries(t *testing.T) {
	pool, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()

	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}

	// Roll back just 00024, seed one library of every type the way a
	// pre-migration install would have them, then migrate forward again.
	if err := goose.DownToContext(ctx, db, ".", 23); err != nil {
		t.Fatalf("down to 23: %v", err)
	}
	if columnExists(ctx, t, pool, "libraries", "trickplay_enabled") {
		t.Fatal("Down did not drop libraries.trickplay_enabled")
	}
	want := map[string]bool{
		"movie": true, "show": true, "home_video": true, "anime": true, "cartoons": true,
		"music": false, "photo": false, "dvr": false, "audiobook": false,
		"book": false, "podcast": false,
	}
	for typ := range want {
		if _, err := pool.Exec(ctx,
			`INSERT INTO libraries (name, type, scan_paths) VALUES ($1, $2, $3)`,
			"lib-"+typ, typ, []string{"/media/" + typ}); err != nil {
			t.Fatalf("seed %s library: %v", typ, err)
		}
	}
	if err := goose.UpToContext(ctx, db, ".", 24); err != nil {
		t.Fatalf("up to 24: %v", err)
	}

	rows, err := pool.Query(ctx, `SELECT type, trickplay_enabled FROM libraries`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for rows.Next() {
		var typ string
		var on bool
		if err := rows.Scan(&typ, &on); err != nil {
			t.Fatal(err)
		}
		got[typ] = on
	}
	rows.Close()
	for typ, on := range want {
		if got[typ] != on {
			t.Errorf("%s library trickplay_enabled = %v, want %v", typ, got[typ], on)
		}
	}

	// A row inserted without naming the column gets the conservative default.
	var def bool
	if err := pool.QueryRow(ctx,
		`INSERT INTO libraries (name, type, scan_paths) VALUES ('late', 'movie', '{/late}') RETURNING trickplay_enabled`,
	).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if def {
		t.Error("column default should be false; the create path opts video libraries in explicitly")
	}

	// Down/up round trip is clean.
	if err := goose.DownToContext(ctx, db, ".", 23); err != nil {
		t.Fatalf("second down: %v", err)
	}
	if columnExists(ctx, t, pool, "libraries", "trickplay_enabled") {
		t.Error("second Down left the column behind")
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	if !columnExists(ctx, t, pool, "libraries", "trickplay_enabled") {
		t.Error("re-up did not restore the column")
	}
}

// tpFixture builds libraries/items/files for the candidate tests.
type tpFixture struct {
	ctx  context.Context
	t    *testing.T
	q    *gen.Queries
	pool *pgxpool.Pool
	n    int
}

func (f *tpFixture) library(name string, enabled bool) uuid.UUID {
	f.t.Helper()
	lib, err := f.q.CreateLibrary(f.ctx, gen.CreateLibraryParams{
		Name: name, Type: "movie", ScanPaths: []string{"/media/" + name},
		Agent: "tmdb", Language: "en", ScanInterval: time.Hour,
		MetadataRefreshInterval: 24 * time.Hour, TrickplayEnabled: enabled,
	})
	if err != nil {
		f.t.Fatalf("CreateLibrary %s: %v", name, err)
	}
	if lib.TrickplayEnabled != enabled {
		f.t.Fatalf("CreateLibrary returned trickplay_enabled=%v, want %v", lib.TrickplayEnabled, enabled)
	}
	return lib.ID
}

// item inserts an item with a created_at that increases per call, so
// "oldest first" ordering is deterministic.
func (f *tpFixture) item(lib uuid.UUID, typ string) uuid.UUID {
	f.t.Helper()
	f.n++
	title := typ + "-" + uuid.NewString()[:6]
	it, err := f.q.CreateMediaItem(f.ctx, gen.CreateMediaItemParams{
		LibraryID: lib, Type: typ, Title: title, SortTitle: title,
	})
	if err != nil {
		f.t.Fatalf("CreateMediaItem: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE media_items SET created_at = $2 WHERE id = $1`,
		it.ID, time.Date(2026, 1, 1, 0, f.n, 0, 0, time.UTC)); err != nil {
		f.t.Fatal(err)
	}
	return it.ID
}

func (f *tpFixture) file(item uuid.UUID, videoCodec *string, durationMS *int64) uuid.UUID {
	f.t.Helper()
	mf, err := f.q.CreateMediaFile(f.ctx, gen.CreateMediaFileParams{
		MediaItemID: item, FilePath: "/media/" + uuid.NewString() + ".mkv", FileSize: 1,
		VideoCodec: videoCodec, DurationMs: durationMS,
	})
	if err != nil {
		f.t.Fatalf("CreateMediaFile: %v", err)
	}
	return mf.ID
}

func (f *tpFixture) goodFile(item uuid.UUID) uuid.UUID {
	codec, dur := "h264", int64(90_000)
	return f.file(item, &codec, &dur)
}

func (f *tpFixture) status(item, file uuid.UUID, status string) {
	f.t.Helper()
	if err := f.q.UpsertTrickplayPending(f.ctx, gen.UpsertTrickplayPendingParams{
		ItemID: item, FileID: pgtype.UUID{Bytes: file, Valid: true},
		IntervalSec: 10, ThumbWidth: 320, ThumbHeight: 180, GridCols: 10, GridRows: 10,
	}); err != nil {
		f.t.Fatal(err)
	}
	switch status {
	case "done":
		if err := f.q.MarkTrickplayDone(f.ctx, gen.MarkTrickplayDoneParams{ItemID: item, SpriteCount: 1}); err != nil {
			f.t.Fatal(err)
		}
	case "failed":
		reason := "ffmpeg exploded"
		if err := f.q.MarkTrickplayFailed(f.ctx, gen.MarkTrickplayFailedParams{ItemID: item, LastError: &reason}); err != nil {
			f.t.Fatal(err)
		}
	case "skipped":
		if err := f.q.MarkTrickplaySkipped(f.ctx, gen.MarkTrickplaySkippedParams{ItemID: item, Reason: "no file"}); err != nil {
			f.t.Fatal(err)
		}
	case "pending":
	default:
		f.t.Fatalf("unknown status %q", status)
	}
}

func (f *tpFixture) candidates(lib *uuid.UUID, onlyEnabled, includeFailed bool, limit int32) []uuid.UUID {
	f.t.Helper()
	p := gen.ListTrickplayCandidatesParams{OnlyEnabled: onlyEnabled, IncludeFailed: includeFailed, Lim: limit}
	if lib != nil {
		p.LibraryID = pgtype.UUID{Bytes: *lib, Valid: true}
	}
	ids, err := f.q.ListTrickplayCandidates(f.ctx, p)
	if err != nil {
		f.t.Fatalf("ListTrickplayCandidates: %v", err)
	}
	return ids
}

func TestTrickplayQueue_Integration_CandidatesCountsAndBookkeeping(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	f := &tpFixture{ctx: ctx, t: t, q: gen.New(pool), pool: pool}
	q := f.q

	on := f.library("on", true)
	off := f.library("off", false)

	// Eligible, no row → candidate.
	fresh := f.item(on, "movie")
	f.goodFile(fresh)
	// Eligible episode → candidate.
	episode := f.item(on, "episode")
	f.goodFile(episode)
	// Eligible, done → counted done, not a candidate.
	done := f.item(on, "movie")
	f.status(done, f.goodFile(done), "done")
	// Eligible, failed → counted failed; candidate only with include_failed.
	failed := f.item(on, "movie")
	f.status(failed, f.goodFile(failed), "failed")
	// Eligible, skipped → counted failed; never a candidate.
	skipped := f.item(on, "movie")
	f.status(skipped, f.goodFile(skipped), "skipped")
	// Eligible, pending and live → counted pending, not a candidate.
	running := f.item(on, "movie")
	f.status(running, f.goodFile(running), "pending")
	// Eligible, pending but orphaned (7h old) → candidate again.
	orphan := f.item(on, "movie")
	f.status(orphan, f.goodFile(orphan), "pending")
	if _, err := pool.Exec(ctx, `UPDATE trickplay_status SET last_attempted_at = NOW() - INTERVAL '7 hours' WHERE item_id = $1`, orphan); err != nil {
		t.Fatal(err)
	}
	// Ineligible: no file / audio-only / no duration / damaged / deleted /
	// wrong item type.
	f.item(on, "movie")
	codec := "h264"
	f.file(f.item(on, "movie"), nil, func() *int64 { d := int64(1000); return &d }())
	f.file(f.item(on, "movie"), &codec, nil)
	damaged := f.item(on, "movie")
	damagedFile := f.goodFile(damaged)
	detail := "green frames"
	if err := q.UpdateMediaFileIntegrity(ctx, gen.UpdateMediaFileIntegrityParams{ID: damagedFile, IntegrityStatus: "damaged", IntegrityDetail: &detail}); err != nil {
		t.Fatalf("UpdateMediaFileIntegrity: %v", err)
	}
	gone := f.item(on, "movie")
	f.goodFile(gone)
	if err := q.SoftDeleteMediaItem(ctx, gone); err != nil {
		t.Fatal(err)
	}
	f.goodFile(f.item(on, "show"))
	// The disabled library's eligible item.
	offItem := f.item(off, "movie")
	f.goodFile(offItem)

	// Automatic path: this library, toggle enforced, oldest first.
	got := f.candidates(&on, true, false, 100)
	if want := []uuid.UUID{fresh, episode, orphan}; !slices.Equal(got, want) {
		t.Errorf("candidates = %v, want %v (oldest first)", got, want)
	}
	if got := f.candidates(&on, true, false, 1); len(got) != 1 || got[0] != fresh {
		t.Errorf("limit 1 = %v, want [%s]", got, fresh)
	}
	if got := f.candidates(&on, true, true, 100); !slices.Contains(got, failed) || len(got) != 4 {
		t.Errorf("include_failed = %v, want the three plus %s", got, failed)
	}
	// Toggle off: nothing for the automatic path, everything for an explicit request.
	if got := f.candidates(&off, true, false, 100); len(got) != 0 {
		t.Errorf("disabled library listed %v for the automatic path", got)
	}
	if got := f.candidates(&off, false, false, 100); !slices.Equal(got, []uuid.UUID{offItem}) {
		t.Errorf("explicit request on disabled library = %v, want [%s]", got, offItem)
	}
	// Backfill: all enabled libraries.
	if got := f.candidates(nil, true, false, 100); len(got) != 3 || slices.Contains(got, offItem) {
		t.Errorf("backfill candidates = %v", got)
	}
	n, err := q.CountTrickplayCandidates(ctx, gen.CountTrickplayCandidatesParams{OnlyEnabled: true})
	if err != nil || n != 3 {
		t.Errorf("CountTrickplayCandidates(enabled) = %d, %v; want 3", n, err)
	}
	n, err = q.CountTrickplayCandidates(ctx, gen.CountTrickplayCandidatesParams{
		LibraryID: pgtype.UUID{Bytes: off, Valid: true},
	})
	if err != nil || n != 1 {
		t.Errorf("CountTrickplayCandidates(off, explicit) = %d, %v; want 1", n, err)
	}

	// Progress counts over the eligible set: fresh, episode, done, failed,
	// skipped, running, orphan.
	counts, err := q.GetLibraryTrickplayCounts(ctx, on)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Total != 7 || counts.Done != 1 || counts.Failed != 2 {
		t.Errorf("counts = %+v, want total=7 done=1 failed=2", counts)
	}

	// Bookkeeping writes.
	if err := q.MarkTrickplaySkipped(ctx, gen.MarkTrickplaySkippedParams{ItemID: fresh, Reason: "trickplay: item has no playable file"}); err != nil {
		t.Fatal(err)
	}
	row, err := q.GetTrickplayStatus(ctx, fresh)
	if err != nil || row.Status != "skipped" || row.LastError == nil || *row.LastError == "" {
		t.Errorf("after MarkTrickplaySkipped: %+v, %v", row, err)
	}
	if err := q.ResetInterruptedTrickplay(ctx, running); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetTrickplayStatus(ctx, running); err == nil {
		t.Error("ResetInterruptedTrickplay left the pending row")
	}
	if err := q.ResetInterruptedTrickplay(ctx, done); err != nil {
		t.Fatal(err)
	}
	if row, err := q.GetTrickplayStatus(ctx, done); err != nil || row.Status != "done" {
		t.Errorf("ResetInterruptedTrickplay must only touch pending rows: %+v, %v", row, err)
	}
	got = f.candidates(&on, true, false, 100)
	if want := []uuid.UUID{episode, running, orphan}; !slices.Equal(got, want) {
		t.Errorf("after bookkeeping candidates = %v, want %v", got, want)
	}

	// Soft-deleting the library hides everything in it.
	if err := q.SoftDeleteLibrary(ctx, on); err != nil {
		t.Fatal(err)
	}
	if got := f.candidates(nil, true, false, 100); len(got) != 0 {
		t.Errorf("deleted library still listed %v", got)
	}
}

func TestTrickplayLibraryToggle_Integration_UpdatePatchSemantics(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	f := &tpFixture{ctx: ctx, t: t, q: gen.New(pool), pool: pool}
	id := f.library("toggle", true)

	lib, err := f.q.GetLibrary(ctx, id)
	if err != nil || !lib.TrickplayEnabled {
		t.Fatalf("GetLibrary = %+v, %v", lib, err)
	}
	base := gen.UpdateLibraryParams{
		ID: id, Name: "renamed", ScanPaths: lib.ScanPaths, Agent: lib.Agent, Language: lib.Language,
		ScanInterval: lib.ScanInterval, MetadataRefreshInterval: lib.MetadataRefreshInterval,
	}
	// nil preserves.
	got, err := f.q.UpdateLibrary(ctx, base)
	if err != nil || !got.TrickplayEnabled {
		t.Errorf("rename-only update: %+v, %v", got.TrickplayEnabled, err)
	}
	off := false
	base.TrickplayEnabled = &off
	got, err = f.q.UpdateLibrary(ctx, base)
	if err != nil || got.TrickplayEnabled {
		t.Errorf("flip off: %+v, %v", got.TrickplayEnabled, err)
	}
	libs, err := f.q.ListLibraries(ctx)
	if err != nil || len(libs) != 1 || libs[0].TrickplayEnabled {
		t.Errorf("ListLibraries = %+v, %v", libs, err)
	}
}
