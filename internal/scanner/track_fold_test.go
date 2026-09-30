package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// An older scanner matched disc 2's track 1 to disc 1's by number and hung
// both files on one track. Their files are unchanged, so without a gate the
// fast skip returns before any tag is read and the fold never splits. A
// track holding several files when the scan starts sends each of its files
// down the slow path, once per process: a genuine second copy (FLAC + MP3 of
// one song) resolves back to the same track and must not pay on every scan.
func TestResolveUnchangedFile_FoldedTrackIsRereadOncePerFile(t *testing.T) {
	ctx := context.Background()
	libID := uuid.New()
	svc := newMockMediaService()
	poster := "poster.jpg" // enriched: nothing else surfaces the item
	album := &media.Item{ID: uuid.New(), Type: "album", Title: "Box", PosterPath: &poster}
	folded := &media.Item{ID: uuid.New(), Type: "track", Title: "Alpha", ParentID: &album.ID,
		Index: ip(1), PosterPath: &poster}
	single := &media.Item{ID: uuid.New(), Type: "track", Title: "Gamma", ParentID: &album.ID,
		Index: ip(2), PosterPath: &poster}
	for _, it := range []*media.Item{album, folded, single} {
		svc.items[it.ID] = it
	}
	alphaFile := &media.File{ID: uuid.New(), MediaItemID: folded.ID, Status: "active"}
	betaFile := &media.File{ID: uuid.New(), MediaItemID: folded.ID, Status: "active"}
	gammaFile := &media.File{ID: uuid.New(), MediaItemID: single.ID, Status: "active"}
	svc.foldedTracks = map[uuid.UUID][]uuid.UUID{libID: {folded.ID}}

	s := newTestScanner(svc)
	skip := func(s *Scanner, lib uuid.UUID, libType string, f *media.File) bool {
		t.Helper()
		_, _, skip := s.resolveUnchangedFile(ctx, lib, libType, "/music/Box/file.flac", f)
		return skip
	}

	// Before any music scan loaded the snapshot, nothing is re-read.
	if !skip(s, libID, "music", alphaFile) {
		t.Fatal("re-read a file before its library's snapshot was loaded")
	}
	s.loadFoldedTracks(ctx, libID)
	if skip(s, libID, "music", alphaFile) {
		t.Error("first file of a folded track was skipped")
	}
	if !skip(s, libID, "music", alphaFile) {
		t.Error("the same file was re-read twice in one process")
	}
	if skip(s, libID, "music", betaFile) {
		t.Error("second file of a folded track was skipped")
	}
	if !skip(s, libID, "music", gammaFile) {
		t.Error("a track holding one file was re-read")
	}
	if !skip(s, uuid.New(), "music", betaFile) {
		t.Error("another library's snapshot sent the file down the slow path")
	}

	// Once split, the next scan's snapshot no longer lists the track.
	svc.foldedTracks = nil
	fresh := newTestScanner(svc)
	fresh.loadFoldedTracks(ctx, libID)
	if !skip(fresh, libID, "music", alphaFile) {
		t.Error("a split track's file was re-read")
	}
}

