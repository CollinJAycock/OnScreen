package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindArtOnDisk_PriorityOrder verifies that when multiple candidate
// filenames exist in a directory we pick the one earliest in the
// caller's list — "cover.jpg" beats "folder.jpg" beats "album.jpg" —
// matching Plex/Jellyfin precedence.
func TestFindArtOnDisk_PriorityOrder(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "folder.jpg"), []byte("folder-bytes"))
	write(t, filepath.Join(dir, "cover.jpg"), []byte("cover-bytes"))
	write(t, filepath.Join(dir, "album.jpg"), []byte("album-bytes"))

	got, ok := findArtOnDisk(dir, albumArtFilenames)
	if !ok {
		t.Fatal("expected a hit, got ok=false")
	}
	if string(got) != "cover-bytes" {
		t.Errorf("expected cover-bytes (first in priority list), got %q", got)
	}
}

// TestFindArtOnDisk_CaseInsensitive handles messy libraries where names
// like "Cover.JPG" or "FOLDER.jpeg" turn up — ripper-dependent casing
// shouldn't matter.
func TestFindArtOnDisk_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Folder.JPG"), []byte("ok"))
	got, ok := findArtOnDisk(dir, albumArtFilenames)
	if !ok || string(got) != "ok" {
		t.Errorf("case-insensitive match failed: ok=%v got=%q", ok, got)
	}
}

// TestFindArtOnDisk_SkipsEmpty guards against zero-byte placeholder
// files (e.g. from a failed download) being picked up as valid art.
func TestFindArtOnDisk_SkipsEmpty(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "cover.jpg"), []byte{})
	_, ok := findArtOnDisk(dir, albumArtFilenames)
	if ok {
		t.Error("expected empty file to be skipped, got ok=true")
	}
}

// TestFindArtOnDisk_NoMatch returns false cleanly when the directory
// contains only audio files and no recognized cover-art filename.
func TestFindArtOnDisk_NoMatch(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "track01.flac"), []byte("audio"))
	write(t, filepath.Join(dir, "random.png"), []byte("other"))
	if _, ok := findArtOnDisk(dir, albumArtFilenames); ok {
		t.Error("expected no match, got ok=true")
	}
}

// TestFindArtOnDisk_MissingDir is the common case on flat libraries
// where os.ReadDir fails — must not panic, must return false.
func TestFindArtOnDisk_MissingDir(t *testing.T) {
	if _, ok := findArtOnDisk("/nonexistent/path", albumArtFilenames); ok {
		t.Error("expected false for missing dir")
	}
}

// TestResolveArtworkPath_Exists confirms a valid poster_path resolves.
func TestResolveArtworkPath_Exists(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "AC+DC"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "AC+DC", "x-poster.jpg"), []byte("img"))
	got := resolveArtworkPath("AC+DC/x-poster.jpg", []string{root})
	if got == "" {
		t.Error("expected a resolved path, got empty")
	}
}

// TestResolveArtworkPath_Missing is the self-heal path: DB thinks art
// exists but the file is gone. Caller uses this signal to re-run disk
// discovery instead of trusting the stale DB reference.
func TestResolveArtworkPath_Missing(t *testing.T) {
	root := t.TempDir()
	got := resolveArtworkPath("gone/poster.jpg", []string{root})
	if got != "" {
		t.Errorf("expected empty for missing file, got %q", got)
	}
}

// TestResolveArtworkPath_Empty short-circuits when poster_path is "".
func TestResolveArtworkPath_Empty(t *testing.T) {
	if got := resolveArtworkPath("", []string{t.TempDir()}); got != "" {
		t.Errorf("expected empty for empty input, got %q", got)
	}
}

// TestResolveArtworkPath_ZeroByte treats a 0-byte file the same as
// missing — a failed download that left a stub shouldn't lock the
// scanner out of retrying.
func TestResolveArtworkPath_ZeroByte(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "stub.jpg"), []byte{})
	if got := resolveArtworkPath("stub.jpg", []string{root}); got != "" {
		t.Errorf("expected empty for zero-byte file, got %q", got)
	}
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
