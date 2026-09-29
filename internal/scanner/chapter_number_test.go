package scanner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

func TestChapterNumber(t *testing.T) {
	tests := []struct {
		name     string
		tagTrack int
		want     int
		wantOK   bool
	}{
		{"01 Chapter One.mp3", 0, 1, true},
		{"001 - Prologue.m4a", 0, 1, true},
		{"7.Seven.mp3", 0, 7, true},
		{"12_Twelve.mp3", 0, 12, true},
		{"05.mp3", 0, 5, true},
		{"03 Three.mp3", 9, 3, true},       // the name wins over the tag
		{"Chapter 05.mp3", 5, 5, true},     // no leading number: the tag
		{"Chapter 05.mp3", 0, 0, false},    // neither
		{"1984 - Part 1.mp3", 0, 0, false}, // a year, not a chapter
		{"1984 - Part 1.mp3", 2, 2, true},  // ... so the tag decides
		{"00-Prologue.mp3", 0, 0, true},    // before chapter 1, so it sorts first
		{"000 Credits.mp3", 7, 0, true},    // the name's zero wins over a tag too
		{"4Tracks Mix.mp3", 0, 0, false},   // a number glued to a word isn't one
		{"  02 Spaced.mp3", 0, 2, true},
	}
	for _, tc := range tests {
		got, ok := chapterNumber(filepath.Join("root", "Author", "Book", tc.name), tc.tagTrack)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("chapterNumber(%q, %d) = %d, %v; want %d, %v", tc.name, tc.tagTrack, got, ok, tc.want, tc.wantOK)
		}
	}
}

// refusingTransport fails every request, keeping cover-art lookups offline.
type refusingTransport struct{}

func (refusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline in tests")
}

