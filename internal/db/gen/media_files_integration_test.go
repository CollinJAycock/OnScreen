//go:build integration

package gen_test

import (
	"context"
	"testing"

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
