//go:build integration

package trickplay

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/testdb"
)

// TestChapterThumbState_Integration walks chapter_thumb_count (migration
// 00051) through its life: a finished run whose file has chapters is a
// backfill candidate until thumbnails are recorded, the detail lookup sees
// them only on a 'done' row, and a new sprite run resets the count.
func TestChapterThumbState_Integration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	store := NewStore(pool)

	var lib, item, file, bare, bareFile uuid.UUID
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('m', 'movie', '{/m}') RETURNING id`).Scan(&lib))
	must(pool.QueryRow(ctx, `INSERT INTO media_items (library_id, type, title, sort_title) VALUES ($1, 'movie', 'With Chapters', 'w') RETURNING id`, lib).Scan(&item))
	must(pool.QueryRow(ctx, `INSERT INTO media_files (media_item_id, file_path, file_size, video_codec, duration_ms, chapters)
		VALUES ($1, '/m/a.mkv', 1, 'h264', 60000, '[{"title":"One","start_ms":0,"end_ms":30000},{"title":"Two","start_ms":30000,"end_ms":60000}]') RETURNING id`, item).Scan(&file))
	must(pool.QueryRow(ctx, `INSERT INTO media_items (library_id, type, title, sort_title) VALUES ($1, 'movie', 'No Chapters', 'n') RETURNING id`, lib).Scan(&bare))
	must(pool.QueryRow(ctx, `INSERT INTO media_files (media_item_id, file_path, file_size, video_codec, duration_ms)
		VALUES ($1, '/m/b.mkv', 1, 'h264', 60000) RETURNING id`, bare).Scan(&bareFile))

	for _, p := range []struct{ item, file uuid.UUID }{{item, file}, {bare, bareFile}} {
		must(store.UpsertPending(ctx, p.item, p.file, Default))
		must(store.MarkDone(ctx, p.item, 3))
	}

	cands, err := store.ListChapterThumbCandidates(ctx, 10)
	must(err)
	if len(cands) != 1 || cands[0] != item {
		t.Fatalf("candidates = %v, want only the item with chapters", cands)
	}
	if fid, n, err := store.ChapterThumbs(ctx, item); err != nil || fid != uuid.Nil || n != 0 {
		t.Fatalf("before generation: %v %d %v", fid, n, err)
	}

	must(store.SetChapterThumbs(ctx, item, 2))
	fid, n, err := store.ChapterThumbs(ctx, item)
	must(err)
	if fid != file || n != 2 {
		t.Fatalf("after generation: file %v n %d", fid, n)
	}
	if cands, _ := store.ListChapterThumbCandidates(ctx, 10); len(cands) != 0 {
		t.Errorf("generated item still a candidate: %v", cands)
	}

	// Regenerating the sprites wipes the directory: the count resets and the
	// detail lookup stops advertising thumbnails until the run finishes.
	must(store.UpsertPending(ctx, item, file, Default))
	if _, n, _ := store.ChapterThumbs(ctx, item); n != 0 {
		t.Errorf("pending run still advertises %d thumbs", n)
	}
	row, _, err := store.Status(ctx, item)
	must(err)
	if row.ChapterThumbCount != nil {
		t.Errorf("count not reset on a new run: %v", *row.ChapterThumbCount)
	}
}