// The re-read file really takes the slow path: its tags are read, its own
// (disc, track) is looked up, and the file row is pointed at that track.
func TestProcessFile_FoldedTrackFileMovesToItsOwnTrack(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Artist", "Box", "CD2", "01 - Beta.flac")
	writeTaggedFLAC(t, path, "TITLE=Beta", "ARTIST=Artist", "ALBUM=Box", "TRACKNUMBER=1", "DISCNUMBER=2/2")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	libID := uuid.New()
	svc := newMockMediaService()
	poster := "poster.jpg"
	alpha := &media.Item{ID: uuid.New(), Type: "track", Title: "Alpha", Index: ip(1), PosterPath: &poster}
	svc.items[alpha.ID] = alpha
	hash := "unchanged"
	svc.fileByPath[path] = &media.File{ID: uuid.New(), MediaItemID: alpha.ID, FilePath: path,
		FileSize: info.Size(), FileHash: &hash, Status: "active", ScannedAt: time.Now()}
	svc.foldedTracks = map[uuid.UUID][]uuid.UUID{libID: {alpha.ID}}

	s := newTestScanner(svc)
	s.loadFoldedTracks(context.Background(), libID)
	if _, _, _, err := s.processFile(context.Background(), libID, "music", path, []string{root}); err != nil {
		t.Fatalf("processFile: %v", err)
	}
	if len(svc.fileCalls) != 1 {
		t.Fatalf("file upserts = %d, want 1 (the slow path)", len(svc.fileCalls))
	}
	last := svc.hierarchyCalls[len(svc.hierarchyCalls)-1]
	if last.Type != "track" || !samePos(last.Index, ip(1)) || !samePos(last.DiscNumber, ip(2)) {
		t.Errorf("track lookup = %s %s, want disc 2 track 1", last.Type, fmtPos(last.DiscNumber, last.Index))
	}
	if svc.fileCalls[0].MediaItemID == alpha.ID {
		t.Error("the file stayed on the folded track")
	}
}

// Both files of a folded track can resolve the disc-less row at once. The
// first to fill its disc owns the row; the other must look again rather
// than stay on the other disc's track.
func TestProcessMusicHierarchy_FoldedRowTakenByOtherDisc(t *testing.T) {
	for _, tc := range []struct {
		name       string
		storedDisc *int // the row's disc in the database when the file fills
		wantSame   bool
	}{
		{name: "the other disc filled first: look again", storedDisc: ip(1), wantSame: false},
		{name: "this file fills the disc: keep the row", storedDisc: nil, wantSame: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "Artist", "Box", "01 - Beta.flac")
			writeTaggedFLAC(t, path, "TITLE=Beta", "ARTIST=Artist", "ALBUM=Box", "TRACKNUMBER=1", "DISCNUMBER=2")

			svc := newMockMediaService()
			row := uuid.New()
			svc.items[row] = &media.Item{ID: row, Type: "track", Title: "Beta", Index: ip(1), DiscNumber: tc.storedDisc}
			finds := 0
			svc.hierarchyFind = func(p media.CreateItemParams) *media.Item {
				if p.Type != "track" {
					return nil
				}
				finds++
				if finds > 1 {
					return nil // the row's disc now rules it out: create
				}
				// As read before either fill: no disc yet.
				return &media.Item{ID: row, Type: "track", Title: "Beta", Index: ip(1)}
			}

			got, _, err := newTestScanner(svc).processMusicHierarchy(context.Background(), uuid.New(), path, []string{root})
			if err != nil {
				t.Fatalf("processMusicHierarchy: %v", err)
			}
			if (got.ID == row) != tc.wantSame {
				t.Errorf("resolved to the folded row = %v, want %v", got.ID == row, tc.wantSame)
			}
			if !samePos(got.DiscNumber, ip(2)) || !samePos(got.Index, ip(1)) {
				t.Errorf("track position = %s, want disc 2 track 1", fmtPos(got.DiscNumber, got.Index))
			}
			if len(svc.titleCalls) != 0 {
				t.Errorf("retitled %v; the file's title is already the row's", svc.titleCalls)
			}
		})
	}
}

