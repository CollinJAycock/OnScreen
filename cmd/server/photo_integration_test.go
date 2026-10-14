//go:build integration

// Photo libraries against a real Postgres, through the production media
// adapter, media service and scanner (migration 00057):
//
//   - photo items are keyed by file: two folders' DSCF0001.JPG are two
//     photos;
//   - a RAW and its same-named JPEG pair into one photo showing the JPEG,
//     the RAW-only photo shows its embedded preview and carries its EXIF;
//   - turning pairing off splits the pair on the next scan (the JPEG keeps
//     its item), turning it back on pairs them again.
//
// Run with: go test -tags=integration ./cmd/server/ -run Integration_Photo
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/scanner"
	"github.com/onscreen/onscreen/internal/testdb"
)

func photoTestJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// photoTestRAF builds a minimal Fujifilm RAF whose preview JPEG carries
// EXIF Make and DateTimeOriginal.
func photoTestRAF(t *testing.T) []byte {
	t.Helper()
	be := binary.BigEndian
	// TIFF: IFD0 {Make, ExifIFD} → EXIF IFD {DateTimeOriginal}.
	tiff := []byte("MM\x00*")
	tiff = be.AppendUint32(tiff, 8)
	tiff = be.AppendUint16(tiff, 2)
	mk := "FUJIFILM\x00"
	makeOff := uint32(8 + 2 + 2*12 + 4)
	exifOff := makeOff + uint32(len(mk)) + 1 // keep even
	tiff = be.AppendUint16(tiff, 0x010F)
	tiff = be.AppendUint16(tiff, 2)
	tiff = be.AppendUint32(tiff, uint32(len(mk)))
	tiff = be.AppendUint32(tiff, makeOff)
	tiff = be.AppendUint16(tiff, 0x8769)
	tiff = be.AppendUint16(tiff, 4)
	tiff = be.AppendUint32(tiff, 1)
	tiff = be.AppendUint32(tiff, exifOff)
	tiff = be.AppendUint32(tiff, 0)
	tiff = append(tiff, mk...)
	tiff = append(tiff, 0)
	date := "2020:08:15 10:20:30\x00"
	dateOff := exifOff + 2 + 12 + 4
	tiff = be.AppendUint16(tiff, 1)
	tiff = be.AppendUint16(tiff, 0x9003)
	tiff = be.AppendUint16(tiff, 2)
	tiff = be.AppendUint32(tiff, uint32(len(date)))
	tiff = be.AppendUint32(tiff, dateOff)
	tiff = be.AppendUint32(tiff, 0)
	tiff = append(tiff, date...)

	app1 := append([]byte("Exif\x00\x00"), tiff...)
	preview := []byte{0xFF, 0xD8, 0xFF, 0xE1}
	preview = be.AppendUint16(preview, uint16(len(app1)+2))
	preview = append(preview, app1...)
	preview = append(preview, photoTestJPEG(t, 300, 200)[2:]...)

	hdr := make([]byte, 128)
	copy(hdr, "FUJIFILMCCD-RAW 0201")
	be.PutUint32(hdr[84:], uint32(len(hdr)))
	be.PutUint32(hdr[88:], uint32(len(preview)))
	return append(hdr, preview...)
}

