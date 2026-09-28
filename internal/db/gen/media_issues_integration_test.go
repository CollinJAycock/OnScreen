//go:build integration

// Round-trips against a real Postgres testcontainer for migration 00026
// (media_issues) and the queries in media_issues.sql: the constraints and the
// open-report unique index, the per-user/per-item reads, the admin queue's
// joins and filters, closing a report, the FK cascades, the damaged-file list
// the Library health page reads, and the migration's Down/Up.
//
// Run with: go test -tags 'dev integration' ./internal/db/gen/ -run MediaIssues
package gen_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func issueNote(s string) *string { return &s }

func TestMediaIssues_Integration_ConstraintsAndUserReads(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	sfx := uuid.New().String()[:8]

	u := seedUser(ctx, t, q, "iss-"+sfx)
	other := seedUser(ctx, t, q, "iss-other-"+sfx)
	lib := seedLibrary(ctx, t, q, "iss-"+sfx)
	movie := seedMediaItem(ctx, t, q, lib, "Heat")
	file := seedMediaFile(ctx, t, q, movie, "/m/"+sfx+"/Heat.mkv")

	iss, err := q.CreateMediaIssue(ctx, gen.CreateMediaIssueParams{
		ItemID: movie, FileID: pgtype.UUID{Bytes: file, Valid: true}, UserID: u, Kind: "video", Note: issueNote("green"),
	})
	if err != nil {
		t.Fatalf("CreateMediaIssue: %v", err)
	}
	if iss.Status != "open" || !iss.CreatedAt.Valid || iss.ResolvedAt.Valid || iss.ID == uuid.Nil {
		t.Errorf("new issue = %+v", iss)
	}

	// One open report per (user, item, kind).
	_, err = q.CreateMediaIssue(ctx, gen.CreateMediaIssueParams{ItemID: movie, UserID: u, Kind: "video"})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("duplicate open report: err = %v, want 23505", err)
	}
	// Another kind, or another user, is fine.
	if _, err := q.CreateMediaIssue(ctx, gen.CreateMediaIssueParams{ItemID: movie, UserID: u, Kind: "audio"}); err != nil {
		t.Errorf("other kind: %v", err)
	}
	if _, err := q.CreateMediaIssue(ctx, gen.CreateMediaIssueParams{ItemID: movie, UserID: other, Kind: "video"}); err != nil {
		t.Errorf("other user: %v", err)
	}

	// CHECKs.
	for name, p := range map[string]gen.CreateMediaIssueParams{
		"kind":      {ItemID: movie, UserID: u, Kind: "smell"},
		"note 1001": {ItemID: movie, UserID: u, Kind: "other", Note: issueNote(strings.Repeat("é", 1001))},
	} {
		if _, err := q.CreateMediaIssue(ctx, p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := q.CreateMediaIssue(ctx, gen.CreateMediaIssueParams{
		ItemID: movie, UserID: u, Kind: "other", Note: issueNote(strings.Repeat("é", 1000)),
	}); err != nil {
		t.Errorf("1000-char note refused: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_issues SET status = 'bogus' WHERE id = $1`, iss.ID); err == nil {
		t.Error("bogus status accepted")
	}
	if _, err := pool.Exec(ctx, `UPDATE media_issues SET status = 'resolved' WHERE id = $1`, iss.ID); err == nil {
		t.Error("closed without resolved_at accepted")
	}

	n, err := q.CountOpenMediaIssuesForUser(ctx, u)
	if err != nil || n != 3 {
		t.Errorf("open count = %d (%v), want 3", n, err)
	}
	got, err := q.GetOpenMediaIssueForUserItemKind(ctx, gen.GetOpenMediaIssueForUserItemKindParams{UserID: u, ItemID: movie, Kind: "video"})
	if err != nil || got.ID != iss.ID {
		t.Errorf("open lookup = %v (%v)", got.ID, err)
	}
	if _, err := q.GetOpenMediaIssueForUserItemKind(ctx, gen.GetOpenMediaIssueForUserItemKindParams{UserID: u, ItemID: movie, Kind: "subtitles"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("absent kind: err = %v, want ErrNoRows", err)
	}
	mine, err := q.ListMediaIssuesForUserItem(ctx, gen.ListMediaIssuesForUserItemParams{UserID: u, ItemID: movie})
	if err != nil || len(mine) != 3 {
		t.Fatalf("mine = %d (%v), want 3", len(mine), err)
	}
	for i := 1; i < len(mine); i++ {
		if mine[i].CreatedAt.Time.After(mine[i-1].CreatedAt.Time) {
			t.Error("ListMediaIssuesForUserItem not newest first")
		}
	}

	// Close: sets the resolution fields; a second close finds no open row.
	admin := seedUser(ctx, t, q, "iss-admin-"+sfx)
	closed, err := q.CloseMediaIssue(ctx, gen.CloseMediaIssueParams{
		ID: iss.ID, Status: "resolved", ResolvedBy: pgtype.UUID{Bytes: admin, Valid: true}, ResolutionNote: issueNote("replaced"),
	})
	if err != nil {
		t.Fatalf("CloseMediaIssue: %v", err)
	}
	if closed.Status != "resolved" || !closed.ResolvedAt.Valid || uuid.UUID(closed.ResolvedBy.Bytes) != admin ||
		closed.ResolutionNote == nil || *closed.ResolutionNote != "replaced" {
		t.Errorf("closed = %+v", closed)
	}
	if _, err := q.CloseMediaIssue(ctx, gen.CloseMediaIssueParams{ID: iss.ID, Status: "dismissed"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("re-close: err = %v, want ErrNoRows", err)
	}
	// Closing frees the (user, item, kind) slot for a new report.
	if _, err := q.CreateMediaIssue(ctx, gen.CreateMediaIssueParams{ItemID: movie, UserID: u, Kind: "video"}); err != nil {
		t.Errorf("new report after close: %v", err)
	}

	// FK behaviour: deleting the file nulls file_id; deleting the resolving
	// admin nulls resolved_by; deleting the item removes its reports.
	if _, err := pool.Exec(ctx, `DELETE FROM media_files WHERE id = $1`, file); err != nil {
		t.Fatalf("delete file: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, admin); err != nil {
		t.Fatalf("delete admin: %v", err)
	}
	after, err := q.GetMediaIssue(ctx, iss.ID)
	if err != nil || after.FileID.Valid || after.ResolvedBy.Valid || after.Status != "resolved" {
		t.Errorf("after file/admin delete = %+v (%v)", after, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM media_items WHERE id = $1`, movie); err != nil {
		t.Fatalf("delete item: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_issues WHERE item_id = $1`, movie).Scan(&left); err != nil || left != 0 {
		t.Errorf("reports left after item delete = %d (%v)", left, err)
	}
}

func TestMediaIssues_Integration_AdminQueue(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	sfx := uuid.New().String()[:8]

	u := seedUser(ctx, t, q, "reporter-"+sfx)
	lib := seedLibrary(ctx, t, q, "queue-"+sfx)
	show := seedChild(ctx, t, q, lib, uuid.Nil, "show", "Andor", -1, "")
	if _, err := pool.Exec(ctx, `UPDATE media_items SET poster_path = 'andor/poster.jpg' WHERE id = $1`, show); err != nil {
		t.Fatal(err)
	}
	season := seedChild(ctx, t, q, lib, show, "season", "Season 2", 2, "")
	ep := seedChild(ctx, t, q, lib, season, "episode", "Harvest", 5, "")
	epFile := seedMediaFile(ctx, t, q, ep, `/tv/Andor/Season 02/Andor.S02E05.mkv`)
	movie := seedMediaItem(ctx, t, q, lib, "Heat")
	gone := seedMediaItem(ctx, t, q, lib, "Gone")

	epIssue, err := q.CreateMediaIssue(ctx, gen.CreateMediaIssueParams{
		ItemID: ep, FileID: pgtype.UUID{Bytes: epFile, Valid: true}, UserID: u, Kind: "audio",
	})
	if err != nil {
		t.Fatal(err)
	}
	movieIssue, err := q.CreateMediaIssue(ctx, gen.CreateMediaIssueParams{ItemID: movie, UserID: u, Kind: "wrong_match"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateMediaIssue(ctx, gen.CreateMediaIssueParams{ItemID: gone, UserID: u, Kind: "other"}); err != nil {
		t.Fatal(err)
	}
	// A soft-deleted item's reports leave the queue.
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = NOW() WHERE id = $1`, gone); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CloseMediaIssue(ctx, gen.CloseMediaIssueParams{ID: movieIssue.ID, Status: "dismissed"}); err != nil {
		t.Fatal(err)
	}

	open := "open"
	rows, err := q.ListMediaIssuesAdmin(ctx, gen.ListMediaIssuesAdminParams{Status: &open, Lim: 50})
	if err != nil {
		t.Fatalf("ListMediaIssuesAdmin: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != epIssue.ID {
		t.Fatalf("open queue = %+v, want just the episode report", rows)
	}
	r := rows[0]
	if r.ItemType != "episode" || r.ItemTitle != "Harvest" || r.ItemIndex == nil || *r.ItemIndex != 5 ||
		r.ParentIndex == nil || *r.ParentIndex != 2 || r.GrandparentTitle == nil || *r.GrandparentTitle != "Andor" ||
		r.PosterPath == nil || *r.PosterPath != "andor/poster.jpg" || r.ReporterUsername != "reporter-"+sfx ||
		r.FilePath == nil || !strings.HasSuffix(*r.FilePath, "Andor.S02E05.mkv") || r.LibraryID != lib {
		t.Errorf("episode row = %+v", r)
	}
	if n, err := q.CountMediaIssuesAdmin(ctx, &open); err != nil || n != 1 {
		t.Errorf("open count = %d (%v), want 1", n, err)
	}

	all, err := q.ListMediaIssuesAdmin(ctx, gen.ListMediaIssuesAdminParams{Lim: 50})
	if err != nil || len(all) != 2 {
		t.Fatalf("all = %d (%v), want 2 (soft-deleted item excluded)", len(all), err)
	}
	if n, _ := q.CountMediaIssuesAdmin(ctx, nil); n != 2 {
		t.Errorf("all count = %d, want 2", n)
	}
	dismissed := "dismissed"
	d, _ := q.ListMediaIssuesAdmin(ctx, gen.ListMediaIssuesAdminParams{Status: &dismissed, Lim: 50})
	if len(d) != 1 || d[0].ID != movieIssue.ID || d[0].ParentTitle != nil {
		t.Errorf("dismissed = %+v", d)
	}
	// Paging.
	page, _ := q.ListMediaIssuesAdmin(ctx, gen.ListMediaIssuesAdminParams{Lim: 1, Off: 1})
	if len(page) != 1 || page[0].ID != all[1].ID {
		t.Errorf("page 2 = %+v, want %v", page, all[1].ID)
	}
}

func TestMediaIssues_Integration_DamagedFiles(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	sfx := uuid.New().String()[:8]

	lib := seedLibrary(ctx, t, q, "dmg-"+sfx)
	movie := seedMediaItem(ctx, t, q, lib, "Masters")
	bad := seedMediaFile(ctx, t, q, movie, "/m/"+sfx+"/Masters.mkv")
	ok := seedMediaFile(ctx, t, q, movie, "/m/"+sfx+"/Masters.ok.mkv")
	missing := seedMediaFile(ctx, t, q, movie, "/m/"+sfx+"/Masters.missing.mkv")
	deletedItem := seedMediaItem(ctx, t, q, lib, "Deleted")
	deletedFile := seedMediaFile(ctx, t, q, deletedItem, "/m/"+sfx+"/Deleted.mkv")

	detail := "only 2s decode"
	for _, f := range []uuid.UUID{bad, missing, deletedFile} {
		if err := q.UpdateMediaFileIntegrity(ctx, gen.UpdateMediaFileIntegrityParams{
			ID: f, IntegrityStatus: "damaged", IntegrityDetail: &detail,
		}); err != nil {
			t.Fatalf("UpdateMediaFileIntegrity: %v", err)
		}
	}
	if err := q.UpdateMediaFileIntegrity(ctx, gen.UpdateMediaFileIntegrityParams{ID: ok, IntegrityStatus: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_files SET status = 'missing', missing_since = NOW() WHERE id = $1`, missing); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = NOW() WHERE id = $1`, deletedItem); err != nil {
		t.Fatal(err)
	}

	rows, err := q.ListDamagedMediaFiles(ctx, gen.ListDamagedMediaFilesParams{Lim: 10})
	if err != nil {
		t.Fatalf("ListDamagedMediaFiles: %v", err)
	}
	if len(rows) != 1 || rows[0].FileID != bad || rows[0].ItemID != movie || rows[0].ItemTitle != "Masters" ||
		rows[0].IntegrityDetail == nil || *rows[0].IntegrityDetail != detail || !rows[0].IntegrityCheckedAt.Valid {
		t.Errorf("damaged = %+v, want only the active file of a live item", rows)
	}
	if n, err := q.CountDamagedMediaFiles(ctx); err != nil || n != 1 {
		t.Errorf("damaged count = %d (%v), want 1", n, err)
	}
}

// 00026 Down drops the table and the partial damaged-file index; Up
// re-creates both cleanly afterwards.
func TestMediaIssues_Integration_MigrationDownUp(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	sfx := uuid.New().String()[:8]
	u := seedUser(ctx, t, q, "mig-iss-"+sfx)
	lib := seedLibrary(ctx, t, q, "mig-iss-"+sfx)
	movie := seedMediaItem(ctx, t, q, lib, "M")
	if _, err := q.CreateMediaIssue(ctx, gen.CreateMediaIssueParams{ItemID: movie, UserID: u, Kind: "video"}); err != nil {
		t.Fatal(err)
	}

	up, down := migrationSections(t, "00026_media_issues.sql")
	exists := func(name string) bool {
		var reg *string
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1)::text`, name).Scan(&reg); err != nil {
			t.Fatalf("to_regclass(%s): %v", name, err)
		}
		return reg != nil
	}
	execSQL(ctx, t, pool, "00026 down", down)
	for _, name := range []string{"public.media_issues", "public.idx_media_files_integrity_damaged"} {
		if exists(name) {
			t.Errorf("after down: %s still exists", name)
		}
	}
	execSQL(ctx, t, pool, "00026 up", up)
	for _, name := range []string{
		"public.media_issues", "public.idx_media_issues_status_created", "public.idx_media_issues_item",
		"public.uq_media_issues_open_user_item_kind", "public.idx_media_files_integrity_damaged",
	} {
		if !exists(name) {
			t.Errorf("after up: %s missing", name)
		}
	}
	if _, err := q.CreateMediaIssue(ctx, gen.CreateMediaIssueParams{ItemID: movie, UserID: u, Kind: "video"}); err != nil {
		t.Errorf("insert after re-up: %v", err)
	}
}
