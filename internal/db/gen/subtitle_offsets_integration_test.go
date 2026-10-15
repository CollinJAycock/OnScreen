//go:build integration

package gen_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestSubtitleOffsets_Integration covers migration 00055: an external
// subtitle's offset is stored on its row, survives a re-download of the same
// release and is range-checked; embedded-stream offsets upsert, list per
// file, delete, and go with their file.
//
// Run with: go test -tags 'dev integration' -run SubtitleOffsets ./internal/db/gen/
func TestSubtitleOffsets_Integration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := gen.New(pool)

	var lib, item, file uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('M', 'movie', '{/m}') RETURNING id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_items (library_id, type, title, sort_title) VALUES ($1, 'movie', 'X', 'X') RETURNING id`, lib).Scan(&item); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_files (media_item_id, file_path, file_size) VALUES ($1, '/m/X.mkv', 1) RETURNING id`, item).Scan(&file); err != nil {
		t.Fatal(err)
	}

	src := "os"
	sid := "123"
	ins := gen.InsertExternalSubtitleParams{FileID: file, Language: "en", Source: src, SourceID: &sid, StoragePath: "/c/a.vtt"}
	sub, err := q.InsertExternalSubtitle(ctx, ins)
	if err != nil {
		t.Fatal(err)
	}
	if sub.OffsetMs != 0 {
		t.Errorf("default offset = %d", sub.OffsetMs)
	}
	sub, err = q.SetExternalSubtitleOffset(ctx, gen.SetExternalSubtitleOffsetParams{ID: sub.ID, OffsetMs: -2500})
	if err != nil || sub.OffsetMs != -2500 {
		t.Fatalf("set: %v %d", err, sub.OffsetMs)
	}
	// Re-downloading the same release keeps the correction.
	ins.StoragePath = "/c/b.vtt"
	again, err := q.InsertExternalSubtitle(ctx, ins)
	if err != nil || again.ID != sub.ID || again.OffsetMs != -2500 {
		t.Errorf("re-download: %v id %v offset %d", err, again.ID, again.OffsetMs)
	}
	if _, err := q.SetExternalSubtitleOffset(ctx, gen.SetExternalSubtitleOffsetParams{ID: sub.ID, OffsetMs: 600001}); err == nil {
		t.Error("an offset past 10 minutes was stored")
	}

	off, err := q.GetSubtitleStreamOffset(ctx, gen.GetSubtitleStreamOffsetParams{FileID: file, StreamIndex: 2})
	if err != nil || off != 0 {
		t.Fatalf("unset stream: %v %d", err, off)
	}
	for _, v := range []int32{900, 1200} {
		if err := q.UpsertSubtitleStreamOffset(ctx, gen.UpsertSubtitleStreamOffsetParams{FileID: file, StreamIndex: 2, OffsetMs: v}); err != nil {
			t.Fatal(err)
		}
	}
	if off, _ := q.GetSubtitleStreamOffset(ctx, gen.GetSubtitleStreamOffsetParams{FileID: file, StreamIndex: 2}); off != 1200 {
		t.Errorf("after upserts = %d", off)
	}
	if err := q.UpsertSubtitleStreamOffset(ctx, gen.UpsertSubtitleStreamOffsetParams{FileID: file, StreamIndex: 3, OffsetMs: 0}); err == nil {
		t.Error("a zero stream offset was stored (the table is sparse)")
	}
	rows, err := q.ListSubtitleStreamOffsets(ctx, []uuid.UUID{file, uuid.New()})
	if err != nil || len(rows) != 1 || rows[0].StreamIndex != 2 || rows[0].OffsetMs != 1200 {
		t.Errorf("list: %v %+v", err, rows)
	}
	if err := q.DeleteSubtitleStreamOffset(ctx, gen.DeleteSubtitleStreamOffsetParams{FileID: file, StreamIndex: 2}); err != nil {
		t.Fatal(err)
	}
	if off, _ := q.GetSubtitleStreamOffset(ctx, gen.GetSubtitleStreamOffsetParams{FileID: file, StreamIndex: 2}); off != 0 {
		t.Errorf("after delete = %d", off)
	}

	// The offsets go with the file.
	if err := q.UpsertSubtitleStreamOffset(ctx, gen.UpsertSubtitleStreamOffsetParams{FileID: file, StreamIndex: 4, OffsetMs: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM media_files WHERE id = $1`, file); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM subtitle_stream_offsets`).Scan(&n); err != nil || n != 0 {
		t.Errorf("offsets left after the file was deleted: %d (%v)", n, err)
	}
}
