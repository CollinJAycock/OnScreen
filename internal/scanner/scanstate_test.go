package scanner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// serialConc runs one file at a time, so the mock's plain call counters
// can't race.
type serialConc struct{}

func (serialConc) ScanFileConcurrency() int    { return 1 }
func (serialConc) ScanLibraryConcurrency() int { return 1 }

// unchangedShow is an indexed, fully enriched show on disk: one season and
// n episodes whose files haven't changed since their rows were written.
type unchangedShow struct {
	root      string
	libraryID uuid.UUID
	svc       *mockMediaService
	paths     []string
	episodes  []*media.Item
	season    *media.Item
	show      *media.Item
}

func newUnchangedShow(t *testing.T, n int) *unchangedShow {
	t.Helper()
	u := &unchangedShow{root: t.TempDir(), libraryID: uuid.New(), svc: newMockMediaService()}
	poster := "The Show/poster.jpg"
	u.show = &media.Item{ID: uuid.New(), LibraryID: u.libraryID, Type: "show", Title: "The Show", PosterPath: &poster}
	u.season = &media.Item{ID: uuid.New(), LibraryID: u.libraryID, Type: "season", Title: "Season 1",
		ParentID: &u.show.ID, PosterPath: &poster}
	u.svc.items[u.show.ID] = u.show
	u.svc.items[u.season.ID] = u.season
	past := time.Now().Add(-time.Hour)
	for i := 1; i <= n; i++ {
		path := filepath.Join(u.root, "The Show", "Season 01", "The Show - S01E0"+string(rune('0'+i))+".mkv")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("unchanged episode bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, past, past); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		thumb := "The Show/Season 01/thumb.jpg"
		summary := "Enriched."
		idx := i
		ep := &media.Item{ID: uuid.New(), LibraryID: u.libraryID, Type: "episode", Title: "Episode",
			ParentID: &u.season.ID, Index: &idx, ThumbPath: &thumb, Summary: &summary, PosterPath: &poster}
		u.svc.items[ep.ID] = ep
		hash := "unchanged"
		dur := int64(1_000)
		audio := []byte(`[{"codec":"aac"}]`)
		f := &media.File{ID: uuid.New(), MediaItemID: ep.ID, FilePath: path, FileSize: info.Size(),
			FileHash: &hash, DurationMS: &dur, Status: "active", ScannedAt: time.Now(), AudioStreams: audio}
		u.svc.files[f.ID] = f
		u.svc.fileByPath[path] = f
		u.paths = append(u.paths, path)
		u.episodes = append(u.episodes, ep)
	}
	return u
}

func (u *unchangedShow) scanner() *Scanner {
	return New(u.svc, nil, serialConc{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// A full rescan of an unchanged library decides every file from the one
// snapshot read: no file lookups at all, each parent read once for the whole
// scan rather than once per file, and one narrow active-file read for orphan
// detection. Before, each of these episodes cost four queries (the file by
// path, the episode, its season, its show) and the scan read every active
// file row twice.
func TestFullScan_UnchangedFilesSkipPerFileLookups(t *testing.T) {
	const n = 3
	u := newUnchangedShow(t, n)
	if _, err := u.scanner().ScanLibrary(context.Background(), u.libraryID, "show", []string{u.root}); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if u.svc.snapshotCalls != 1 {
		t.Errorf("snapshot reads = %d, want 1", u.svc.snapshotCalls)
	}
	if u.svc.getFileByPathCalls != 0 {
		t.Errorf("GetFileByPath calls = %d, want 0 (every file is in the snapshot)", u.svc.getFileByPathCalls)
	}
	// One read per episode (its own row) plus the season and the show once.
	if want := n + 2; u.svc.getItemCalls != want {
		t.Errorf("GetItem calls = %d, want %d", u.svc.getItemCalls, want)
	}
	if u.svc.activeRefsCalls != 1 {
		t.Errorf("active file reads = %d, want 1", u.svc.activeRefsCalls)
	}
	if len(u.svc.fileCalls) != 0 {
		t.Errorf("unchanged files took the slow path: %d upserts", len(u.svc.fileCalls))
	}
}

// A directory scan reads no snapshot (one folder doesn't justify the whole
// library's rows) and lists no active files (it never runs orphan
// detection): it looks each file up by path, as before.
func TestDirectoryScan_LooksFilesUpByPath(t *testing.T) {
	const n = 3
	u := newUnchangedShow(t, n)
	dir := filepath.Join(u.root, "The Show", "Season 01")
	if _, err := u.scanner().ScanDirectory(context.Background(), u.libraryID, "show", dir, []string{u.root}); err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}
	if u.svc.snapshotCalls != 0 || u.svc.activeRefsCalls != 0 {
		t.Errorf("directory scan read snapshot %d / active files %d times, want 0 / 0",
			u.svc.snapshotCalls, u.svc.activeRefsCalls)
	}
	if u.svc.getFileByPathCalls != n {
		t.Errorf("GetFileByPath calls = %d, want %d", u.svc.getFileByPathCalls, n)
	}
	// The parent cache still applies within a directory scan.
	if want := n + 2; u.svc.getItemCalls != want {
		t.Errorf("GetItem calls = %d, want %d", u.svc.getItemCalls, want)
	}
}

// A snapshot that can't be read leaves the scan correct, just slower: every
// file is looked up by path and still fast-skipped.
func TestFullScan_SnapshotErrorFallsBackToLookups(t *testing.T) {
	const n = 3
	u := newUnchangedShow(t, n)
	u.svc.snapshotErr = errors.New("snapshot unavailable")
	if _, err := u.scanner().ScanLibrary(context.Background(), u.libraryID, "show", []string{u.root}); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if u.svc.getFileByPathCalls != n {
		t.Errorf("GetFileByPath calls = %d, want %d", u.svc.getFileByPathCalls, n)
	}
	if len(u.svc.fileCalls) != 0 {
		t.Errorf("unchanged files took the slow path: %d upserts", len(u.svc.fileCalls))
	}
}

// A file the snapshot doesn't hold (indexed under another library, or after
// the snapshot was read) is still found by path, so it is never re-created.
func TestFullScan_SnapshotMissFallsBackToLookup(t *testing.T) {
	u := newUnchangedShow(t, 2)
	// Move one episode's item out of the library: the snapshot (scoped to
	// the library's items) no longer holds its file, GetFileByPath does.
	u.episodes[0].LibraryID = uuid.New()
	if _, err := u.scanner().ScanLibrary(context.Background(), u.libraryID, "show", []string{u.root}); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if u.svc.getFileByPathCalls != 1 {
		t.Errorf("GetFileByPath calls = %d, want 1 (the one snapshot miss)", u.svc.getFileByPathCalls)
	}
	if len(u.svc.fileCalls) != 0 {
		t.Errorf("a known file was re-created: %d upserts", len(u.svc.fileCalls))
	}
}

// Orphan detection sweeps the narrow active-file list: a row the walk didn't
// see is deleted, the rest are kept, and an error listing them skips the
// sweep rather than reading as "no active files".
func TestFullScan_OrphanSweepUsesActiveRefs(t *testing.T) {
	u := newUnchangedShow(t, 3)
	gone := media.FileRef{ID: uuid.New(), MediaItemID: u.episodes[0].ID, FilePath: filepath.Join(u.root, "gone.mkv")}
	for _, p := range u.paths {
		f := u.svc.fileByPath[p]
		u.svc.activeRefs = append(u.svc.activeRefs, media.FileRef{ID: f.ID, MediaItemID: f.MediaItemID, FilePath: p})
	}
	u.svc.activeRefs = append(u.svc.activeRefs, gone)
	if _, err := u.scanner().ScanLibrary(context.Background(), u.libraryID, "show", []string{u.root}); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if len(u.svc.deletedFiles) != 1 || u.svc.deletedFiles[0] != gone.ID {
		t.Errorf("deleted files = %v, want only %v", u.svc.deletedFiles, gone.ID)
	}

	u2 := newUnchangedShow(t, 3)
	u2.svc.activeRefs = []media.FileRef{gone}
	u2.svc.activeRefsErr = errors.New("db down")
	if _, err := u2.scanner().ScanLibrary(context.Background(), u2.libraryID, "show", []string{u2.root}); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if len(u2.svc.deletedFiles) != 0 {
		t.Errorf("a failed active-file read still swept: deleted %v", u2.svc.deletedFiles)
	}
}

// The full row, not the snapshot's slice of it, goes to the enricher: an
// unchanged file whose item still needs enrichment is handed back with every
// column.
func TestProcessFile_SnapshotHitHandsEnricherTheFullRow(t *testing.T) {
	u := newUnchangedShow(t, 1)
	ep := u.episodes[0]
	ep.ThumbPath, ep.Summary = nil, nil // never enriched
	path := u.paths[0]
	snap := u.svc.fileByPath[path]
	st := &scanState{files: map[string]media.FileScanSnapshot{path: {
		ID: snap.ID, MediaItemID: snap.MediaItemID, FilePath: path, FileSize: snap.FileSize,
		FileHash: snap.FileHash, Status: snap.Status, ScannedAt: snap.ScannedAt, DurationMS: snap.DurationMS,
	}}}
	ctx := withScanState(context.Background(), st)
	item, file, isNew, err := u.scanner().processFile(ctx, u.libraryID, "show", path, []string{u.root})
	if err != nil || isNew {
		t.Fatalf("processFile: isNew %v, err %v", isNew, err)
	}
	if item == nil || file == nil {
		t.Fatal("an item needing enrichment came back without its file")
	}
	if string(file.AudioStreams) != `[{"codec":"aac"}]` {
		t.Errorf("enricher got the snapshot File (AudioStreams %q), want the full row", file.AudioStreams)
	}
}

// A parent without a poster isn't cached: enrichment may give it one, so it
// is read again by the next file that asks.
func TestParentItem_CachesOnlyParentsWithPosters(t *testing.T) {
	svc := newMockMediaService()
	poster := "p.jpg"
	with := &media.Item{ID: uuid.New(), Type: "season", PosterPath: &poster}
	without := &media.Item{ID: uuid.New(), Type: "season"}
	svc.items[with.ID], svc.items[without.ID] = with, without
	s := newTestScanner(svc)
	ctx := withScanState(context.Background(), &scanState{})
	for i := 0; i < 3; i++ {
		if _, err := s.parentItem(ctx, with.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.parentItem(ctx, without.ID); err != nil {
			t.Fatal(err)
		}
	}
	if want := 1 + 3; svc.getItemCalls != want {
		t.Errorf("GetItem calls = %d, want %d (one for the poster'd parent, three for the other)", svc.getItemCalls, want)
	}
	// Outside a scan nothing is cached.
	svc.getItemCalls = 0
	for i := 0; i < 2; i++ {
		_, _ = s.parentItem(context.Background(), with.ID)
	}
	if svc.getItemCalls != 2 {
		t.Errorf("GetItem calls outside a scan = %d, want 2", svc.getItemCalls)
	}
}

// countingCreditStore counts CreditsScanned lookups.
type countingCreditStore struct {
	*fakeCreditStore
	lookups int
}

func (c *countingCreditStore) CreditsScanned(ctx context.Context, id uuid.UUID) (bool, error) {
	c.lookups++
	return c.fakeCreditStore.CreditsScanned(ctx, id)
}

// The credits-read flag comes from the snapshot when the scan has one, so
// the first scan after a restart doesn't ask the database once per track.
func TestHealTrackCredits_ReadsFlagFromSnapshot(t *testing.T) {
	svc := newMockMediaService()
	album := &media.Item{ID: uuid.New(), Type: "album"}
	svc.items[album.ID] = album
	track := &media.Item{ID: uuid.New(), Type: "track", ParentID: &album.ID}
	file := &media.File{ID: uuid.New(), MediaItemID: track.ID, FilePath: "/m/a/t.flac"}
	store := &countingCreditStore{fakeCreditStore: &fakeCreditStore{
		byMBID: map[uuid.UUID]uuid.UUID{}, credits: map[uuid.UUID][]TrackArtist{}, scanned: map[uuid.UUID]bool{},
	}}
	s := newTestScanner(svc).WithArtistCredits(store)
	st := &scanState{files: map[string]media.FileScanSnapshot{
		file.FilePath: {ID: file.ID, MediaItemID: track.ID, FilePath: file.FilePath, CreditsScanned: true},
	}}
	s.healTrackCredits(withScanState(context.Background(), st), uuid.New(), track, file, file.FilePath)
	if store.lookups != 0 {
		t.Errorf("CreditsScanned lookups = %d, want 0 (the snapshot says read)", store.lookups)
	}
	if svc.getItemCalls != 0 {
		t.Errorf("a track whose credits were read still loaded its album (%d GetItem)", svc.getItemCalls)
	}

	// No snapshot row: ask, as before.
	other := &media.File{ID: uuid.New(), MediaItemID: track.ID, FilePath: "/m/a/u.flac"}
	store.scanned[other.ID] = true
	s.healTrackCredits(withScanState(context.Background(), st), uuid.New(), track, other, other.FilePath)
	if store.lookups != 1 {
		t.Errorf("CreditsScanned lookups = %d, want 1", store.lookups)
	}
}
