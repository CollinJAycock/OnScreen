//go:build integration

// Reproduces and guards the photo timeline-sidebar query (ListPhotoTimelineBuckets).
// On the deployed DB this returned HTTP 500 with
//   column "pm.taken_at" must appear in the GROUP BY clause ... (SQLSTATE 42803)
// because GROUP BY referenced the SELECT-list aliases (year, month) rather than the
// grouped expressions. This test runs the query against a real Postgres so the
// regression can't reappear.
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

func seedPhoto(ctx context.Context, t *testing.T, q *gen.Queries, lib uuid.UUID, title string, takenAt *time.Time) {
	t.Helper()
	item, err := q.CreateMediaItem(ctx, gen.CreateMediaItemParams{
		LibraryID: lib,
		Type:      "photo",
		Title:     title,
		SortTitle: title,
	})
	if err != nil {
		t.Fatalf("CreateMediaItem %q: %v", title, err)
	}
	var ta pgtype.Timestamptz
	if takenAt != nil {
		ta = pgtype.Timestamptz{Time: *takenAt, Valid: true}
	}
	if err := q.UpsertPhotoMetadata(ctx, gen.UpsertPhotoMetadataParams{ItemID: item.ID, TakenAt: ta}); err != nil {
		t.Fatalf("UpsertPhotoMetadata %q: %v", title, err)
	}
}

func TestListPhotoTimelineBuckets_Integration_GroupsByMonth(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	lib := seedLibrary(ctx, t, q, "timeline-"+uuid.New().String()[:8])

	mar1 := time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)
	mar2 := time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC)
	jan := time.Date(2023, 1, 9, 0, 0, 0, 0, time.UTC)
	seedPhoto(ctx, t, q, lib, "mar-a", &mar1)
	seedPhoto(ctx, t, q, lib, "mar-b", &mar2)
	seedPhoto(ctx, t, q, lib, "jan", &jan)
	// taken_at NULL -> falls back to created_at (today); just must not error.
	seedPhoto(ctx, t, q, lib, "no-exif", nil)

	rows, err := q.ListPhotoTimelineBuckets(ctx, lib)
	if err != nil {
		t.Fatalf("ListPhotoTimelineBuckets: %v", err)
	}

	// Find the March 2024 bucket: two photos must collapse into one (year,month) row.
	var marCount int64
	for _, r := range rows {
		if r.Year == 2024 && r.Month == 3 {
			marCount = r.Count
		}
		if r.Count <= 0 {
			t.Errorf("bucket %d-%02d has non-positive count %d", r.Year, r.Month, r.Count)
		}
	}
	if marCount != 2 {
		t.Errorf("March 2024 bucket count = %d, want 2 (the two March photos grouped)", marCount)
	}

	// Descending order by (year, month).
	for i := 1; i < len(rows); i++ {
		a, b := rows[i-1], rows[i]
		if a.Year < b.Year || (a.Year == b.Year && a.Month < b.Month) {
			t.Errorf("buckets not in DESC order at %d: %d-%02d before %d-%02d", i, a.Year, a.Month, b.Year, b.Month)
		}
	}
}