// A new multi-file chapter gets its number after being matched by title:
// the name's number, else the tag's. The number never takes part in the
// match, and one another chapter holds is left off without failing the file.
func TestProcessAudiobook_NumbersChapters(t *testing.T) {
	prev := externalArtHTTPClient
	externalArtHTTPClient = &http.Client{Transport: refusingTransport{}}
	t.Cleanup(func() { externalArtHTTPClient = prev })

	tests := []struct {
		file     string
		comments []string
		fillErr  bool
		want     *int
	}{
		{file: "03 Three.flac", comments: []string{"TRACKNUMBER=9"}, want: ip(3)},
		{file: "Intro.flac", comments: []string{"TRACKNUMBER=1/20"}, want: ip(1)},
		{file: "Outro.flac", want: nil},
		{file: "04 Four.flac", fillErr: true, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "Author", "Book", tc.file)
			writeTaggedFLAC(t, path, append([]string{"TITLE=" + tc.file}, tc.comments...)...)

			svc := newMockMediaService()
			poster := "poster.jpg"
			book := &media.Item{ID: uuid.New(), Type: "audiobook", Title: "Book", PosterPath: &poster}
			svc.items[book.ID] = book
			svc.hierarchyFind = func(p media.CreateItemParams) *media.Item {
				if p.Type == "audiobook" {
					return book
				}
				return nil
			}
			if tc.fillErr {
				svc.fillErr = func(_, _ *int) error { return errors.New("duplicate key value violates unique constraint") }
			}

			chapter, err := newTestScanner(svc).processAudiobook(context.Background(), uuid.New(), path, []string{root})
			if err != nil {
				t.Fatalf("processAudiobook: %v", err)
			}
			if chapter.Type != "audiobook_chapter" || chapter.ParentID == nil || *chapter.ParentID != book.ID {
				t.Fatalf("got %+v; want a chapter of the book", chapter)
			}
			if !samePos(chapter.Index, tc.want) {
				t.Errorf("chapter index = %v, want %v", deref(chapter.Index), deref(tc.want))
			}
			for _, p := range svc.hierarchyCalls {
				if p.Type == "audiobook_chapter" && p.Index != nil {
					t.Errorf("the chapter was matched by number %d; want title only", *p.Index)
				}
			}
		})
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// A chapter already in a library without a number (imported before
// chapters were numbered) gets one on the fast skip, reading the file only
// when its name has no number, and only once per process when nothing
// yields one.
func TestProcessFile_UnchangedChapterGetsNumber(t *testing.T) {
	taken := func(_, _ *int) error { return errors.New("duplicate key value violates unique constraint") }
	tests := []struct {
		name     string
		file     string
		comments []string
		index    *int
		libType  string
		itemType string
		fillErr  func(index, disc *int) error
		want     *int
		opens    int // over two scans
		fills    int // over two scans
	}{
		{name: "number in the name", file: "07 Seven.flac", want: ip(7), fills: 1},
		{name: "number in the tags", file: "Seven.flac", comments: []string{"TRACKNUMBER=7"}, want: ip(7), opens: 1, fills: 1},
		{name: "no number anywhere: read once", file: "Seven.flac", want: nil, opens: 1},
		{name: "number taken: tried once", file: "07 Seven.flac", fillErr: taken, want: nil, fills: 1},
		{name: "already numbered", file: "07 Seven.flac", index: ip(3), want: ip(3)},
		{name: "a single-file book is no chapter", file: "07 Seven.flac", itemType: "audiobook", want: nil},
		{name: "not an audiobook library", file: "07 Seven.flac", libType: "music", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "Author", "Book", tc.file)
			writeTaggedFLAC(t, path, append([]string{"TITLE=Seven"}, tc.comments...)...)
			past := time.Now().Add(-time.Hour)
			if err := os.Chtimes(path, past, past); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			svc := newMockMediaService()
			svc.fillErr = tc.fillErr
			poster := "poster.jpg" // enriched: nothing else surfaces the item
			book := &media.Item{ID: uuid.New(), Type: "audiobook", Title: "Book", PosterPath: &poster}
			itemType := tc.itemType
			if itemType == "" {
				itemType = "audiobook_chapter"
			}
			chapter := &media.Item{ID: uuid.New(), Type: itemType, Title: "Seven", ParentID: &book.ID,
				Index: tc.index, PosterPath: &poster}
			svc.items[book.ID] = book
			svc.items[chapter.ID] = chapter
			hash := "unchanged"
			svc.fileByPath[path] = &media.File{ID: uuid.New(), MediaItemID: chapter.ID, FilePath: path,
				FileSize: info.Size(), FileHash: &hash, Status: "active", ScannedAt: time.Now()}

			store := &countingStore{opens: map[string]int{}}
			s := newTestScanner(svc).WithMediaStore(store)
			libType := tc.libType
			if libType == "" {
				libType = "audiobook"
			}
			for scan := 1; scan <= 2; scan++ {
				if _, _, isNew, err := s.processFile(context.Background(), uuid.New(), libType, path, []string{root}); err != nil || isNew {
					t.Fatalf("scan %d: processFile = isNew %v, err %v", scan, isNew, err)
				}
			}
			if len(svc.fileCalls) != 0 || len(svc.hierarchyCalls) != 0 {
				t.Errorf("file left the fast skip: %d upserts, %d hierarchy calls", len(svc.fileCalls), len(svc.hierarchyCalls))
			}
			if got := store.opens[path]; got != tc.opens {
				t.Errorf("file opened %d times over two scans, want %d", got, tc.opens)
			}
			if len(svc.fillCalls) != tc.fills {
				t.Errorf("fills over two scans = %d, want %d", len(svc.fillCalls), tc.fills)
			}
			if !samePos(chapter.Index, tc.want) {
				t.Errorf("chapter index = %v, want %v", deref(chapter.Index), deref(tc.want))
			}
		})
	}
}

// The merge repair runs before the file pass of a full audiobook scan only:
// a directory scan wouldn't reach every file it marks for re-import, and
// other library types never had audiobooks merged.
func TestScan_RepairsMergedAudiobooksOnFullAudiobookScans(t *testing.T) {
	prev := externalArtHTTPClient
	externalArtHTTPClient = &http.Client{Transport: refusingTransport{}}
	t.Cleanup(func() { externalArtHTTPClient = prev })

	tests := []struct {
		name    string
		libType string
		scoped  bool
		want    int
	}{
		{"full audiobook scan", "audiobook", false, 1},
		{"directory scan", "audiobook", true, 0},
		{"music library", "music", false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTaggedFLAC(t, filepath.Join(root, "Author", "Book", "01 One.flac"), "TITLE=One")
			svc := newMockMediaService()
			s := New(svc, nil, stubConc{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			var err error
			if tc.scoped {
				_, err = s.ScanDirectory(context.Background(), uuid.New(), tc.libType, filepath.Join(root, "Author"), []string{root})
			} else {
				_, err = s.ScanLibrary(context.Background(), uuid.New(), tc.libType, []string{root})
			}
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if svc.repairCalls != tc.want {
				t.Errorf("repair ran %d times, want %d", svc.repairCalls, tc.want)
			}
		})
	}
}