// A folded row can carry the other disc's title. Once the file whose
// (disc, track) it is resolves to it, the row takes that file's title.
func TestProcessMusicHierarchy_RetitlesTrackAtItsPosition(t *testing.T) {
	for _, tc := range []struct {
		name        string
		stored      string // the row's title
		index, disc *int   // the row's position
		fillRefused bool   // the row's disc can't be filled in
		notFolded   bool   // the row held one file when the scan started
		comments    []string
		want        string // "" = no retitle
	}{
		{name: "folded row titled by the other disc", stored: "Beta", index: ip(1), disc: ip(1),
			comments: []string{"TITLE=Alpha", "TRACKNUMBER=1", "DISCNUMBER=1/2"}, want: "Alpha"},
		{name: "disc filled in by this file", stored: "Beta", index: ip(1),
			comments: []string{"TITLE=Alpha", "TRACKNUMBER=1", "DISCNUMBER=1/2"}, want: "Alpha"},
		{name: "no disc tag, no stored disc", stored: "Beta", index: ip(1),
			comments: []string{"TITLE=Alpha", "TRACKNUMBER=1"}, want: "Alpha"},
		{name: "other scripts compare by their letters", stored: "東京", index: ip(1), disc: ip(1),
			comments: []string{"TITLE=大阪", "TRACKNUMBER=1", "DISCNUMBER=1"}, want: "大阪"},
		{name: "same title once folded", stored: "the alpha!", index: ip(1), disc: ip(1),
			comments: []string{"TITLE=Alpha", "TRACKNUMBER=1", "DISCNUMBER=1"}},
		{name: "row at another number", stored: "Beta", index: ip(2), disc: ip(1),
			comments: []string{"TITLE=Alpha", "TRACKNUMBER=1", "DISCNUMBER=1"}},
		{name: "row on another disc", stored: "Beta", index: ip(1), disc: ip(2),
			comments: []string{"TITLE=Alpha", "TRACKNUMBER=1", "DISCNUMBER=1"}},
		{name: "row's disc unknown", stored: "Beta", index: ip(1), fillRefused: true,
			comments: []string{"TITLE=Alpha", "TRACKNUMBER=1", "DISCNUMBER=1"}},
		{name: "no number in the tags", stored: "Beta", index: ip(1), disc: ip(1),
			comments: []string{"TITLE=Alpha"}},
		// A title set in the metadata editor survives a re-read of its file.
		{name: "a row that wasn't folded keeps its title", stored: "My Title", index: ip(1), disc: ip(1),
			notFolded: true, comments: []string{"TITLE=Alpha", "TRACKNUMBER=1", "DISCNUMBER=1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "Artist", "Box", "Song.flac")
			writeTaggedFLAC(t, path, append([]string{"ARTIST=Artist", "ALBUM=Box"}, tc.comments...)...)

			svc := newMockMediaService()
			if tc.fillRefused {
				svc.fillErr = func(_, _ *int) error { return os.ErrExist }
			}
			existing := &media.Item{ID: uuid.New(), Type: "track", Title: tc.stored, Index: tc.index, DiscNumber: tc.disc}
			svc.items[existing.ID] = existing
			svc.hierarchyFind = func(p media.CreateItemParams) *media.Item {
				if p.Type == "track" {
					return existing
				}
				return nil
			}

			sc := newTestScanner(svc)
			libraryID := uuid.New()
			if !tc.notFolded {
				sc.foldedTracks.Store(libraryID, &trackSet{ids: map[uuid.UUID]struct{}{existing.ID: {}}})
			}
			got, _, err := sc.processMusicHierarchy(context.Background(), libraryID, path, []string{root})
			if err != nil {
				t.Fatalf("processMusicHierarchy: %v", err)
			}
			if tc.want == "" {
				if len(svc.titleCalls) != 0 {
					t.Errorf("retitled to %v, want no change", svc.titleCalls)
				}
				if got.Title != tc.stored {
					t.Errorf("title = %q, want %q", got.Title, tc.stored)
				}
				return
			}
			if len(svc.titleCalls) != 1 || svc.titleCalls[0] != tc.want {
				t.Errorf("retitles = %v, want [%s]", svc.titleCalls, tc.want)
			}
			// The caller writes the duration with the returned title.
			if got.Title != tc.want || got.SortTitle != sortTitle(tc.want) {
				t.Errorf("returned title = %q / %q, want %q", got.Title, got.SortTitle, tc.want)
			}
		})
	}
}
