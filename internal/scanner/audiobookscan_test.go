package scanner

import "testing"

// TestParseAudiobookPath_AuthorBookFile covers the canonical
// Audiobookshelf / Jellyfin layout: <Author>/<Book>/<file>.m4b.
// Author comes from the grandparent dir, title from the parent dir —
// not the filename, because audiobook rippers often name the file
// as "Book - Chapter N" and we want the book title for display.
func TestParseAudiobookPath_AuthorBookFile(t *testing.T) {
	title, author := parseAudiobookPath(
		"/media/Audiobooks/Brandon Sanderson/Mistborn/Mistborn - The Final Empire.m4b",
		[]string{"/media/Audiobooks"},
	)
	if author != "Brandon Sanderson" {
		t.Errorf("author = %q, want Brandon Sanderson", author)
	}
	if title != "Mistborn" {
		t.Errorf("title = %q, want Mistborn", title)
	}
}

// TestParseAudiobookPath_AuthorDirectFile covers the single-nested
// layout: <Author>/<file>.m4b. No book folder, title from filename.
func TestParseAudiobookPath_AuthorDirectFile(t *testing.T) {
	title, author := parseAudiobookPath(
		"/media/Audiobooks/Stephen King/It.m4b",
		[]string{"/media/Audiobooks"},
	)
	if author != "Stephen King" {
		t.Errorf("author = %q, want Stephen King", author)
	}
	if title != "It" {
		t.Errorf("title = %q, want It", title)
	}
}

// TestParseAudiobookPath_LooseAtRoot has no author folder at all —
// the file sits directly at the library root. Title from filename,
// author empty (the scanner falls back to tags in that case).
func TestParseAudiobookPath_LooseAtRoot(t *testing.T) {
	title, author := parseAudiobookPath(
		"/media/Audiobooks/Dune.m4b",
		[]string{"/media/Audiobooks"},
	)
	if author != "" {
		t.Errorf("author = %q, want empty (no folder context)", author)
	}
	if title != "Dune" {
		t.Errorf("title = %q, want Dune", title)
	}
}

// TestIsAudiobookFile confirms m4b routes through the audiobook
// branch and non-audio formats don't accidentally match.
func TestIsAudiobookFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"book.m4b", true},
		{"book.mp3", true},
		{"book.m4a", true}, // audible DRM-free often rips here
		{"book.flac", true},
		{"book.mkv", false}, // video, not audio
		{"book.txt", false},
		{"book.pdf", false},
	}
	for _, c := range cases {
		if got := isAudiobookFile(c.path); got != c.want {
			t.Errorf("isAudiobookFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestFileTypeForLibrary_Audiobook wires the library-type-to-item-type
// mapping: files in an audiobook library default to the "audiobook"
// item type, not "movie" or "track".
func TestFileTypeForLibrary_Audiobook(t *testing.T) {
	if got := fileTypeForLibrary("audiobook"); got != "audiobook" {
		t.Errorf("fileTypeForLibrary(audiobook) = %q, want audiobook", got)
	}
}

// TestIsMultiFileBookPath covers the layout-detection branch the
// multi-file scan flow depends on. Three layouts in, three different
// answers out — the scanner picks parent vs leaf shape from this
// boolean, so a misclassification creates a duplicate audiobook row
// per file (the bug that motivated the multi-file rewrite).
func TestIsMultiFileBookPath(t *testing.T) {
	roots := []string{"/media/Audiobooks"}
	cases := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "author / book / file is multi-file",
			path: "/media/Audiobooks/Brandon Sanderson/Mistborn/01 - Prologue.mp3",
			want: true,
		},
		{
			name: "author / file is single-file",
			path: "/media/Audiobooks/Stephen King/It.m4b",
			want: false,
		},
		{
			name: "loose at root is single-file",
			path: "/media/Audiobooks/Dune.m4b",
			want: false,
		},
		{
			name: "deeper than three levels still classified as multi only when great-grand is the root",
			// <root>/Series/<Author>/<Book>/<file> — great-grand is
			// <Author>, not the root, so this is NOT multi-file.
			path: "/media/Audiobooks/Series Name/Brandon Sanderson/Mistborn/01.mp3",
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isMultiFileBookPath(c.path, roots); got != c.want {
				t.Errorf("isMultiFileBookPath(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}
