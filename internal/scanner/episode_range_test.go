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

// TestProcessShowHierarchy_MultiEpisodeFile: an "S01E01E02" file is ONE
// episode item keyed by its first episode, carrying index_end 2 and the
// range default title. No E02 item is created.
func TestProcessShowHierarchy_MultiEpisodeFile(t *testing.T) {
	for _, name := range []string{
		"/tv/Show Name/Season 1/Show.Name.S01E01E02.mkv",
		"/tv/Show Name/Season 1/Show Name - S01E01-E02 - Pilot.mkv",
		"/tv/Show Name/Season 1/Show Name - S01E01-02.mkv",
		"/tv/Show Name/Season 1/Show Name 1x01-1x02.mkv",
	} {
		t.Run(name, func(t *testing.T) {
			svc := newMockMediaService()
			s := newTestScanner(svc)
			ep, err := s.processShowHierarchy(context.Background(), uuid.New(), name, []string{"/tv"})
			if err != nil {
				t.Fatalf("processShowHierarchy: %v", err)
			}
			if ep.Index == nil || *ep.Index != 1 {
				t.Fatalf("index = %v, want 1", ep.Index)
			}
			if ep.IndexEnd == nil || *ep.IndexEnd != 2 {
				t.Fatalf("index_end = %v, want 2", ep.IndexEnd)
			}
			if ep.Title != "Episodes 1–2" {
				t.Errorf("title = %q, want %q", ep.Title, "Episodes 1–2")
			}
			if n := len(svc.hierarchyCalls); n != 3 {
				t.Errorf("hierarchy calls = %d, want 3 (show, season, ONE episode)", n)
			}
			if svc.hierarchyCalls[2].Title != "Episodes 1–2" {
				t.Errorf("episode created as %q, want the range default title", svc.hierarchyCalls[2].Title)
			}
		})
	}
}

// TestProcessShowHierarchy_SingleEpisodeNoRange: a single episode never
// writes index_end.
func TestProcessShowHierarchy_SingleEpisodeNoRange(t *testing.T) {
	svc := newMockMediaService()
	s := newTestScanner(svc)
	ep, err := s.processShowHierarchy(context.Background(), uuid.New(), "/tv/Show/Season 1/Show S01E03.mkv", []string{"/tv"})
	if err != nil {
		t.Fatalf("processShowHierarchy: %v", err)
	}
	if ep.IndexEnd != nil {
		t.Errorf("index_end = %v, want nil", *ep.IndexEnd)
	}
	if len(svc.indexEndCalls) != 0 {
		t.Errorf("SetEpisodeIndexEnd called %d times for a single episode", len(svc.indexEndCalls))
	}
}

