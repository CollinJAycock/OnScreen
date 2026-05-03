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

// TestProcessFile_MtimeFastSkip exercises the mtime+size short-circuit added
// to processFile. The whole point of the fast skip is to bypass HashFile +
// ffprobe + DB upserts when a file on disk hasn't changed since the last
// scan — the bug we're fixing is that periodic scans of a music library
// were re-hashing every track every cycle.
//
// The test plants a pre-existing file row with FileHash set, FileSize equal
// to the temp file's size, and ScannedAt set to a moment AFTER the file's
// mtime. processFile must return (nil, nil, false, nil) — meaning fast skip
// took the path. If it falls through to the slow path, CreateOrUpdateFile
// would be called and a non-nil File would be returned (or HashFile would
// fail loudly on the small temp file). Either failure mode is caught.
func TestProcessFile_MtimeFastSkip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "track.flac")
	contents := []byte("not a real flac, but we never read it on the fast skip path")
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	// Force the file's mtime into the past so ScannedAt = now is strictly
	// after info.ModTime(). On Windows, the os.WriteFile mtime resolution
	// is fine but the same-instant comparison is not safe — chtimes makes
	// the precondition unambiguous.
	pastMtime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(path, pastMtime, pastMtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	itemID := uuid.New()
	hash := "deadbeefcafefood"
	svc := newMockMediaService()
	svc.items[itemID] = &media.Item{
		ID:    itemID,
		Type:  "track",
		Title: "Pre-existing track",
	}
	durationMS := int64(180_000)
	svc.fileByPath[path] = &media.File{
		ID:          uuid.New(),
		MediaItemID: itemID,
		FilePath:    path,
		FileSize:    info.Size(),
		FileHash:    &hash,
		DurationMS:  &durationMS,
		Status:      "active",
		// ScannedAt strictly later than file mtime — this is the
		// precondition for the fast skip.
		ScannedAt: time.Now(),
	}

	s := newTestScanner(svc)
	libID := uuid.New()

	// libraryType="music" exercises the type that the prior hash fast-path
	// explicitly excluded; the new mtime fast-skip applies to all types.
	item, file, isNew, err := s.processFile(context.Background(), libID, "music", path, []string{dir})
	if err != nil {
		t.Fatalf("processFile returned error: %v", err)
	}
	if item != nil {
		t.Errorf("fast skip should return nil item; got %+v", item)
	}
	if file != nil {
		t.Errorf("fast skip should return nil file; got %+v", file)
	}
	if isNew {
		t.Errorf("fast skip should report isNew=false")
	}
	// The slow path always calls CreateOrUpdateFile, which would have
	// produced a second entry in svc.files. Map size must stay at 1
	// (the pre-seeded row).
	if len(svc.files) != 0 {
		t.Errorf("CreateOrUpdateFile should not be called on fast skip; svc.files has %d entries", len(svc.files))
	}
}

// TestProcessFile_MtimeFastSkip_SizeMismatchFallsThrough verifies the
// short-circuit refuses to fire when the on-disk file size differs from the
// stored row — that's the signal that something genuinely changed and we
// must re-process. Without this guard, an in-place edit that preserves mtime
// (rare but possible — e.g. a metadata tagger that resets mtime) would be
// silently ignored.
func TestProcessFile_MtimeFastSkip_SizeMismatchFallsThrough(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "track.flac")
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	pastMtime := time.Now().Add(-1 * time.Hour)
	_ = os.Chtimes(path, pastMtime, pastMtime)

	itemID := uuid.New()
	hash := "stale"
	svc := newMockMediaService()
	svc.items[itemID] = &media.Item{ID: itemID, Type: "track"}
	svc.fileByPath[path] = &media.File{
		ID:          uuid.New(),
		MediaItemID: itemID,
		FilePath:    path,
		FileSize:    99999, // wildly different from on-disk 5 bytes
		FileHash:    &hash,
		Status:      "active",
		ScannedAt:   time.Now(),
	}

	s := newTestScanner(svc)
	// We don't care what the slow path actually does here (the temp file is
	// not real audio so ffprobe will warn) — we only care that it FELL
	// THROUGH to the slow path, evidenced by a non-nil item being returned.
	item, _, _, err := s.processFile(context.Background(), uuid.New(), "music", path, []string{dir})
	if err != nil {
		t.Fatalf("processFile returned error: %v", err)
	}
	if item == nil {
		t.Fatal("size mismatch must fall through to slow path; got nil item (fast skip incorrectly fired)")
	}
}