func TestIntegration_Photo_RAWPairingAndFileKeyedItems(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := &mediaAdapter{q: q}
	newScanner := func() *scanner.Scanner {
		return scanner.New(media.NewService(adapter, adapter, logger), nil, trackTestConc{}, logger).
			WithPhotoStore(photoScanStore{q: q})
	}

	root := t.TempDir()
	write := func(rel string, data []byte) string {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("2023/DSCF0001.JPG", photoTestJPEG(t, 64, 48))
	write("2023/DSCF0001.RAF", photoTestRAF(t))
	write("2024/DSCF0001.JPG", photoTestJPEG(t, 64, 48))
	write("2024/DSCF0002.RAF", photoTestRAF(t))

	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name: "Photos-" + uuid.NewString()[:6], Type: "photo", ScanPaths: []string{root},
		Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	scan := func(label string) {
		t.Helper()
		if _, err := newScanner().ScanLibrary(ctx, lib.ID, "photo", []string{root}); err != nil {
			t.Fatalf("%s: scan: %v", label, err)
		}
	}
	// itemOf maps each library-relative file to its live item and returns
	// the items' posters.
	type state struct {
		itemOf map[string]uuid.UUID
		poster map[uuid.UUID]string
	}
	read := func(label string) state {
		t.Helper()
		rows, err := pool.Query(ctx, `
			SELECT mf.file_path, mi.id, COALESCE(mi.poster_path, '')
			FROM media_files mf JOIN media_items mi ON mi.id = mf.media_item_id
			WHERE mi.library_id = $1 AND mi.deleted_at IS NULL AND mf.status = 'active'`, lib.ID)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		defer rows.Close()
		st := state{itemOf: map[string]uuid.UUID{}, poster: map[uuid.UUID]string{}}
		for rows.Next() {
			var path, poster string
			var id uuid.UUID
			if err := rows.Scan(&path, &id, &poster); err != nil {
				t.Fatal(err)
			}
			rel, _ := filepath.Rel(root, path)
			st.itemOf[filepath.ToSlash(rel)] = id
			st.poster[id] = poster
		}
		return st
	}

	scan("import")
	st := read("import")
	if len(st.itemOf) != 4 || len(st.poster) != 3 {
		t.Fatalf("import: %d files on %d items, want 4 on 3: %v", len(st.itemOf), len(st.poster), st.itemOf)
	}
	pair := st.itemOf["2023/DSCF0001.JPG"]
	if st.itemOf["2023/DSCF0001.RAF"] != pair {
		t.Fatalf("import: the RAW and its JPEG are on different items")
	}
	if st.itemOf["2024/DSCF0001.JPG"] == pair {
		t.Fatalf("import: another folder's DSCF0001.JPG joined the pair")
	}
	if st.poster[pair] != "2023/DSCF0001.JPG" {
		t.Fatalf("import: pair poster = %q, want the JPEG", st.poster[pair])
	}
	rawOnly := st.itemOf["2024/DSCF0002.RAF"]
	if st.poster[rawOnly] != "2024/DSCF0002.RAF" {
		t.Fatalf("import: RAW-only poster = %q", st.poster[rawOnly])
	}
	pm, err := q.GetPhotoMetadata(ctx, rawOnly)
	if err != nil {
		t.Fatalf("import: RAW-only photo has no EXIF row: %v", err)
	}
	if pm.CameraMake == nil || *pm.CameraMake != "FUJIFILM" || !pm.TakenAt.Valid || pm.TakenAt.Time.Year() != 2020 {
		t.Fatalf("import: RAW EXIF make=%v taken=%v", pm.CameraMake, pm.TakenAt)
	}

	scan("rescan")
	if again := read("rescan"); len(again.poster) != 3 || again.itemOf["2023/DSCF0001.RAF"] != pair {
		t.Fatalf("rescan changed the grouping: %v", again.itemOf)
	}

	// Pairing off: the RAW leaves for an item of its own, the JPEG keeps
	// the pair's item (and with it albums and favourites).
	if _, err := q.UpsertLibraryPhotoSettings(ctx, gen.UpsertLibraryPhotoSettingsParams{LibraryID: lib.ID, RawJpegPairing: false}); err != nil {
		t.Fatal(err)
	}
	scan("pairing off")
	st = read("pairing off")
	if len(st.poster) != 4 {
		t.Fatalf("pairing off: %d items, want 4: %v", len(st.poster), st.itemOf)
	}
	if st.itemOf["2023/DSCF0001.JPG"] != pair || st.itemOf["2023/DSCF0001.RAF"] == pair {
		t.Fatalf("pairing off: wrong split: %v", st.itemOf)
	}
	if st.poster[st.itemOf["2023/DSCF0001.RAF"]] != "2023/DSCF0001.RAF" {
		t.Fatalf("pairing off: split RAW poster = %q", st.poster[st.itemOf["2023/DSCF0001.RAF"]])
	}

	// And back on: they pair again on the JPEG's item.
	if _, err := q.UpsertLibraryPhotoSettings(ctx, gen.UpsertLibraryPhotoSettingsParams{LibraryID: lib.ID, RawJpegPairing: true}); err != nil {
		t.Fatal(err)
	}
	scan("pairing on")
	st = read("pairing on")
	if len(st.poster) != 3 || st.itemOf["2023/DSCF0001.RAF"] != pair || st.poster[pair] != "2023/DSCF0001.JPG" {
		t.Fatalf("pairing on: %v posters %v", st.itemOf, st.poster)
	}
}
