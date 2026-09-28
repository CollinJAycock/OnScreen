//go:build integration

// Track numbers healed by a rescan, against a real Postgres: the production
// media adapter, media service and scanner, scanning FLAC files on disk.
//
// Tracks imported before the scanner could read Vorbis "02/12" numbers (or
// fall back to the "03 - Title" filename number) have index NULL, and their
// unchanged files take the mtime fast skip on every later scan. The next scan
// must give them their number without treating the file as changed, and disc
// numbers must keep a multi-disc album's disc 2 track 1 its own track.
//
// Run with: go test -tags=integration ./cmd/server/ -run TrackPosition
package main

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/scanner"
	"github.com/onscreen/onscreen/internal/testdb"
)

type trackTestConc struct{}

func (trackTestConc) ScanFileConcurrency() int    { return 2 }
func (trackTestConc) ScanLibraryConcurrency() int { return 1 }

// writeVorbisFLAC writes the smallest file dhowden/tag reads as FLAC: the
// "fLaC" marker and one (last) VORBIS_COMMENT block.
func writeVorbisFLAC(t *testing.T, path string, comments ...string) {
	t.Helper()
	le32 := func(n int) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, uint32(n)); return b }
	vendor := "onscreen-test"
	body := append(le32(len(vendor)), vendor...)
	body = append(body, le32(len(comments))...)
	for _, c := range comments {
		body = append(append(body, le32(len(c))...), c...)
	}
	data := append([]byte("fLaC"), 0x80|4, byte(len(body)>>16), byte(len(body)>>8), byte(len(body)))
	data = append(data, body...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Written well before the scan records scanned_at, so an unchanged
	// file takes the mtime fast skip on the next scan.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
}

type trackRow struct {
	itemID, parentID uuid.UUID
	index, disc      *int32
	scannedAt        time.Time
}

