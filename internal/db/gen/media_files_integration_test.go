//go:build integration

package gen_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func bdPtr(v int32) *int32 { return &v }

// TestMediaFile_Integration_VideoBitDepthRoundTrip exercises the
// video_bit_depth column end-to-end against a real Postgres: it must survive
// CreateMediaFile's RETURNING, GetMediaFile's SELECT, and (critically for the
// re-probe backfill) UpdateMediaFileTechnicalMetadata. NULL on create must
// read back as nil.
func TestMediaFile_Integration_VideoBitDepthRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)

	libID := seedLibrary(ctx, t, q, "vbd")
	itemID := seedMediaItem(ctx, t, q, libID, "Bit Depth Movie")

	// Create with a 10-bit video stream → RETURNING must echo it.
	created, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
		MediaItemID:   itemID,
		FilePath:      "/media/vbd-10.mkv",
		FileSize:      1,
		VideoBitDepth: bdPtr(10),
	})
	if err != nil {
		t.Fatalf("CreateMediaFile: %v", err)
	}
	if created.VideoBitDepth == nil || *created.VideoBitDepth != 10 {
		t.Fatalf("CreateMediaFile RETURNING video_bit_depth = %v, want 10", created.VideoBitDepth)
	}

	// SELECT round-trip.
	got, err := q.GetMediaFile(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMediaFile: %v", err)
	}
	if got.VideoBitDepth == nil || *got.VideoBitDepth != 10 {
		t.Fatalf("GetMediaFile video_bit_depth = %v, want 10", got.VideoBitDepth)
	}

	// Re-probe path: UpdateMediaFileTechnicalMetadata must persist a new depth
	// (e.g. an 8-bit re-encode replacing a mis-detected value).
	if err := q.UpdateMediaFileTechnicalMetadata(ctx, gen.UpdateMediaFileTechnicalMetadataParams{
		ID:            created.ID,
		VideoBitDepth: bdPtr(8),
	}); err != nil {
		t.Fatalf("UpdateMediaFileTechnicalMetadata: %v", err)
	}
	updated, err := q.GetMediaFile(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMediaFile after update: %v", err)
	}
	if updated.VideoBitDepth == nil || *updated.VideoBitDepth != 8 {
		t.Fatalf("after re-probe update, video_bit_depth = %v, want 8", updated.VideoBitDepth)
	}

	// NULL on create reads back as nil (the common pre-backfill case).
	nullFile, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
		MediaItemID: itemID,
		FilePath:    "/media/vbd-null.mkv",
		FileSize:    1,
	})
	if err != nil {
		t.Fatalf("CreateMediaFile (null depth): %v", err)
	}
	if nullFile.VideoBitDepth != nil {
		t.Fatalf("unset video_bit_depth = %v, want nil", *nullFile.VideoBitDepth)
	}
}

func integStrPtr(s string) *string { return &s }

