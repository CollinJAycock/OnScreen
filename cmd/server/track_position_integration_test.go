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
	"github.com/jackc/pgx/v5/pgxpool"

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
	// Alpha's file is unchanged, but its track held two files when the scan
	// started, so it is read again too and the row gains its disc.
	expect("collapsed rescan", final, "Alpha", 1, 1)
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

// fileConc sets a scan's file concurrency. At 1 the files are processed one
// at a time in walk (lexical) order.
type fileConc int

func (c fileConc) ScanFileConcurrency() int  { return int(c) }
func (fileConc) ScanLibraryConcurrency() int { return 1 }

// trackFile is one active music file and the track row holding it.
type trackFile struct {
	itemID      uuid.UUID
	parentID    uuid.UUID
	title       string
	index, disc *int32
	scannedAt   time.Time
	files       int64 // active files on that track
}

func (f trackFile) pos() (disc, index int32) {
	disc, index = -1, -1
	if f.disc != nil {
		disc = *f.disc
	}
	if f.index != nil {
		index = *f.index
	}
	return
}

// trackFiles returns the library's active track files by file name.
func trackFiles(t *testing.T, pool *pgxpool.Pool, libID uuid.UUID, label string) map[string]trackFile {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT mf.file_path, mi.id, mi.parent_id, mi.title, mi.index, mi.disc_number, mf.scanned_at,
		       (SELECT count(*) FROM media_files o WHERE o.media_item_id = mi.id AND o.status = 'active')
		FROM media_files mf JOIN media_items mi ON mi.id = mf.media_item_id
		WHERE mi.library_id = $1 AND mi.type = 'track' AND mi.deleted_at IS NULL
		  AND mf.status = 'active'`, libID)
	if err != nil {
		t.Fatalf("%s: query track files: %v", label, err)
	}
	defer rows.Close()
	out := map[string]trackFile{}
	for rows.Next() {
		var path string
		var f trackFile
		var parent pgtype.UUID
		if err := rows.Scan(&path, &f.itemID, &parent, &f.title, &f.index, &f.disc, &f.scannedAt, &f.files); err != nil {
			t.Fatalf("%s: scan row: %v", label, err)
		}
		f.parentID = uuid.UUID(parent.Bytes)
		out[filepath.Base(path)] = f
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: rows: %v", label, err)
	}
	return out
}

// expectFile checks the track holding a file: its title, (disc, track) with
// -1 for NULL, and how many files it holds.
func expectFile(t *testing.T, label string, got map[string]trackFile, name, title string, disc, index int32, files int64) {
	t.Helper()
	f, ok := got[name]
	if !ok {
		t.Fatalf("%s: no file %q (have %d files)", label, name, len(got))
	}
	if d, i := f.pos(); f.title != title || d != disc || i != index || f.files != files {
		t.Errorf("%s: %q is on %q at disc %d track %d with %d files; want %q at disc %d track %d with %d",
			label, name, f.title, d, i, f.files, title, disc, index, files)
	}
}

// An album an older scanner folded: it matched disc 2's track 1 to disc 1's
// by number alone, so disc 2's file hangs off disc 1's row, disc 2 has no
// row, and no track has a disc. Nothing on disk changes. A plain rescan must
// split it — the folded track's files are read again because the track
// holds two — and leave the row disc 1's, titled as disc 1's song whichever
// title the fold left on it. A track that rightly holds two files (the same
// song twice, as FLAC and MP3 would be) stays as it is, and is read again
// only once per process.
func TestTrackPosition_Integration_RescanSplitsFoldedTracks(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := &mediaAdapter{q: q}

	for _, tc := range []struct {
		name      string
		files     int    // scan file concurrency
		foldTitle string // the title the fold left on disc 1's row
	}{
		{name: "fold titled by disc 1", files: 2, foldTitle: "Alpha"},
		// One file at a time, in walk order: Alpha's file (CD1) resolves the
		// row first, gives it disc 1, and retitles it.
		{name: "fold titled by disc 2", files: 1, foldTitle: "Beta"},
		// Both files resolve the disc-less row at once. Whichever fills in
		// its disc first keeps the row; the other file gets its own.
		{name: "fold titled by disc 2, files at once", files: 4, foldTitle: "Beta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := scanner.New(media.NewService(adapter, adapter, logger), nil, fileConc(tc.files), logger)
			root := t.TempDir()
			file := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
			box := func(comments ...string) []string {
				return append([]string{"ARTIST=Artist", "ALBUM=Box"}, comments...)
			}
			writeVorbisFLAC(t, file("Artist/Box/CD1/01 - Alpha.flac"), box("TITLE=Alpha", "TRACKNUMBER=1", "DISCNUMBER=1/2")...)
			writeVorbisFLAC(t, file("Artist/Box/CD1/02 - Gamma.flac"), box("TITLE=Gamma", "TRACKNUMBER=2", "DISCNUMBER=1/2")...)
			writeVorbisFLAC(t, file("Artist/Box/CD2/01 - Beta.flac"), box("TITLE=Beta", "TRACKNUMBER=1", "DISCNUMBER=2/2")...)
			for _, name := range []string{"01 - Song.flac", "01 - Song (copy).flac"} {
				writeVorbisFLAC(t, file("Artist/Dual/"+name),
					"TITLE=Song", "ARTIST=Artist", "ALBUM=Dual", "TRACKNUMBER=1")
			}

			lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
				Name: "Music " + tc.name, Type: "music", ScanPaths: []string{root},
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

			scan("import")
			got := trackFiles(t, pool, lib.ID, "import")
			expectFile(t, "import", got, "01 - Alpha.flac", "Alpha", 1, 1, 1)
			expectFile(t, "import", got, "02 - Gamma.flac", "Gamma", 1, 2, 1)
			expectFile(t, "import", got, "01 - Beta.flac", "Beta", 2, 1, 1)
			expectFile(t, "import", got, "01 - Song.flac", "Song", -1, 1, 2)
			if got["01 - Song.flac"].itemID != got["01 - Song (copy).flac"].itemID {
				t.Fatal("import: the two copies of Song are on two tracks")
			}

			// Fold Box the way an older scanner left it.
			alpha, beta := got["01 - Alpha.flac"].itemID, got["01 - Beta.flac"].itemID
			for _, stmt := range []struct {
				sql  string
				args []any
			}{
				{`UPDATE media_files SET media_item_id = $1 WHERE media_item_id = $2`, []any{alpha, beta}},
				{`DELETE FROM media_items WHERE id = $1`, []any{beta}},
				{`UPDATE media_items SET disc_number = NULL WHERE library_id = $1 AND type = 'track'`, []any{lib.ID}},
				{`UPDATE media_items SET title = $2, sort_title = lower($2) WHERE id = $1`, []any{alpha, tc.foldTitle}},
			} {
				if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
					t.Fatalf("fold Box: %v", err)
				}
			}
			folded := trackFiles(t, pool, lib.ID, "folded")
			expectFile(t, "folded", folded, "01 - Alpha.flac", tc.foldTitle, -1, 1, 2)
			expectFile(t, "folded", folded, "01 - Beta.flac", tc.foldTitle, -1, 1, 2)

			// 1. Rescan, no file touched: the fold splits.
			scan("rescan")
			after := trackFiles(t, pool, lib.ID, "rescan")
			expectFile(t, "rescan", after, "01 - Alpha.flac", "Alpha", 1, 1, 1)
			expectFile(t, "rescan", after, "01 - Beta.flac", "Beta", 2, 1, 1)
			if after["01 - Alpha.flac"].itemID == after["01 - Beta.flac"].itemID {
				t.Error("rescan: Alpha and Beta still share a track")
			}
			if tc.files == 1 && after["01 - Alpha.flac"].itemID != alpha {
				t.Error("rescan: disc 1's row was replaced rather than retitled")
			}
			if tc.foldTitle == "Alpha" && after["01 - Alpha.flac"].itemID != alpha {
				t.Error("rescan: Alpha's file left its own row")
			}
			// Gamma's track held only its file: skipped as before, disc
			// unknown as the fold left it.
			expectFile(t, "rescan", after, "02 - Gamma.flac", "Gamma", -1, 2, 1)
			if !after["02 - Gamma.flac"].scannedAt.Equal(folded["02 - Gamma.flac"].scannedAt) {
				t.Error("rescan: Gamma's unchanged file was re-processed")
			}
			// The two copies of Song are read again and stay together.
			expectFile(t, "rescan", after, "01 - Song.flac", "Song", -1, 1, 2)
			if after["01 - Song.flac"].itemID != after["01 - Song (copy).flac"].itemID {
				t.Error("rescan: the two copies of Song were split")
			}
			kids, err := q.ListMediaItemChildren(ctx, pgtype.UUID{Bytes: after["02 - Gamma.flac"].parentID, Valid: true})
			if err != nil {
				t.Fatalf("list box tracks: %v", err)
			}
			if names := titles(kids); len(names) != 3 || names[0] != "Alpha" || names[1] != "Gamma" || names[2] != "Beta" {
				t.Errorf("rescan: box order = %v, want [Alpha Gamma Beta]", names)
			}

			// 2. Rescan again: nothing left to split, and Song's copies were
			// read once this process — every file takes the fast skip.
			scan("second rescan")
			again := trackFiles(t, pool, lib.ID, "second rescan")
			for name, f := range after {
				if again[name].itemID != f.itemID {
					t.Errorf("second rescan: %q moved to another track", name)
				}
				if !again[name].scannedAt.Equal(f.scannedAt) {
					t.Errorf("second rescan: %q was re-processed", name)
				}
			}
		})
	}
}

// Two unnumbered tracks of an album that never learned its disc count, each
// "01 - …" by name, disc 2's sorting first. The name's number doesn't say
// which disc: trusted on its own, it put disc 2's Ace on disc 1's track 1,
// and disc 1's own track 1 then couldn't take its place. The rescan must
// read both files' tags (still without the slow path).
func TestTrackPosition_Integration_UnknownDiscCountReadsTags(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := &mediaAdapter{q: q}
	// One file at a time, in walk order: Ace's file before One's.
	sc := scanner.New(media.NewService(adapter, adapter, logger), nil, fileConc(1), logger)

	root := t.TempDir()
	pair := filepath.Join(root, "Artist", "Pair")
	writeVorbisFLAC(t, filepath.Join(pair, "01 - Ace.flac"),
		"TITLE=Ace", "ARTIST=Artist", "ALBUM=Pair", "TRACKNUMBER=1", "DISCNUMBER=2")
	writeVorbisFLAC(t, filepath.Join(pair, "01 - One.flac"),
		"TITLE=One", "ARTIST=Artist", "ALBUM=Pair", "TRACKNUMBER=1", "DISCNUMBER=1")

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

	scan("import")
	got := trackFiles(t, pool, lib.ID, "import")
	expectFile(t, "import", got, "01 - Ace.flac", "Ace", 2, 1, 1)
	expectFile(t, "import", got, "01 - One.flac", "One", 1, 1, 1)

	// As an older scanner left the album: no numbers, no discs, no count.
	for _, stmt := range []string{
		`UPDATE media_items SET index = NULL, disc_number = NULL WHERE library_id = $1 AND type = 'track'`,
		`UPDATE media_items SET disc_total = NULL WHERE library_id = $1 AND type = 'album'`,
	} {
		if _, err := pool.Exec(ctx, stmt, lib.ID); err != nil {
			t.Fatalf("clear positions: %v", err)
		}
	}

	scan("rescan")
	after := trackFiles(t, pool, lib.ID, "rescan")
	expectFile(t, "rescan", after, "01 - Ace.flac", "Ace", 2, 1, 1)
	expectFile(t, "rescan", after, "01 - One.flac", "One", 1, 1, 1)
	for name, f := range after {
		if f.itemID != got[name].itemID {
			t.Errorf("rescan: %q moved to a new track row", name)
		}
		if !f.scannedAt.Equal(got[name].scannedAt) {
			t.Errorf("rescan: %q file was re-processed", name)
		}
	}
}

func titles(rows []gen.ListMediaItemChildrenRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Title
	}
	return out
}