func TestTrackPosition_Integration_RescanHealsUnnumberedTracks(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := &mediaAdapter{q: q}
	sc := scanner.New(media.NewService(adapter, adapter, logger), nil, trackTestConc{}, logger)

	root := t.TempDir()
	file := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	// Number only in the filename.
	writeVorbisFLAC(t, file("Artist/Album/03 - Third.flac"),
		"TITLE=Third", "ARTIST=Artist", "ALBUM=Album")
	// Number only in an "N/M" Vorbis tag.
	writeVorbisFLAC(t, file("Artist/Album/Second.flac"),
		"TITLE=Second", "ARTIST=Artist", "ALBUM=Album", "TRACKNUMBER=02/12", "DISCNUMBER=1/1")
	// Two discs, each opening with track 1.
	writeVorbisFLAC(t, file("Artist/Box/CD1/01 - Alpha.flac"),
		"TITLE=Alpha", "ARTIST=Artist", "ALBUM=Box", "TRACKNUMBER=1", "DISCNUMBER=1/2")
	writeVorbisFLAC(t, file("Artist/Box/CD2/01 - Beta.flac"),
		"TITLE=Beta", "ARTIST=Artist", "ALBUM=Box", "TRACKNUMBER=1", "DISCNUMBER=2/2")
	// Two discs in one folder: the filenames don't say which disc.
	writeVorbisFLAC(t, file("Artist/Split/01 - One.flac"),
		"TITLE=One", "ARTIST=Artist", "ALBUM=Split", "TRACKNUMBER=01/5", "DISCNUMBER=1/2")
	writeVorbisFLAC(t, file("Artist/Split/01 - Uno.flac"),
		"TITLE=Uno", "ARTIST=Artist", "ALBUM=Split", "TRACKNUMBER=01/5", "DISCNUMBER=2/2")

	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name: "Music", Type: "music", ScanPaths: []string{root},
		Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	scan := func(label string) {
		t.Helper()
		if _, err := sc.ScanLibrary(ctx, lib.ID, "music", []string{root}); err != nil {
			t.Fatalf("%s: scan: %v", label, err)
		}
	}
	tracks := func(label string) map[string]trackRow {
		t.Helper()
		rows, err := pool.Query(ctx, `
			SELECT mi.title, mi.id, mi.parent_id, mi.index, mi.disc_number, mf.scanned_at
			FROM media_items mi JOIN media_files mf ON mf.media_item_id = mi.id
			WHERE mi.library_id = $1 AND mi.type = 'track' AND mi.deleted_at IS NULL`, lib.ID)
		if err != nil {
			t.Fatalf("%s: query tracks: %v", label, err)
		}
		defer rows.Close()
		out := map[string]trackRow{}
		for rows.Next() {
			var title string
			var r trackRow
			var parent pgtype.UUID
			if err := rows.Scan(&title, &r.itemID, &parent, &r.index, &r.disc, &r.scannedAt); err != nil {
				t.Fatalf("%s: scan row: %v", label, err)
			}
			r.parentID = uuid.UUID(parent.Bytes)
			if _, dup := out[title]; dup {
				t.Fatalf("%s: %q has two files on one track row, or two rows", label, title)
			}
			out[title] = r
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("%s: rows: %v", label, err)
		}
		return out
	}
	pos := func(r trackRow) (disc, index int32) {
		disc, index = -1, -1
		if r.disc != nil {
			disc = *r.disc
		}
		if r.index != nil {
			index = *r.index
		}
		return
	}
	expect := func(label string, got map[string]trackRow, title string, disc, index int32) {
		t.Helper()
		r, ok := got[title]
		if !ok {
			t.Fatalf("%s: no track %q (have %d tracks)", label, title, len(got))
		}
		if d, i := pos(r); d != disc || i != index {
			t.Errorf("%s: %q at disc %d track %d, want disc %d track %d (-1 = NULL)", label, title, d, i, disc, index)
		}
	}

	// 1. Import. Numbers come from the "N/M" tags, the filename, and the
	// disc tags; each album's two track 1s are two tracks.
	scan("import")
	got := tracks("import")
	if len(got) != 6 {
		t.Fatalf("import: %d tracks, want 6", len(got))
	}
	expect("import", got, "Third", -1, 3)
	expect("import", got, "Second", 1, 2)
	expect("import", got, "Alpha", 1, 1)
	expect("import", got, "Beta", 2, 1)
	expect("import", got, "One", 1, 1)
	expect("import", got, "Uno", 2, 1)

	// Album children come back in (disc, track) order.
	box := got["Alpha"].parentID
	kids, err := q.ListMediaItemChildren(ctx, pgtype.UUID{Bytes: box, Valid: true})
	if err != nil {
		t.Fatalf("list box tracks: %v", err)
	}
	if len(kids) != 2 || kids[0].Title != "Alpha" || kids[1].Title != "Beta" {
		t.Errorf("box order = %v, want [Alpha Beta]", titles(kids))
	}

	// 2. Put tracks back the way an older scanner left them: no track
	// number, no disc — and, for Split, an album that never learned its disc
	// count (the "1/2" DISCNUMBER wasn't readable either).
	if _, err := pool.Exec(ctx, `UPDATE media_items SET index = NULL, disc_number = NULL
		WHERE library_id = $1 AND title IN ('Third', 'Second', 'Uno')`, lib.ID); err != nil {
		t.Fatalf("clear numbers: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_items SET disc_total = NULL
		WHERE library_id = $1 AND type = 'album' AND title = 'Split'`, lib.ID); err != nil {
		t.Fatalf("clear disc total: %v", err)
	}
	before := tracks("cleared")
	expect("cleared", before, "Third", -1, -1)
	expect("cleared", before, "Second", -1, -1)
	expect("cleared", before, "Uno", -1, -1)

	// 3. Rescan, no file changed: the numbers come back, on the same rows,
	// and the file rows are untouched — the fast skip held (a slow-path
	// upsert would have moved scanned_at).
	scan("rescan")
	after := tracks("rescan")
	expect("rescan", after, "Third", -1, 3) // from "03 - Third.flac"
	expect("rescan", after, "Second", 1, 2) // from TRACKNUMBER=02/12, DISCNUMBER=1/1
	// "01 - Uno.flac" reads as disc 1 track 1, which One holds: the unique
	// position index refuses it and the tags place Uno on disc 2.
	expect("rescan", after, "Uno", 2, 1)
	expect("rescan", after, "One", 1, 1)
	for _, title := range []string{"Third", "Second", "Uno"} {
		if after[title].itemID != before[title].itemID {
			t.Errorf("rescan: %q moved to a new track row", title)
		}
		if !after[title].scannedAt.Equal(before[title].scannedAt) {
			t.Errorf("rescan: %q file was re-processed (scanned_at %v → %v)", title,
				before[title].scannedAt, after[title].scannedAt)
		}
	}
	album := after["Third"].parentID
	kids, err = q.ListMediaItemChildren(ctx, pgtype.UUID{Bytes: album, Valid: true})
	if err != nil {
		t.Fatalf("list album tracks: %v", err)
	}
	if len(kids) != 2 || kids[0].Title != "Second" || kids[1].Title != "Third" {
		t.Errorf("album order = %v, want [Second Third]", titles(kids))
	}
	split := after["One"].parentID
	kids, err = q.ListMediaItemChildren(ctx, pgtype.UUID{Bytes: split, Valid: true})
	if err != nil {
		t.Fatalf("list split tracks: %v", err)
	}
	if len(kids) != 2 || kids[0].Title != "One" || kids[1].Title != "Uno" {
		t.Errorf("split order = %v, want [One Uno]", titles(kids))
	}

	// 4. What an older scanner did to Box: disc 2 track 1 matched disc 1
	// track 1 by number, so Beta's file hangs off Alpha's row, Beta's own row
	// was cleaned up as empty, and nothing has a disc. Once Beta's file
	// changes, the rescan must give it its own row back.
	alpha, beta := got["Alpha"].itemID, got["Beta"].itemID
	for _, stmt := range []string{
		`UPDATE media_files SET media_item_id = $1 WHERE media_item_id = $2`,
		`UPDATE media_items SET deleted_at = NOW() WHERE id = $2 AND $1 = $1`,
		`UPDATE media_items SET disc_number = NULL WHERE id = $1 AND $2 = $2`,
	} {
		if _, err := pool.Exec(ctx, stmt, alpha, beta); err != nil {
			t.Fatalf("collapse Box: %v", err)
		}
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(file("Artist/Box/CD2/01 - Beta.flac"), future, future); err != nil {
		t.Fatal(err)
	}
	scan("collapsed rescan")
	final := tracks("collapsed rescan") // fails if Alpha's row still has both files
	expect("collapsed rescan", final, "Beta", 2, 1)
	expect("collapsed rescan", final, "Alpha", -1, 1) // unchanged file: disc stays unknown
	if final["Alpha"].itemID != alpha || final["Beta"].itemID == alpha {
		t.Error("collapsed rescan: Beta's file is still on Alpha's row")
	}
	kids, err = q.ListMediaItemChildren(ctx, pgtype.UUID{Bytes: box, Valid: true})
	if err != nil {
		t.Fatalf("list box tracks: %v", err)
	}
	if len(kids) != 2 || kids[0].Title != "Alpha" || kids[1].Title != "Beta" {
		t.Errorf("box order after collapsed rescan = %v, want [Alpha Beta]", titles(kids))
	}
}

func titles(rows []gen.ListMediaItemChildrenRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Title
	}
	return out
}
