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

// TestProcessFile_MovieNaming covers the slow path: a new movie file's
// edition and part number reach CreateOrUpdateFile, and the title/year the
// item is found by are the ones the name always gave.
func TestProcessFile_MovieNaming(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Movie (2001)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Movie (2001) - Director's Cut - cd2.mkv")
	if err := os.WriteFile(path, []byte("not a real mkv"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := newMockMediaService()
	s := newTestScanner(svc)
	if _, _, _, err := s.processFile(context.Background(), uuid.New(), "movie", path, []string{filepath.Dir(dir)}); err != nil {
		t.Fatalf("processFile: %v", err)
	}
	if len(svc.fileCalls) != 1 {
		t.Fatalf("CreateOrUpdateFile calls = %d, want 1", len(svc.fileCalls))
	}
	p := svc.fileCalls[0]
	if p.Edition == nil || *p.Edition != "Director's Cut" {
		t.Errorf("edition = %v, want Director's Cut", p.Edition)
	}
	if p.PartIndex == nil || *p.PartIndex != 2 {
		t.Errorf("part = %v, want 2", p.PartIndex)
	}
	var item *media.Item
	for _, it := range svc.items {
		item = it
	}
	if item == nil || item.Title != "Movie" || item.Year == nil || *item.Year != 2001 {
		t.Fatalf("item = %+v, want Movie (2001)", item)
	}
}

// TestProcessFile_EpisodeHasNoNaming: TV files never get an edition or a
// part, whatever their names say.
func TestProcessFile_EpisodeHasNoNaming(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Show (2001) S01E01 - Extended cd1.mkv")
	if err := os.WriteFile(path, []byte("not a real mkv"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := newMockMediaService()
	s := newTestScanner(svc)
	if _, _, _, err := s.processFile(context.Background(), uuid.New(), "show", path, []string{dir}); err != nil {
		t.Fatalf("processFile: %v", err)
	}
	if len(svc.fileCalls) != 1 {
		t.Fatalf("CreateOrUpdateFile calls = %d, want 1", len(svc.fileCalls))
	}
	if p := svc.fileCalls[0]; p.Edition != nil || p.PartIndex != nil {
		t.Errorf("episode naming = %v/%v, want none", p.Edition, p.PartIndex)
	}
}

// TestProcessFile_FastSkipHealsNaming: a movie file scanned before migration
// 00045 is unchanged on disk, so the fast skip handles it — and must still
// fill its edition and part from the name, once.
func TestProcessFile_FastSkipHealsNaming(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Movie (2001) [Extended] cd2.mkv")
	if err := os.WriteFile(path, []byte("contents"), 0o644); err != nil {
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
	itemID := uuid.New()
	poster, summary := "p.jpg", "s"
	svc.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "Movie", PosterPath: &poster, ThumbPath: &poster, Summary: &summary}
	hash := "cafe"
	dur := int64(1000)
	fileID := uuid.New()
	svc.fileByPath[path] = &media.File{
		ID: fileID, MediaItemID: itemID, FilePath: path, FileSize: info.Size(),
		FileHash: &hash, DurationMS: &dur, Status: "active", ScannedAt: time.Now(),
	}
	s := newTestScanner(svc)
	for i := 0; i < 2; i++ {
		if _, _, _, err := s.processFile(context.Background(), uuid.New(), "movie", path, []string{dir}); err != nil {
			t.Fatalf("processFile: %v", err)
		}
	}
	if len(svc.fileCalls) != 0 {
		t.Fatalf("slow path ran (%d CreateOrUpdateFile calls); want the fast skip", len(svc.fileCalls))
	}
	if len(svc.namingCalls) != 1 {
		t.Fatalf("SetFileNaming calls = %d, want 1 (second scan agrees with the row)", len(svc.namingCalls))
	}
	c := svc.namingCalls[0]
	if c.id != fileID || c.edition == nil || *c.edition != "Extended" || c.partIndex == nil || *c.partIndex != 2 {
		t.Fatalf("SetFileNaming = %+v, want Extended / part 2 on %s", c, fileID)
	}
}