// TestProcessFile_ExistingEpisodeGainsRange is the rescan conversion: an
// unchanged "S01E01-E02" file whose item was filed (and enriched) as E01
// before ranges were read gains index_end 2 on the fast skip, without the
// slow path and without a second episode item; its title goes back to the
// range default so the episode is surfaced for re-enrichment (and gets its
// joined title). A second scan is a no-op.
func TestProcessFile_ExistingEpisodeGainsRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Show Name - S01E01-E02 - Pilot.mkv")
	if err := os.WriteFile(path, []byte("not a real mkv"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	svc := newMockMediaService()
	seasonID := uuid.New()
	one := 1
	thumb, summary := "Shows/ep/thumb.jpg", "The pilot."
	epID := uuid.New()
	svc.items[epID] = &media.Item{
		ID: epID, Type: "episode", Title: "Pilot", SortTitle: "Pilot",
		ParentID: &seasonID, Index: &one, ThumbPath: &thumb, Summary: &summary,
	}
	hash := "deadbeef"
	svc.fileByPath[path] = &media.File{
		ID: uuid.New(), MediaItemID: epID, FilePath: path, FileSize: info.Size(),
		FileHash: &hash, Status: "active", ScannedAt: time.Now(),
	}

	s := newTestScanner(svc)
	item, file, isNew, err := s.processFile(context.Background(), uuid.New(), "show", path, []string{dir})
	if err != nil {
		t.Fatalf("processFile: %v", err)
	}
	if isNew {
		t.Errorf("unchanged file reported as new")
	}
	got := svc.items[epID]
	if got.IndexEnd == nil || *got.IndexEnd != 2 {
		t.Fatalf("index_end = %v, want 2", got.IndexEnd)
	}
	if got.Title != "Episodes 1–2" {
		t.Errorf("title = %q, want the range default so it re-enriches", got.Title)
	}
	if item == nil || item.ID != epID || file == nil {
		t.Errorf("the converted episode should be surfaced for enrichment; got item=%v file=%v", item, file)
	}
	if len(svc.hierarchyCalls) != 0 || len(svc.fileCalls) != 0 {
		t.Errorf("conversion must not take the slow path: hierarchy=%d files=%d", len(svc.hierarchyCalls), len(svc.fileCalls))
	}

	// Second scan: already converted, nothing written.
	calls := len(svc.indexEndCalls)
	if _, _, _, err := s.processFile(context.Background(), uuid.New(), "show", path, []string{dir}); err != nil {
		t.Fatalf("second processFile: %v", err)
	}
	if len(svc.indexEndCalls) != calls {
		t.Errorf("second scan wrote index_end again")
	}
}

// TestApplyEpisodeRange_KeepsRangeOfAnotherVersion: an item holding both a
// multi-episode file and a single-episode version keeps its range when the
// single one is (re)scanned.
func TestApplyEpisodeRange_KeepsRangeOfAnotherVersion(t *testing.T) {
	svc := newMockMediaService()
	s := newTestScanner(svc)
	seasonID := uuid.New()
	one, two := 1, 2
	epID := uuid.New()
	svc.items[epID] = &media.Item{ID: epID, Type: "episode", Title: "Pilot / Part 2", ParentID: &seasonID, Index: &one, IndexEnd: &two}
	svc.filesByItem = map[uuid.UUID][]media.File{
		epID: {
			{ID: uuid.New(), MediaItemID: epID, FilePath: "/tv/Show/Season 1/Show S01E01-E02.mkv", Status: "active"},
			{ID: uuid.New(), MediaItemID: epID, FilePath: "/tv/Show/Season 1/Show S01E01.mkv", Status: "active"},
		},
	}
	id, _ := parseEpisodeIdentity("/tv/Show/Season 1/Show S01E01.mkv")
	s.applyEpisodeRange(context.Background(), svc.items[epID], id, "/tv/Show/Season 1/Show S01E01.mkv")
	if got := svc.items[epID]; got.IndexEnd == nil || *got.IndexEnd != 2 {
		t.Fatalf("range dropped: index_end = %v", got.IndexEnd)
	}

	// With the multi-episode version gone, the single one clears it; an
	// enriched title is left for enrichment to replace.
	svc.filesByItem[epID][0].Status = "missing"
	s.applyEpisodeRange(context.Background(), svc.items[epID], id, "/tv/Show/Season 1/Show S01E01.mkv")
	if got := svc.items[epID]; got.IndexEnd != nil {
		t.Fatalf("index_end = %v, want cleared", *got.IndexEnd)
	}
	if got := svc.items[epID]; got.Title != "Pilot / Part 2" {
		t.Errorf("title = %q, an enriched title must not be reset when a range is dropped", got.Title)
	}
}

// TestItemNeedsEnrich_RangeDefaultTitle: a multi-episode item still titled
// "Episodes N–M" needs enrichment even with a thumb and summary.
func TestItemNeedsEnrich_RangeDefaultTitle(t *testing.T) {
	one, two := 1, 2
	thumb, summary := "Shows/ep/thumb.jpg", "x"
	it := &media.Item{Type: "episode", Title: "Episodes 1–2", Index: &one, IndexEnd: &two, ThumbPath: &thumb, Summary: &summary}
	if !itemNeedsEnrich(it) {
		t.Errorf("range default title should need enrichment")
	}
	it.Title = "Pilot / Part 2"
	if itemNeedsEnrich(it) {
		t.Errorf("an enriched range episode should not")
	}
}