// TestMediaFile_Integration_IntegrityRoundTrip pins the integrity columns
// (migration 00018) end-to-end: the 'unchecked' default on create, the
// verdict write, the work-queue visibility flip, the CHECK constraint, and
// — critically — the reset back to 'unchecked' when the scanner re-writes
// technical metadata (file content changed → verdict stale).
func TestMediaFile_Integration_IntegrityRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)

	libID := seedLibrary(ctx, t, q, "integ")
	itemID := seedMediaItem(ctx, t, q, libID, "Integrity Movie")

	created, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
		MediaItemID: itemID,
		FilePath:    "/media/fake-release.mkv",
		FileSize:    1,
		VideoCodec:  integStrPtr("hevc"),
	})
	if err != nil {
		t.Fatalf("CreateMediaFile: %v", err)
	}
	if created.IntegrityStatus != "unchecked" {
		t.Fatalf("default integrity_status = %q, want unchecked", created.IntegrityStatus)
	}
	if created.IntegrityCheckedAt.Valid || created.IntegrityDetail != nil {
		t.Fatalf("fresh row must have no probe timestamp/detail, got %v / %v",
			created.IntegrityCheckedAt, created.IntegrityDetail)
	}

	// Unchecked video file in a movie library → visible to the probe queue.
	lib := pgtype.UUID{Bytes: [16]byte(libID), Valid: true}
	queued, err := q.ListFilesForIntegrityCheck(ctx, gen.ListFilesForIntegrityCheckParams{LibraryID: lib, Lim: 10})
	if err != nil {
		t.Fatalf("ListFilesForIntegrityCheck: %v", err)
	}
	if len(queued) != 1 || queued[0].ID != created.ID {
		t.Fatalf("work queue = %d files, want the created file", len(queued))
	}

	// Record a damaged verdict → detail + timestamp persist, queue empties.
	if err := q.UpdateMediaFileIntegrity(ctx, gen.UpdateMediaFileIntegrityParams{
		ID:              created.ID,
		IntegrityStatus: "damaged",
		IntegrityDetail: integStrPtr("spot-decode failed at 50% (t=3600s): 0/5 frames decoded"),
	}); err != nil {
		t.Fatalf("UpdateMediaFileIntegrity: %v", err)
	}
	got, err := q.GetMediaFile(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMediaFile: %v", err)
	}
	if got.IntegrityStatus != "damaged" || got.IntegrityDetail == nil || !got.IntegrityCheckedAt.Valid {
		t.Fatalf("after verdict: status=%q detail=%v checkedAt=%v", got.IntegrityStatus, got.IntegrityDetail, got.IntegrityCheckedAt)
	}
	n, err := q.CountFilesForIntegrityCheck(ctx, lib)
	if err != nil {
		t.Fatalf("CountFilesForIntegrityCheck: %v", err)
	}
	if n != 0 {
		t.Fatalf("queue after verdict = %d, want 0", n)
	}

	// Scanner re-writes technical metadata (content changed on disk) →
	// verdict resets to unchecked and the file re-enters the queue.
	if err := q.UpdateMediaFileTechnicalMetadata(ctx, gen.UpdateMediaFileTechnicalMetadataParams{
		ID:         created.ID,
		VideoCodec: integStrPtr("hevc"),
	}); err != nil {
		t.Fatalf("UpdateMediaFileTechnicalMetadata: %v", err)
	}
	reset, err := q.GetMediaFile(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMediaFile after reset: %v", err)
	}
	if reset.IntegrityStatus != "unchecked" || reset.IntegrityCheckedAt.Valid || reset.IntegrityDetail != nil {
		t.Fatalf("technical-metadata update must reset the verdict, got status=%q checkedAt=%v detail=%v",
			reset.IntegrityStatus, reset.IntegrityCheckedAt, reset.IntegrityDetail)
	}
	if n, _ := q.CountFilesForIntegrityCheck(ctx, lib); n != 1 {
		t.Fatalf("queue after reset = %d, want 1", n)
	}

	// The CHECK constraint rejects values outside unchecked|ok|damaged.
	if err := q.UpdateMediaFileIntegrity(ctx, gen.UpdateMediaFileIntegrityParams{
		ID:              created.ID,
		IntegrityStatus: "banana",
	}); err == nil {
		t.Fatal("integrity_status CHECK constraint should reject unknown values")
	}
}

func dvPtr(v int16) *int16 { return &v }

