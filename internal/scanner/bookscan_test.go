package scanner

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// TestParseBookTitle covers the filename → title normalisation. The
// scanner doesn't try to extract series/issue numbers in Stage 1 —
// that hierarchy story is a follow-up. Just strips the extension and
// normalises common separators.
func TestParseBookTitle(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/comics/Saga Vol 1.cbz", "Saga Vol 1"},
		{"/comics/saga_vol_1.cbz", "saga vol 1"},
		{"/comics/Saga.Vol.1.cbz", "Saga Vol 1"},
		{"/comics/single.cbz", "single"},
	}
	for _, c := range cases {
		got := parseBookTitle(c.path)
		if got != c.want {
			t.Errorf("parseBookTitle(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// TestIsCBZPageEntry covers the entry filter the scanner and API
// handler share — image extensions allowed, directories and macOS
// metadata files (which inflate the page count if not filtered)
// excluded.
func TestIsCBZPageEntry(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// Real pages
		{"001.jpg", true},
		{"page-001.png", true},
		{"chapter1/001.webp", true},
		// Directory entries
		{"chapter1/", false},
		{"", false},
		// macOS metadata noise
		{"__MACOSX/001.jpg", false},
		{"chapter1/.DS_Store", false},
		{"chapter1/._cover.jpg", false},
		// Non-image extensions
		{"info.txt", false},
		{"comicinfo.xml", false},
	}
	for _, c := range cases {
		got := isCBZPageEntry(c.name)
		if got != c.want {
			t.Errorf("isCBZPageEntry(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestCountCBZPages_RoundTrip builds a real CBZ in a temp dir and
// confirms the page count matches the number of image entries —
// catches regressions in either the entry filter or the zip walk.
func TestCountCBZPages_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cbzPath := filepath.Join(dir, "test.cbz")

	f, err := os.Create(cbzPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{
		"001.jpg",
		"002.png",
		"003.webp",
		"chapter1/004.jpg",
		// Should be excluded
		"__MACOSX/005.jpg",
		"info.txt",
		"chapter1/",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("x"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if got := countCBZPages(cbzPath); got != 4 {
		t.Errorf("countCBZPages = %d, want 4 (filter should exclude __MACOSX, .txt, dir entry)", got)
	}
}
