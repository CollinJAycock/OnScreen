package scanner

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dhowden/tag"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// ── mock MediaService ────────────────────────────────────────────────────────

type mockMediaService struct {
	items      map[uuid.UUID]*media.Item
	files      map[uuid.UUID]*media.File
	fileByPath map[string]*media.File

	// Track calls to FindOrCreateHierarchyItem.
	hierarchyCalls []media.CreateItemParams
	// Track calls to FindOrCreateItem.
	flatCalls []media.CreateItemParams

	// Dedupe stub: records calls and returns dedupeResult/dedupeErr.
	dedupeCalls  []dedupeCall
	dedupeResult media.DedupeResult
	dedupeErr    error

	// Enrichment-attempt tracking for shouldEnrich tests.
	enrichAttempts        map[uuid.UUID]time.Time
	touchedEnrichAttempts []uuid.UUID
}

type dedupeCall struct {
	itemType  string
	libraryID uuid.UUID
}

func newMockMediaService() *mockMediaService {
	return &mockMediaService{
		items:      make(map[uuid.UUID]*media.Item),
		files:      make(map[uuid.UUID]*media.File),
		fileByPath: make(map[string]*media.File),
	}
}

func (m *mockMediaService) FindOrCreateItem(_ context.Context, p media.CreateItemParams) (*media.Item, error) {
	m.flatCalls = append(m.flatCalls, p)
	it := &media.Item{
		ID:        uuid.New(),
		LibraryID: p.LibraryID,
		Type:      p.Type,
		Title:     p.Title,
		SortTitle: p.SortTitle,
		Year:      p.Year,
		ParentID:  p.ParentID,
		Index:     p.Index,
	}
	m.items[it.ID] = it
	return it, nil
}

func (m *mockMediaService) FindOrCreateHierarchyItem(_ context.Context, p media.CreateItemParams) (*media.Item, error) {
	m.hierarchyCalls = append(m.hierarchyCalls, p)
	it := &media.Item{
		ID:        uuid.New(),
		LibraryID: p.LibraryID,
		Type:      p.Type,
		Title:     p.Title,
		SortTitle: p.SortTitle,
		Year:      p.Year,
		ParentID:  p.ParentID,
		Index:     p.Index,
	}
	m.items[it.ID] = it
	return it, nil
}

func (m *mockMediaService) CreateOrUpdateFile(_ context.Context, p media.CreateFileParams) (*media.File, bool, error) {
	f := &media.File{
		ID:          uuid.New(),
		MediaItemID: p.MediaItemID,
		FilePath:    p.FilePath,
		FileSize:    p.FileSize,
		Status:      "active",
	}
	m.files[f.ID] = f
	m.fileByPath[p.FilePath] = f
	return f, true, nil
}

func (m *mockMediaService) GetFileByPath(_ context.Context, path string) (*media.File, error) {
	if f, ok := m.fileByPath[path]; ok {
		return f, nil
	}
	return nil, errors.New("not found")
}

func (m *mockMediaService) GetItem(_ context.Context, id uuid.UUID) (*media.Item, error) {
	if it, ok := m.items[id]; ok {
		return it, nil
	}
	return nil, errors.New("not found")
}

func (m *mockMediaService) UpdateItemMetadata(_ context.Context, p media.UpdateItemMetadataParams) (*media.Item, error) {
	if it, ok := m.items[p.ID]; ok {
		it.Title = p.Title
		if p.PosterPath != nil {
			it.PosterPath = p.PosterPath
		}
		if p.FanartPath != nil {
			it.FanartPath = p.FanartPath
		}
		return it, nil
	}
	return nil, errors.New("not found")
}