// TestMediaFile_Integration_DynamicRange pins dv_profile (migration 00036) and
// the narrow re-classification write the scanner uses for files an older scan
// tagged 'dolby_vision': it changes hdr_type and dv_profile only, keeping the
// integrity verdict and scanned_at (the file's bytes didn't change), and every
// full-row read returns the profile.
func TestMediaFile_Integration_DynamicRange(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)

	libID := seedLibrary(ctx, t, q, "dv")
	itemID := seedMediaItem(ctx, t, q, libID, "Dolby Vision Movie")

	// An old-style row: tagged Dolby Vision, no profile.
	created, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
		MediaItemID: itemID,
		FilePath:    "/media/dv-p8-over-sdr.mkv",
		FileSize:    1,
		VideoCodec:  integStrPtr("hevc"),
		HdrType:     integStrPtr("dolby_vision"),
	})
	if err != nil {
		t.Fatalf("CreateMediaFile: %v", err)
	}
	if created.DvProfile != nil {
		t.Fatalf("unset dv_profile = %v, want nil", *created.DvProfile)
	}
	if err := q.UpdateMediaFileIntegrity(ctx, gen.UpdateMediaFileIntegrityParams{
		ID: created.ID, IntegrityStatus: "ok",
	}); err != nil {
		t.Fatalf("UpdateMediaFileIntegrity: %v", err)
	}
	before, err := q.GetMediaFile(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMediaFile: %v", err)
	}

	// Re-read: a profile 8 record over SDR video → SDR, profile 8.
	if err := q.UpdateMediaFileDynamicRange(ctx, gen.UpdateMediaFileDynamicRangeParams{
		ID: created.ID, HdrType: nil, DvProfile: dvPtr(8),
	}); err != nil {
		t.Fatalf("UpdateMediaFileDynamicRange: %v", err)
	}
	got, err := q.GetMediaFile(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMediaFile after re-read: %v", err)
	}
	if got.HdrType != nil || got.DvProfile == nil || *got.DvProfile != 8 {
		t.Fatalf("after re-read hdr_type=%v dv_profile=%v, want nil / 8", got.HdrType, got.DvProfile)
	}
	if got.IntegrityStatus != "ok" || !got.IntegrityCheckedAt.Valid {
		t.Fatalf("re-read must keep the integrity verdict, got %q (checked %v)", got.IntegrityStatus, got.IntegrityCheckedAt.Valid)
	}
	if !got.ScannedAt.Time.Equal(before.ScannedAt.Time) {
		t.Fatalf("re-read must keep scanned_at: %v -> %v", before.ScannedAt.Time, got.ScannedAt.Time)
	}
	if got.VideoCodec == nil || *got.VideoCodec != "hevc" || got.FilePath != before.FilePath {
		t.Fatalf("re-read touched other columns: codec=%v path=%q", got.VideoCodec, got.FilePath)
	}

	// Every full-row read carries the profile.
	byPath, err := q.GetMediaFileByPath(ctx, created.FilePath)
	if err != nil || byPath.DvProfile == nil || *byPath.DvProfile != 8 {
		t.Fatalf("GetMediaFileByPath dv_profile = %v (err %v), want 8", byPath.DvProfile, err)
	}
	forItem, err := q.ListMediaFilesForItem(ctx, itemID)
	if err != nil || len(forItem) != 1 || forItem[0].DvProfile == nil || *forItem[0].DvProfile != 8 {
		t.Fatalf("ListMediaFilesForItem dv_profile wrong (err %v)", err)
	}
	inLib, err := q.ListActiveFilesForLibrary(ctx, libID)
	if err != nil || len(inLib) != 1 || inLib[0].DvProfile == nil || *inLib[0].DvProfile != 8 {
		t.Fatalf("ListActiveFilesForLibrary dv_profile wrong (err %v)", err)
	}

	// A new scan of changed content writes it through the full update too.
	if err := q.UpdateMediaFileTechnicalMetadata(ctx, gen.UpdateMediaFileTechnicalMetadataParams{
		ID: created.ID, VideoCodec: integStrPtr("hevc"), HdrType: integStrPtr("dolby_vision"), DvProfile: dvPtr(5),
	}); err != nil {
		t.Fatalf("UpdateMediaFileTechnicalMetadata: %v", err)
	}
	p5, err := q.GetMediaFile(ctx, created.ID)
	if err != nil || p5.DvProfile == nil || *p5.DvProfile != 5 || p5.HdrType == nil || *p5.HdrType != "dolby_vision" {
		t.Fatalf("after technical update hdr_type=%v dv_profile=%v (err %v), want dolby_vision / 5", p5.HdrType, p5.DvProfile, err)
	}

	// CreateMediaFile's RETURNING echoes a profile.
	withProfile, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
		MediaItemID: itemID,
		FilePath:    "/media/dv-p7.mkv",
		FileSize:    1,
		HdrType:     integStrPtr("hdr10"),
		DvProfile:   dvPtr(7),
	})
	if err != nil || withProfile.DvProfile == nil || *withProfile.DvProfile != 7 {
		t.Fatalf("CreateMediaFile RETURNING dv_profile = %v (err %v), want 7", withProfile.DvProfile, err)
	}
}
