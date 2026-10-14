//go:build integration

// Round-trips the DVR post-processing queue (migration 00052), the media
// file re-point that swaps a processed recording in, and commercial markers.
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func seedCompletedRecording(ctx context.Context, t *testing.T, q *gen.Queries, user, chID uuid.UUID, path string) uuid.UUID {
	t.Helper()
	r, err := q.UpsertRecording(ctx, gen.UpsertRecordingParams{
		UserID: user, ChannelID: chID, Title: "News",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(-2 * time.Hour), Valid: true},
		EndsAt:   pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("UpsertRecording: %v", err)
	}
	if _, err := q.SetRecordingStartedFile(ctx, gen.SetRecordingStartedFileParams{ID: r.ID, FilePath: &path}); err != nil {
		t.Fatalf("SetRecordingStartedFile: %v", err)
	}
	if err := q.SetRecordingStatus(ctx, gen.SetRecordingStatusParams{ID: r.ID, Status: "completed"}); err != nil {
		t.Fatalf("SetRecordingStatus: %v", err)
	}
	return r.ID
}

func TestDVRPostProcess_Integration_QueueLifecycle(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	user := seedUser(ctx, t, q, "dvr-pp-"+uuid.New().String()[:8])
	chID := seedTunerChannel(t, q, "dvr-pp-tuner-"+uuid.New().String()[:6])
	recA := seedCompletedRecording(ctx, t, q, user, chID, "/dvr/a.mp4")
	recB := seedCompletedRecording(ctx, t, q, user, chID, "/dvr/b.mp4")

	if _, err := q.ClaimNextDVRPostProcess(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim on empty queue: %v, want ErrNoRows", err)
	}
	for _, id := range []uuid.UUID{recA, recB} {
		n, err := q.EnqueueDVRPostProcess(ctx, gen.EnqueueDVRPostProcessParams{RecordingID: id, Kind: "full"})
		if err != nil || n != 1 {
			t.Fatalf("enqueue %s: n=%d err=%v", id, n, err)
		}
	}
	// Already queued: refused.
	if n, err := q.EnqueueDVRPostProcess(ctx, gen.EnqueueDVRPostProcessParams{RecordingID: recA, Kind: "detect"}); err != nil || n != 0 {
		t.Fatalf("re-enqueue while queued: n=%d err=%v, want 0", n, err)
	}

	job, err := q.ClaimNextDVRPostProcess(ctx)
	if err != nil || job.RecordingID != recA || job.Status != "processing" || job.Kind != "full" {
		t.Fatalf("claim = %+v, %v; want recA processing", job, err)
	}
	msg := "Remuxing to MKV"
	if err := q.SetDVRPostProcessMessage(ctx, gen.SetDVRPostProcessMessageParams{RecordingID: recA, Message: &msg}); err != nil {
		t.Fatal(err)
	}
	rows, err := q.ListRecordingsForUser(ctx, gen.ListRecordingsForUserParams{UserID: user, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]gen.ListRecordingsForUserRow{}
	for _, r := range rows {
		seen[r.ID] = r
	}
	if a := seen[recA]; a.PostprocessStatus == nil || *a.PostprocessStatus != "processing" || a.PostprocessMessage == nil || *a.PostprocessMessage != msg {
		t.Errorf("list row for A: status=%v message=%v", a.PostprocessStatus, a.PostprocessMessage)
	}

	done := "remuxed to MKV"
	if err := q.FinishDVRPostProcess(ctx, gen.FinishDVRPostProcessParams{RecordingID: recA, Status: "processed", Message: &done}); err != nil {
		t.Fatal(err)
	}
	// Finished jobs can be re-queued (e.g. Detect commercials afterwards).
	if n, _ := q.EnqueueDVRPostProcess(ctx, gen.EnqueueDVRPostProcessParams{RecordingID: recA, Kind: "detect"}); n != 1 {
		t.Fatalf("re-enqueue after finish: n=%d, want 1", n)
	}
	// B was queued first, so it is next.
	if job, err := q.ClaimNextDVRPostProcess(ctx); err != nil || job.RecordingID != recB {
		t.Fatalf("second claim = %+v, %v; want recB", job, err)
	}
	// A server restart fails whatever was mid-run.
	if n, err := q.FailInterruptedDVRPostProcess(ctx); err != nil || n != 1 {
		t.Fatalf("fail interrupted: n=%d err=%v", n, err)
	}
	b, _ := q.GetDVRPostProcessJob(ctx, recB)
	if b.Status != "failed" || b.Message == nil || *b.Message != "interrupted by server restart" {
		t.Errorf("job B = %+v", b)
	}

	// Deleting the recording drops its job.
	if err := q.DeleteRecording(ctx, recA); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetDVRPostProcessJob(ctx, recA); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("job survived its recording: %v", err)
	}
}