func (m *mockMediaService) UpdateItemLyrics(_ context.Context, _ uuid.UUID, _, _ *string) error {
	return nil
}
func (m *mockMediaService) SetItemKind(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (m *mockMediaService) MarkFileActive(_ context.Context, _ uuid.UUID) error        { return nil }
func (m *mockMediaService) MarkMissing(_ context.Context, _ uuid.UUID) error           { return nil }
func (m *mockMediaService) DeleteFile(_ context.Context, _ uuid.UUID) error            { return nil }
func (m *mockMediaService) SoftDeleteItemIfEmpty(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockMediaService) RestoreItemAncestry(_ context.Context, _ uuid.UUID) error   { return nil }
func (m *mockMediaService) GetFiles(_ context.Context, _ uuid.UUID) ([]media.File, error) {
	return nil, nil
}
func (m *mockMediaService) ListActiveFilesForLibrary(_ context.Context, _ uuid.UUID) ([]media.File, error) {
	return nil, nil
}
func (m *mockMediaService) CleanupMissingFiles(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (m *mockMediaService) UpsertPhotoMetadata(_ context.Context, _ media.PhotoMetadataParams) error {
	return nil
}
func (m *mockMediaService) CleanupEmptyItems(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockMediaService) GetEnrichAttemptedAt(_ context.Context, id uuid.UUID) (*time.Time, error) {
	if ts, ok := m.enrichAttempts[id]; ok {
		return &ts, nil
	}
	return nil, nil
}
func (m *mockMediaService) TouchEnrichAttempt(_ context.Context, id uuid.UUID) error {
	if m.enrichAttempts == nil {
		m.enrichAttempts = make(map[uuid.UUID]time.Time)
	}
	m.enrichAttempts[id] = time.Now()
	m.touchedEnrichAttempts = append(m.touchedEnrichAttempts, id)
	return nil
}
func (m *mockMediaService) DedupeTopLevelItems(_ context.Context, itemType string, libraryID *uuid.UUID) (media.DedupeResult, error) {
	call := dedupeCall{itemType: itemType}
	if libraryID != nil {
		call.libraryID = *libraryID
	}
	m.dedupeCalls = append(m.dedupeCalls, call)
	return m.dedupeResult, m.dedupeErr
}
func (m *mockMediaService) DedupeChildItems(_ context.Context, itemType string, parentID *uuid.UUID) (media.DedupeResult, error) {
	call := dedupeCall{itemType: itemType}
	if parentID != nil {
		call.libraryID = *parentID
	}
	m.dedupeCalls = append(m.dedupeCalls, call)
	return m.dedupeResult, m.dedupeErr
}
func (m *mockMediaService) MergeCollabArtists(_ context.Context, libraryID *uuid.UUID) (media.DedupeResult, error) {
	call := dedupeCall{itemType: "collab-artist"}
	if libraryID != nil {
		call.libraryID = *libraryID
	}
	m.dedupeCalls = append(m.dedupeCalls, call)
	return m.dedupeResult, m.dedupeErr
}
func (m *mockMediaService) MergeCrossParentAudiobooks(_ context.Context, libraryID uuid.UUID) (media.DedupeResult, error) {
	m.dedupeCalls = append(m.dedupeCalls, dedupeCall{itemType: "cross-parent-audiobook", libraryID: libraryID})
	return m.dedupeResult, m.dedupeErr
}
func (m *mockMediaService) PrunePhantomAudiobooks(_ context.Context, _ uuid.UUID) (int, error) {
	return 0, nil
}
func (m *mockMediaService) PruneEmptyBookAuthors(_ context.Context, _ uuid.UUID) (int, error) {
	return 0, nil
}
func (m *mockMediaService) UpsertEventCollection(_ context.Context, _ uuid.UUID, _ string) (uuid.UUID, error) {
	return uuid.Nil, nil
}
func (m *mockMediaService) AddItemToCollection(_ context.Context, _, _ uuid.UUID) error {
	return nil
}
func (m *mockMediaService) ListItems(_ context.Context, libraryID uuid.UUID, itemType string, _, _ int32) ([]media.Item, error) {
	var out []media.Item
	for _, it := range m.items {
		if it.LibraryID == libraryID && it.Type == itemType && it.ParentID == nil {
			out = append(out, *it)
		}
	}
	return out, nil
}
func (m *mockMediaService) FindTopLevelItem(_ context.Context, libraryID uuid.UUID, itemType, title string) (*media.Item, error) {
	for _, it := range m.items {
		if it.LibraryID == libraryID && it.Type == itemType && it.ParentID == nil && it.Title == title {
			return it, nil
		}
	}
	return nil, nil
}

func newTestScanner(svc *mockMediaService) *Scanner {
	return New(svc, nil, nil, slog.Default())
}

// ── processShowHierarchy ─────────────────────────────────────────────────────

func TestProcessShowHierarchy_S01E03(t *testing.T) {
	svc := newMockMediaService()
	s := newTestScanner(svc)
	libID := uuid.New()

	episode, err := s.processShowHierarchy(context.Background(), libID, "/media/tv/Breaking.Bad.S01E03.mkv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if episode.Type != "episode" {
		t.Errorf("type: got %q, want %q", episode.Type, "episode")
	}
	if episode.Title != "Episode 3" {
		t.Errorf("title: got %q, want %q", episode.Title, "Episode 3")
	}
	if episode.Index == nil || *episode.Index != 3 {
		t.Errorf("index: got %v, want 3", episode.Index)
	}

	// Should have created 3 hierarchy items: show, season, episode.
	if len(svc.hierarchyCalls) != 3 {
		t.Fatalf("expected 3 hierarchy calls, got %d", len(svc.hierarchyCalls))
	}
	if svc.hierarchyCalls[0].Type != "show" {
		t.Errorf("call[0] type: got %q, want %q", svc.hierarchyCalls[0].Type, "show")
	}
	if svc.hierarchyCalls[0].Title != "Breaking Bad" {
		t.Errorf("call[0] title: got %q, want %q", svc.hierarchyCalls[0].Title, "Breaking Bad")
	}
	if svc.hierarchyCalls[1].Type != "season" {
		t.Errorf("call[1] type: got %q, want %q", svc.hierarchyCalls[1].Type, "season")
	}
	if svc.hierarchyCalls[2].Type != "episode" {
		t.Errorf("call[2] type: got %q, want %q", svc.hierarchyCalls[2].Type, "episode")
	}
}

func TestProcessShowHierarchy_FolderStructure(t *testing.T) {
	svc := newMockMediaService()
	s := newTestScanner(svc)
	libID := uuid.New()

	episode, err := s.processShowHierarchy(context.Background(), libID, "/media/tv/The Wire/Season 3/S03E07.mkv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if episode.Type != "episode" {
		t.Errorf("type: got %q, want %q", episode.Type, "episode")
	}
	// Verify the show was created with the correct title from folder structure.
	if svc.hierarchyCalls[0].Title != "The Wire" {
		t.Errorf("show title: got %q, want %q", svc.hierarchyCalls[0].Title, "The Wire")
	}
}

func TestProcessShowHierarchy_UnparseableFallsBack(t *testing.T) {
	svc := newMockMediaService()
	s := newTestScanner(svc)
	libID := uuid.New()

	// A filename without any S##E## or 1x03 pattern should fall back to flat episode.
	item, err := s.processShowHierarchy(context.Background(), libID, "/media/tv/Some.Random.Video.2020.mkv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.Type != "episode" {
		t.Errorf("type: got %q, want %q", item.Type, "episode")
	}
	// Should use FindOrCreateItem (flat), not FindOrCreateHierarchyItem.
	if len(svc.hierarchyCalls) != 0 {
		t.Errorf("expected 0 hierarchy calls for unparseable file, got %d", len(svc.hierarchyCalls))
	}
	if len(svc.flatCalls) != 1 {
		t.Errorf("expected 1 flat call for unparseable file, got %d", len(svc.flatCalls))
	}
}

func TestProcessShowHierarchy_1x03Pattern(t *testing.T) {
	svc := newMockMediaService()
	s := newTestScanner(svc)
	libID := uuid.New()

	episode, err := s.processShowHierarchy(context.Background(), libID, "/media/tv/Show Name 2x05.mkv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if episode.Index == nil || *episode.Index != 5 {
		t.Errorf("episode index: got %v, want 5", episode.Index)
	}
	// Season should be 2.
	if svc.hierarchyCalls[1].Index == nil || *svc.hierarchyCalls[1].Index != 2 {
		t.Errorf("season index: got %v, want 2", svc.hierarchyCalls[1].Index)
	}
}

// ── processMusicHierarchy ────────────────────────────────────────────────────

// stubTagReader replaces readTagFrom to inject fake tag data in tests.
type stubTagMetadata struct {
	artist  string
	album   string
	title   string
	trackN  int
	trackT  int
	year    int
	genre   string
	picture *tag.Picture
}

func (s *stubTagMetadata) Format() tag.Format          { return tag.UnknownFormat }
func (s *stubTagMetadata) FileType() tag.FileType      { return tag.UnknownFileType }
func (s *stubTagMetadata) Title() string               { return s.title }
func (s *stubTagMetadata) Album() string               { return s.album }
func (s *stubTagMetadata) Artist() string              { return s.artist }
func (s *stubTagMetadata) AlbumArtist() string         { return s.artist }
func (s *stubTagMetadata) Composer() string            { return "" }
func (s *stubTagMetadata) Genre() string               { return s.genre }
func (s *stubTagMetadata) Year() int                   { return s.year }
func (s *stubTagMetadata) Track() (int, int)           { return s.trackN, s.trackT }
func (s *stubTagMetadata) Disc() (int, int)            { return 0, 0 }
func (s *stubTagMetadata) Picture() *tag.Picture       { return s.picture }
func (s *stubTagMetadata) Lyrics() string              { return "" }
func (s *stubTagMetadata) Comment() string             { return "" }
func (s *stubTagMetadata) Raw() map[string]interface{} { return nil }

func TestProcessMusicHierarchy_WithTags(t *testing.T) {
	svc := newMockMediaService()
	s := newTestScanner(svc)
	libID := uuid.New()

	// Create a temp file and stub the tag reader.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "song.flac")
	if err := os.WriteFile(filePath, []byte("fake audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := readTagFrom
	readTagFrom = func(r io.ReadSeeker) (tag.Metadata, error) {
		return &stubTagMetadata{
			artist: "Pink Floyd",
			album:  "Dark Side of the Moon",
			title:  "Time",
			trackN: 4,
			year:   1973,
			genre:  "Progressive Rock",
		}, nil
	}
	defer func() { readTagFrom = orig }()

	track, _, err := s.processMusicHierarchy(context.Background(), libID, filePath, []string{"/media/music"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if track.Type != "track" {
		t.Errorf("type: got %q, want %q", track.Type, "track")
	}

	// Should have created 3 hierarchy items: artist, album, track.
	if len(svc.hierarchyCalls) != 3 {
		t.Fatalf("expected 3 hierarchy calls, got %d", len(svc.hierarchyCalls))
	}
	if svc.hierarchyCalls[0].Type != "artist" {
		t.Errorf("call[0] type: got %q, want %q", svc.hierarchyCalls[0].Type, "artist")
	}
	if svc.hierarchyCalls[0].Title != "Pink Floyd" {
		t.Errorf("call[0] title: got %q, want %q", svc.hierarchyCalls[0].Title, "Pink Floyd")
	}
	if svc.hierarchyCalls[1].Type != "album" {
		t.Errorf("call[1] type: got %q, want %q", svc.hierarchyCalls[1].Type, "album")
	}
	if svc.hierarchyCalls[1].Title != "Dark Side of the Moon" {
		t.Errorf("call[1] title: got %q, want %q", svc.hierarchyCalls[1].Title, "Dark Side of the Moon")
	}
	if svc.hierarchyCalls[2].Type != "track" {
		t.Errorf("call[2] type: got %q, want %q", svc.hierarchyCalls[2].Type, "track")
	}
	if svc.hierarchyCalls[2].Title != "Time" {
		t.Errorf("call[2] title: got %q, want %q", svc.hierarchyCalls[2].Title, "Time")
	}
}

func TestProcessMusicHierarchy_FallbackToPath(t *testing.T) {
	svc := newMockMediaService()
	s := newTestScanner(svc)
	libID := uuid.New()

	// Create temp directory structure: Artist/Album/01 - Song.flac
	dir := t.TempDir()
	artistDir := filepath.Join(dir, "The Beatles")
	albumDir := filepath.Join(artistDir, "Abbey Road")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(albumDir, "05 - Come Together.flac")
	if err := os.WriteFile(filePath, []byte("not real audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	// readTagFrom will fail, forcing fallback to path parsing.
	orig := readTagFrom
	readTagFrom = func(r io.ReadSeeker) (tag.Metadata, error) {
		return nil, errors.New("not a valid audio file")
	}
	defer func() { readTagFrom = orig }()

	track, _, err := s.processMusicHierarchy(context.Background(), libID, filePath, []string{"/media/music"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if track.Type != "track" {
		t.Errorf("type: got %q, want %q", track.Type, "track")
	}

	if len(svc.hierarchyCalls) != 3 {
		t.Fatalf("expected 3 hierarchy calls, got %d", len(svc.hierarchyCalls))
	}
	if svc.hierarchyCalls[0].Title != "The Beatles" {
		t.Errorf("artist: got %q, want %q", svc.hierarchyCalls[0].Title, "The Beatles")
	}
	if svc.hierarchyCalls[1].Title != "Abbey Road" {
		t.Errorf("album: got %q, want %q", svc.hierarchyCalls[1].Title, "Abbey Road")
	}
	if svc.hierarchyCalls[2].Title != "Come Together" {
		t.Errorf("track: got %q, want %q", svc.hierarchyCalls[2].Title, "Come Together")
	}
}

func TestProcessMusicHierarchy_GenrePassedToTrack(t *testing.T) {
	svc := newMockMediaService()
	s := newTestScanner(svc)
	libID := uuid.New()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "song.mp3")
	if err := os.WriteFile(filePath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := readTagFrom
	readTagFrom = func(r io.ReadSeeker) (tag.Metadata, error) {
		return &stubTagMetadata{
			artist: "Radiohead",
			album:  "OK Computer",
			title:  "Paranoid Android",
			trackN: 2,
			genre:  "Alternative Rock",
		}, nil
	}
	defer func() { readTagFrom = orig }()

	_, _, err := s.processMusicHierarchy(context.Background(), libID, filePath, []string{"/media/music"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The track (3rd call) should have genre set.
	trackCall := svc.hierarchyCalls[2]
	if len(trackCall.Genres) != 1 || trackCall.Genres[0] != "Alternative Rock" {
		t.Errorf("genres: got %v, want [Alternative Rock]", trackCall.Genres)
	}
}

func TestProcessMusicHierarchy_YearOnAlbumAndTrack(t *testing.T) {
	svc := newMockMediaService()
	s := newTestScanner(svc)
	libID := uuid.New()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "song.flac")
	if err := os.WriteFile(filePath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := readTagFrom
	readTagFrom = func(r io.ReadSeeker) (tag.Metadata, error) {
		return &stubTagMetadata{
			artist: "Tool",
			album:  "Lateralus",
			title:  "Schism",
			trackN: 5,
			year:   2001,
		}, nil
	}
	defer func() { readTagFrom = orig }()

	_, _, err := s.processMusicHierarchy(context.Background(), libID, filePath, []string{"/media/music"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Album (2nd call) and track (3rd call) should have year set.
	albumCall := svc.hierarchyCalls[1]
	if albumCall.Year == nil || *albumCall.Year != 2001 {
		t.Errorf("album year: got %v, want 2001", albumCall.Year)
	}
	trackCall := svc.hierarchyCalls[2]
	if trackCall.Year == nil || *trackCall.Year != 2001 {
		t.Errorf("track year: got %v, want 2001", trackCall.Year)
	}
}

func TestProcessMusicHierarchy_SortTitleStripsArticle(t *testing.T) {
	svc := newMockMediaService()
	s := newTestScanner(svc)
	libID := uuid.New()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "song.flac")
	if err := os.WriteFile(filePath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := readTagFrom
	readTagFrom = func(r io.ReadSeeker) (tag.Metadata, error) {
		return &stubTagMetadata{
			artist: "The Beatles",
			album:  "Abbey Road",
			title:  "Come Together",
			trackN: 1,
		}, nil
	}
	defer func() { readTagFrom = orig }()

	_, _, err := s.processMusicHierarchy(context.Background(), libID, filePath, []string{"/media/music"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Artist sort title should strip "The ".
	artistCall := svc.hierarchyCalls[0]
	if artistCall.SortTitle != "beatles" {
		t.Errorf("artist sort_title: got %q, want %q", artistCall.SortTitle, "beatles")
	}
}

// TestProcessMusicHierarchy_FlatLayout_PostersDontCollide is the
// regression guard for "every album on an artist shows the same art".
// Two albums whose tracks share a directory (flat "/Music/Artist/*.flac"
// layouts are the common case) must end up with distinct poster files
// on disk AND distinct poster_path values in the DB. Before the fix,
// both albums wrote to "<dir>/poster.jpg" and the second clobbered the
// first.
func TestProcessMusicHierarchy_FlatLayout_PostersDontCollide(t *testing.T) {
	// makeJPEG produces a tiny valid JPEG the extractor can re-encode.
	makeJPEG := func(marker byte) []byte {
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.Pix[0] = marker
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			t.Fatalf("encode jpeg: %v", err)
		}
		return buf.Bytes()
	}

	svc := newMockMediaService()
	s := newTestScanner(svc)
	libID := uuid.New()
	musicRoot := t.TempDir()
	artistDir := filepath.Join(musicRoot, "The Beatles")
	if err := os.MkdirAll(artistDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Two tracks, two different albums, SAME directory — the flat-layout
	// bug scenario.
	trackA := filepath.Join(artistDir, "abbey-road-track.flac")
	trackB := filepath.Join(artistDir, "let-it-be-track.flac")
	if err := os.WriteFile(trackA, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackB, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := readTagFrom
	defer func() { readTagFrom = orig }()
	readTagFrom = func(r io.ReadSeeker) (tag.Metadata, error) {
		// Differentiate by whichever track the scanner is currently
		// reading — tag.ReadFrom is called on the opened file, and
		// file identity leaks via the os.File name. Easier: use the
		// file size stored in each file to pick which fixture to
		// return (both are "fake" here; distinguish by re-reading the
		// filename from the reader). In this test we serialise calls,
		// so a simple counter works.
		return nil, errors.New("unused")
	}

	// Run each track through separately with its own tag stub so we
	// get two distinct albums under one shared artistDir.
	runOne := func(trackPath string, album string, art []byte) *media.Item {
		readTagFrom = func(r io.ReadSeeker) (tag.Metadata, error) {
			return &stubTagMetadata{
				artist:  "The Beatles",
				album:   album,
				title:   "some track",
				trackN:  1,
				picture: &tag.Picture{MIMEType: "image/jpeg", Data: art},
			}, nil
		}
		track, _, err := s.processMusicHierarchy(context.Background(), libID, trackPath, []string{musicRoot})
		if err != nil {
			t.Fatalf("processMusicHierarchy %s: %v", trackPath, err)
		}
		// Find the album item (parent of the track).
		if track.ParentID == nil {
			t.Fatalf("track has no parent_id")
		}
		album_item, ok := svc.items[*track.ParentID]
		if !ok {
			t.Fatalf("album item %s missing from mock", *track.ParentID)
		}
		return album_item
	}

	albumA := runOne(trackA, "Abbey Road", makeJPEG(0xAA))
	albumB := runOne(trackB, "Let It Be", makeJPEG(0xBB))

	if albumA.ID == albumB.ID {
		t.Fatal("expected distinct album items")
	}
	if albumA.PosterPath == nil || albumB.PosterPath == nil {
		t.Fatalf("poster_path not set: A=%v B=%v", albumA.PosterPath, albumB.PosterPath)
	}
	if *albumA.PosterPath == *albumB.PosterPath {
		t.Fatalf("both albums share poster_path %q — collision regression", *albumA.PosterPath)
	}

	// Both posters must exist on disk at their ID-qualified filenames.
	for _, al := range []*media.Item{albumA, albumB} {
		want := filepath.Join(artistDir, al.ID.String()+"-poster.jpg")
		if _, err := os.Stat(want); err != nil {
			t.Errorf("album %q poster missing on disk at %s: %v", al.Title, want, err)
		}
	}
}
