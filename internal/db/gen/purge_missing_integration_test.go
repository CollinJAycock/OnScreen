//go:build integration

// Round-trips PurgeExpiredMissingFiles, the single-statement (atomic) form of
// PromoteExpiredMissing's hard-delete + parent soft-delete cascade. The query
// is subtle: all its data-modifying CTEs run against the same pre-statement
// snapshot, so the cascade can't see the DELETE's effect on media_files and must
// instead exclude the about-to-be-deleted files itself (mf.id <> ALL(@file_ids)).
// These tests exist to prove that reformulation actually cascades correctly
// against a real Postgres, not just that sqlc parses it.
package gen_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func seedMediaFile(ctx context.Context, t *testing.T, q *gen.Queries, itemID uuid.UUID, path string) uuid.UUID {
	t.Helper()
	f, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
		MediaItemID: itemID,
		FilePath:    path,
		FileSize:    1,
	})
	if err != nil {
		t.Fatalf("CreateMediaFile %q: %v", path, err)
	}
	return f.ID
}

// TestPurgeExpiredMissingFiles_Integration_CascadesAllGone — an item whose every
// file is in the delete set must be soft-deleted, and the files hard-deleted.
func TestPurgeExpiredMissingFiles_Integration_CascadesAllGone(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	lib := seedLibrary(ctx, t, q, "purge-cascade-"+uuid.New().String()[:8])
	item := seedMediaItem(ctx, t, q, lib, "All Files Missing")
	f1 := seedMediaFile(ctx, t, q, item, "/p/"+uuid.New().String()+"-a.mkv")
	f2 := seedMediaFile(ctx, t, q, item, "/p/"+uuid.New().String()+"-b.mkv")

	deleted, err := q.PurgeExpiredMissingFiles(ctx, gen.PurgeExpiredMissingFilesParams{
		FileIds: []uuid.UUID{f1, f2},
		ItemIds: []uuid.UUID{item},
	})
	if err != nil {
		t.Fatalf("PurgeExpiredMissingFiles: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	// Read deleted_at directly: GetMediaItem filters deleted_at IS NULL, so it
	// would (correctly) report no rows for a soft-deleted item — ambiguous proof.
	var softDeleted bool
	if err := pool.QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM media_items WHERE id = $1", item).Scan(&softDeleted); err != nil {
		t.Fatalf("read deleted_at: %v", err)
	}
	if !softDeleted {
		t.Error("item with all files deleted should be soft-deleted (deleted_at set)")
	}
	// And both files must be gone.
	files, err := q.ListMediaFilesForItem(ctx, item)
	if err != nil {
		t.Fatalf("ListMediaFilesForItem: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files after purge, got %d", len(files))
	}
}

// TestPurgeExpiredMissingFiles_Integration_KeepsItemWithSurvivor — the snapshot
// gotcha guard: an item that still has a file OUTSIDE the delete set must NOT be
// soft-deleted, even though one of its files is being purged. A bare NOT EXISTS
// (against the unchanged snapshot) would wrongly conclude the item still has all
// its files and never cascade; the <> ALL(@file_ids) form must see the survivor.
func TestPurgeExpiredMissingFiles_Integration_KeepsItemWithSurvivor(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	lib := seedLibrary(ctx, t, q, "purge-survivor-"+uuid.New().String()[:8])
	item := seedMediaItem(ctx, t, q, lib, "One File Survives")
	doomed := seedMediaFile(ctx, t, q, item, "/p/"+uuid.New().String()+"-doomed.mkv")
	survivor := seedMediaFile(ctx, t, q, item, "/p/"+uuid.New().String()+"-survivor.mkv")

	deleted, err := q.PurgeExpiredMissingFiles(ctx, gen.PurgeExpiredMissingFilesParams{
		FileIds: []uuid.UUID{doomed},
		ItemIds: []uuid.UUID{item},
	})
	if err != nil {
		t.Fatalf("PurgeExpiredMissingFiles: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	got, err := q.GetMediaItem(ctx, item)
	if err != nil {
		t.Fatalf("GetMediaItem: %v", err)
	}
	if got.DeletedAt.Valid {
		t.Error("item with a surviving file must NOT be soft-deleted")
	}
	// And the survivor must still be attached to the item.
	files, err := q.ListMediaFilesForItem(ctx, item)
	if err != nil {
		t.Fatalf("ListMediaFilesForItem: %v", err)
	}
	if len(files) != 1 || files[0].ID != survivor {
		t.Errorf("expected exactly the survivor file to remain, got %d files", len(files))
	}
}
