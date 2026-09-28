//go:build integration

// Round-trips against a real Postgres testcontainer for migration 00025
// (live Radarr/Sonarr download status on media_requests): the new columns and
// their CHECKs, the sync's work-list query and round-robin order, the
// state-refresh / fail / recover transitions the arr_request_sync task relies
// on for exactly-once failure notices, MarkMediaRequestAvailable clearing the
// download fields, and the migration's Down/Up.
//
// Run with: go test -tags 'dev integration' ./internal/db/gen/ -run RequestDownloads
package gen_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// seedDownloadRequest inserts a request for (user, type, tmdb) in the given
// status, routed to svc when svc != nil (approved-and-dispatched shape).
func seedDownloadRequest(ctx context.Context, t *testing.T, pool *pgxpool.Pool, q *gen.Queries,
	user uuid.UUID, typ string, tmdb int32, status string, svc *uuid.UUID) gen.MediaRequest {
	t.Helper()
	r, err := q.CreateMediaRequest(ctx, gen.CreateMediaRequestParams{
		UserID: user, Type: typ, TmdbID: tmdb, Title: "T", Status: status,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest %d/%s: %v", tmdb, status, err)
	}
	if svc != nil {
		if _, err := pool.Exec(ctx, `UPDATE media_requests SET service_id = $2 WHERE id = $1`, r.ID, *svc); err != nil {
			t.Fatalf("route request: %v", err)
		}
	}
	got, err := q.GetMediaRequest(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetMediaRequest: %v", err)
	}
	return got
}

func seedDownloadArrService(ctx context.Context, t *testing.T, q *gen.Queries, kind string) uuid.UUID {
	t.Helper()
	svc, err := q.CreateArrService(ctx, gen.CreateArrServiceParams{
		ID: uuid.New(), Name: kind + "-" + uuid.New().String()[:6], Kind: kind,
		BaseUrl: "http://arr.invalid", ApiKey: "k", DefaultTags: []byte("[]"), Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateArrService: %v", err)
	}
	return svc.ID
}

func TestRequestDownloads_Integration_WorkListAndOrder(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	user := seedUser(ctx, t, q, "dl-list-"+uuid.New().String()[:8])
	svc := seedDownloadArrService(ctx, t, q, "radarr")

	downloading := seedDownloadRequest(ctx, t, pool, q, user, "movie", 1, "downloading", &svc)
	approved := seedDownloadRequest(ctx, t, pool, q, user, "movie", 2, "approved", &svc)
	failedRecent := seedDownloadRequest(ctx, t, pool, q, user, "movie", 3, "failed", &svc)
	failedOld := seedDownloadRequest(ctx, t, pool, q, user, "movie", 4, "failed", &svc)
	seedDownloadRequest(ctx, t, pool, q, user, "movie", 5, "pending", &svc)
	seedDownloadRequest(ctx, t, pool, q, user, "movie", 6, "available", &svc)
	seedDownloadRequest(ctx, t, pool, q, user, "movie", 7, "downloading", nil) // service deleted
	if _, err := pool.Exec(ctx, `UPDATE media_requests SET updated_at = now() - interval '40 days' WHERE id = $1`, failedOld.ID); err != nil {
		t.Fatal(err)
	}
	// downloading was checked a minute ago; the others never.
	if _, err := pool.Exec(ctx, `UPDATE media_requests SET download_updated_at = now() - interval '1 minute' WHERE id = $1`, downloading.ID); err != nil {
		t.Fatal(err)
	}

	rows, err := q.ListMediaRequestsForDownloadSync(ctx, gen.ListMediaRequestsForDownloadSyncParams{
		FailedSince: pgtype.Timestamptz{Time: time.Now().Add(-30 * 24 * time.Hour), Valid: true},
		MaxRows:     100,
	})
	if err != nil {
		t.Fatalf("ListMediaRequestsForDownloadSync: %v", err)
	}
	var got []uuid.UUID
	for _, r := range rows {
		if r.UserID == user {
			got = append(got, r.ID)
		}
	}
	// Never-checked first (by created_at), then the checked one.
	want := []uuid.UUID{approved.ID, failedRecent.ID, downloading.ID}
	if len(got) != len(want) {
		t.Fatalf("work list = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("work list[%d] = %v, want %v", i, got[i], want[i])
		}
	}

	capped, err := q.ListMediaRequestsForDownloadSync(ctx, gen.ListMediaRequestsForDownloadSyncParams{
		FailedSince: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}, MaxRows: 1,
	})
	if err != nil || len(capped) != 1 {
		t.Errorf("max_rows: %d rows, %v", len(capped), err)
	}
}

func TestRequestDownloads_Integration_ColumnsAndTransitions(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	user := seedUser(ctx, t, q, "dl-tx-"+uuid.New().String()[:8])
	svc := seedDownloadArrService(ctx, t, q, "radarr")
	r := seedDownloadRequest(ctx, t, pool, q, user, "movie", 949, "downloading", &svc)
	originalUpdated := r.UpdatedAt.Time

	// Link to the Radarr movie; the CHECK refuses a non-positive id.
	seven := int32(7)
	if err := q.SetMediaRequestArrItem(ctx, gen.SetMediaRequestArrItemParams{ID: r.ID, ArrItemID: &seven}); err != nil {
		t.Fatalf("SetMediaRequestArrItem: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_requests SET arr_item_id = 0 WHERE id = $1`, r.ID); err == nil {
		t.Error("arr_item_id = 0 accepted")
	}

	// A state refresh stores every field, leaves status and updated_at alone.
	state, msg := "downloading", "50% of 4 GB"
	p := float32(0.5)
	size := int64(4 << 30)
	eta := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Second)
	n, err := q.UpdateMediaRequestDownload(ctx, gen.UpdateMediaRequestDownloadParams{
		ID: r.ID, DownloadState: &state, DownloadProgress: &p,
		DownloadEta: pgtype.Timestamptz{Time: eta, Valid: true}, DownloadSizeBytes: &size, DownloadMessage: &msg,
	})
	if err != nil || n != 1 {
		t.Fatalf("UpdateMediaRequestDownload = (%d, %v)", n, err)
	}
	got, _ := q.GetMediaRequest(ctx, r.ID)
	if got.Status != "downloading" || !got.UpdatedAt.Time.Equal(originalUpdated) {
		t.Errorf("status %q updated_at %v (was %v): a refresh must not move either", got.Status, got.UpdatedAt.Time, originalUpdated)
	}
	if got.ArrItemID == nil || *got.ArrItemID != 7 || got.DownloadState == nil || *got.DownloadState != "downloading" ||
		got.DownloadProgress == nil || *got.DownloadProgress != 0.5 || !got.DownloadEta.Time.Equal(eta) ||
		got.DownloadSizeBytes == nil || *got.DownloadSizeBytes != size || !got.DownloadUpdatedAt.Valid {
		t.Errorf("row after refresh = %+v", got)
	}

	// CHECKs: the six states only, progress in [0,1], size >= 0.
	for _, bad := range []string{
		`UPDATE media_requests SET download_state = 'paused' WHERE id = $1`,
		`UPDATE media_requests SET download_progress = 1.5 WHERE id = $1`,
		`UPDATE media_requests SET download_progress = -0.1 WHERE id = $1`,
		`UPDATE media_requests SET download_size_bytes = -1 WHERE id = $1`,
	} {
		if _, err := pool.Exec(ctx, bad, r.ID); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
	for _, ok := range []string{"searching", "queued", "downloading", "import_pending", "stalled", "failed"} {
		if _, err := pool.Exec(ctx, `UPDATE media_requests SET download_state = $2 WHERE id = $1`, r.ID, ok); err != nil {
			t.Errorf("state %q refused: %v", ok, err)
		}
	}

	// Into failed: exactly one caller wins.
	reason := "Download client reported failure"
	n1, err1 := q.FailMediaRequestDownload(ctx, gen.FailMediaRequestDownloadParams{ID: r.ID, DownloadMessage: &reason})
	n2, err2 := q.FailMediaRequestDownload(ctx, gen.FailMediaRequestDownloadParams{ID: r.ID, DownloadMessage: &reason})
	if err1 != nil || err2 != nil || n1 != 1 || n2 != 0 {
		t.Fatalf("fail twice = (%d,%v) (%d,%v), want 1 then 0", n1, err1, n2, err2)
	}
	got, _ = q.GetMediaRequest(ctx, r.ID)
	if got.Status != "failed" || *got.DownloadState != "failed" || got.DownloadEta.Valid || !got.UpdatedAt.Time.After(originalUpdated) {
		t.Errorf("after fail = status %q state %v eta %v updated %v", got.Status, got.DownloadState, got.DownloadEta, got.UpdatedAt.Time)
	}
	// A refresh still reaches a failed row (the sync keeps its message current).
	if n, _ := q.UpdateMediaRequestDownload(ctx, gen.UpdateMediaRequestDownloadParams{ID: r.ID, DownloadState: downloadStrPtr("failed")}); n != 1 {
		t.Errorf("refresh of failed row touched %d", n)
	}

	// ListActiveMediaRequestsForTMDB includes the failed row (a file landing
	// still fulfils it).
	active, err := q.ListActiveMediaRequestsForTMDB(ctx, gen.ListActiveMediaRequestsForTMDBParams{Type: "movie", TmdbID: 949})
	if err != nil || len(active) != 1 {
		t.Errorf("active for tmdb = %d rows, %v", len(active), err)
	}

	// Out of failed while the user has opened a newer request: refused.
	newer := seedDownloadRequest(ctx, t, pool, q, user, "movie", 949, "pending", nil)
	queued := "queued"
	if n, err := q.RecoverMediaRequestDownload(ctx, gen.RecoverMediaRequestDownloadParams{ID: r.ID, DownloadState: &queued}); err != nil || n != 0 {
		t.Errorf("recover while superseded = (%d, %v), want 0", n, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM media_requests WHERE id = $1`, newer.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := q.RecoverMediaRequestDownload(ctx, gen.RecoverMediaRequestDownloadParams{ID: r.ID, DownloadState: &queued}); err != nil || n != 1 {
		t.Fatalf("recover = (%d, %v), want 1", n, err)
	}
	got, _ = q.GetMediaRequest(ctx, r.ID)
	if got.Status != "downloading" || *got.DownloadState != "queued" {
		t.Errorf("after recover = %q / %v", got.Status, got.DownloadState)
	}
	// Recover only applies to failed rows.
	if n, _ := q.RecoverMediaRequestDownload(ctx, gen.RecoverMediaRequestDownloadParams{ID: r.ID, DownloadState: &queued}); n != 0 {
		t.Errorf("recover of a downloading row touched %d", n)
	}

	// Available clears the download fields — also from failed.
	if _, err := q.FailMediaRequestDownload(ctx, gen.FailMediaRequestDownloadParams{ID: r.ID}); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkMediaRequestAvailable(ctx, gen.MarkMediaRequestAvailableParams{ID: r.ID}); err != nil {
		t.Fatalf("MarkMediaRequestAvailable: %v", err)
	}
	got, _ = q.GetMediaRequest(ctx, r.ID)
	if got.Status != "available" || got.DownloadState != nil || got.DownloadProgress != nil || got.DownloadEta.Valid ||
		got.DownloadSizeBytes != nil || got.DownloadMessage != nil || got.DownloadUpdatedAt.Valid {
		t.Errorf("after available = %+v", got)
	}
	// ...and neither a refresh nor a failure touches it afterwards.
	if n, _ := q.UpdateMediaRequestDownload(ctx, gen.UpdateMediaRequestDownloadParams{ID: r.ID, DownloadState: &queued}); n != 0 {
		t.Errorf("refresh of available row touched %d", n)
	}
	if n, _ := q.FailMediaRequestDownload(ctx, gen.FailMediaRequestDownloadParams{ID: r.ID}); n != 0 {
		t.Errorf("fail of available row touched %d", n)
	}
}

func TestRequestDownloads_Integration_Usernames(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	suffix := uuid.New().String()[:8]
	a := seedUser(ctx, t, q, "dl-a-"+suffix)
	b := seedUser(ctx, t, q, "dl-b-"+suffix)
	rows, err := q.ListUsernamesByIDs(ctx, []uuid.UUID{a, b, uuid.New()})
	if err != nil {
		t.Fatalf("ListUsernamesByIDs: %v", err)
	}
	names := map[uuid.UUID]string{}
	for _, r := range rows {
		names[r.ID] = r.Username
	}
	if len(names) != 2 || names[a] != "dl-a-"+suffix || names[b] != "dl-b-"+suffix {
		t.Errorf("names = %v", names)
	}
	if rows, err := q.ListUsernamesByIDs(ctx, []uuid.UUID{}); err != nil || len(rows) != 0 {
		t.Errorf("empty ids = %v, %v", rows, err)
	}
}

// 00025's Down drops the columns, constraints and index; Up re-applies
// cleanly over existing rows. Executed directly (see migrationSections) so
// later migrations don't have to roll back.
func TestRequestDownloads_Integration_MigrationDownUp(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	user := seedUser(ctx, t, q, "dl-mig-"+uuid.New().String()[:8])
	svc := seedDownloadArrService(ctx, t, q, "sonarr")
	r := seedDownloadRequest(ctx, t, pool, q, user, "show", 1, "downloading", &svc)
	if _, err := pool.Exec(ctx, `UPDATE media_requests SET download_state = 'stalled', arr_item_id = 3 WHERE id = $1`, r.ID); err != nil {
		t.Fatal(err)
	}

	up, down := migrationSections(t, "00025_request_download_status.sql")
	count := func() (cols, idx int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_name = 'media_requests' AND (column_name LIKE 'download_%' OR column_name = 'arr_item_id')`).Scan(&cols); err != nil {
			t.Fatalf("columns: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'media_requests_download_sync'`).Scan(&idx); err != nil {
			t.Fatalf("index: %v", err)
		}
		return cols, idx
	}
	if cols, idx := count(); cols != 7 || idx != 1 {
		t.Fatalf("migrated schema: %d columns, %d index; want 7 / 1", cols, idx)
	}
	execSQL(ctx, t, pool, "00025 down", down)
	if cols, idx := count(); cols != 0 || idx != 0 {
		t.Errorf("after down: %d columns, %d index remain", cols, idx)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM media_requests WHERE id = $1`, r.ID).Scan(&status); err != nil || status != "downloading" {
		t.Errorf("request after down = %q, %v", status, err)
	}
	execSQL(ctx, t, pool, "00025 up", up)
	if cols, idx := count(); cols != 7 || idx != 1 {
		t.Errorf("after up: %d columns, %d index", cols, idx)
	}
	got, err := q.GetMediaRequest(ctx, r.ID)
	if err != nil || got.DownloadState != nil || got.ArrItemID != nil {
		t.Errorf("request after re-up = %+v, %v (new columns start NULL)", got, err)
	}
}

func downloadStrPtr(s string) *string { return &s }