func TestDVRPostProcess_Integration_RepointAndCommercials(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	lib := seedLibrary(ctx, t, q, "dvr-pp-lib-"+uuid.New().String()[:6])
	item := seedMediaItem(ctx, t, q, lib, "Evening News")
	hash := "abc123"
	mpeg2, mp4 := "mpeg2video", "mov"
	f, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
		MediaItemID: item, FilePath: "/dvr/news-" + uuid.NewString()[:6] + ".mp4", FileSize: 100,
		Container: &mp4, VideoCodec: &mpeg2, FileHash: &hash,
	})
	if err != nil {
		t.Fatalf("CreateMediaFile: %v", err)
	}

	for _, m := range []gen.InsertCommercialMarkerParams{
		{MediaFileID: f.ID, StartMs: 600500, EndMs: 690000, Source: "heuristic"},
		{MediaFileID: f.ID, StartMs: 1200000, EndMs: 1290000, Source: "heuristic"},
	} {
		if err := q.InsertCommercialMarker(ctx, m); err != nil {
			t.Fatalf("InsertCommercialMarker: %v", err)
		}
	}
	if err := q.InsertCommercialMarker(ctx, gen.InsertCommercialMarkerParams{
		MediaFileID: f.ID, StartMs: 5, EndMs: 5, Source: "heuristic",
	}); err == nil {
		t.Error("an empty break was accepted")
	}

	newPath := "/dvr/news-" + uuid.NewString()[:6] + ".mkv"
	mkv, h264, dur := "matroska", "h264", int64(1800000)
	n, err := q.RepointMediaFile(ctx, gen.RepointMediaFileParams{
		ID: f.ID, NewPath: newPath, FileSize: 80, Container: &mkv, VideoCodec: &h264, DurationMs: &dur,
	})
	if err != nil || n != 1 {
		t.Fatalf("RepointMediaFile: n=%d err=%v", n, err)
	}
	got, err := q.GetMediaFileByPath(ctx, newPath)
	if err != nil {
		t.Fatalf("file not found at its new path: %v", err)
	}
	if got.ID != f.ID || got.MediaItemID != item {
		t.Errorf("re-point changed identity: %+v", got)
	}
	if got.FileSize != 80 || *got.Container != "matroska" || *got.VideoCodec != "h264" || got.FileHash != nil {
		t.Errorf("re-pointed row = size %d container %v codec %v hash %v", got.FileSize, *got.Container, *got.VideoCodec, got.FileHash)
	}

	// The breaks followed the file across the re-point.
	ms, err := q.ListCommercialMarkersByItem(ctx, item)
	if err != nil || len(ms) != 2 || ms[0].StartMs != 600500 || ms[0].MediaFileID != f.ID {
		t.Fatalf("markers = %+v, %v", ms, err)
	}
	if err := q.DeleteCommercialMarkersByFile(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	if ms, _ := q.ListCommercialMarkersByItem(ctx, item); len(ms) != 0 {
		t.Errorf("markers after delete = %+v", ms)
	}

	// Deleting the file cascades to its markers.
	if err := q.InsertCommercialMarker(ctx, gen.InsertCommercialMarkerParams{
		MediaFileID: f.ID, StartMs: 1, EndMs: 2000, Source: "comskip",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.HardDeleteMediaFile(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM commercial_markers WHERE media_file_id = $1`, f.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("markers outlived their file: %d (%v)", left, err)
	}
}
