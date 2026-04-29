package scanner

import "testing"

// TestParseAudiobookPath_AuthorBookFile covers the canonical
// Audiobookshelf / Jellyfin layout: <Author>/<Book>/<file>.m4b.
// Author comes from the grandparent dir, title from the parent dir —
// not the filename, because audiobook rippers often name the file
// as "Book - Chapter N" and we want the book title for display.
func TestParseAudiobookPath_AuthorBookFile(t *testing.T) {
	title, author, series := parseAudiobookPath(
		"/media/Audiobooks/Brandon Sanderson/Mistborn/Mistborn - The Final Empire.m4b",
		[]string{"/media/Audiobooks"},
	)
	if author != "Brandon Sanderson" {
		t.Errorf("author = %q, want Brandon Sanderson", author)
	}
	if title != "Mistborn" {
		t.Errorf("title = %q, want Mistborn", title)
	}
	if series != "" {
		t.Errorf("series = %q, want empty (no series in this layout)", series)
	}
}

// TestParseAudiobookPath_AuthorDirectFile covers the single-nested
// layout: <Author>/<file>.m4b. No book folder, title from filename.
func TestParseAudiobookPath_AuthorDirectFile(t *testing.T) {
	title, author, series := parseAudiobookPath(
		"/media/Audiobooks/Stephen King/It.m4b",
		[]string{"/media/Audiobooks"},
	)
	if author != "Stephen King" {
		t.Errorf("author = %q, want Stephen King", author)
	}
	if title != "It" {
		t.Errorf("title = %q, want It", title)
	}
	if series != "" {
		t.Errorf("series = %q, want empty", series)
	}
}

// TestParseAudiobookPath_LooseAtRoot has no author folder at all —
// the file sits directly at the library root. Title from filename,
// author empty (the scanner falls back to tags in that case).
func TestParseAudiobookPath_LooseAtRoot(t *testing.T) {
	title, author, series := parseAudiobookPath(
		"/media/Audiobooks/Dune.m4b",
		[]string{"/media/Audiobooks"},
	)
	if author != "" {
		t.Errorf("author = %q, want empty (no folder context)", author)
	}
	if title != "Dune" {
		t.Errorf("title = %q, want Dune", title)
	}
	if series != "" {
		t.Errorf("series = %q, want empty", series)
	}
}

// TestParseAudiobookPath_AuthorSeriesBookFile covers the deepest
// supported layout: <Author>/<Series>/<Book>/<file>. Book = parent,
// series = grand, author = great-grand. The series branch is what
// lets the library grid drill author → series → book → chapter
// instead of flat-listing every book under the author.
func TestParseAudiobookPath_AuthorSeriesBookFile(t *testing.T) {
	title, author, series := parseAudiobookPath(
		"/media/Audiobooks/Brandon Sanderson/Mistborn/The Final Empire/01 - Prologue.mp3",
		[]string{"/media/Audiobooks"},
	)
	if author != "Brandon Sanderson" {
		t.Errorf("author = %q, want Brandon Sanderson", author)
	}
	if series != "Mistborn" {
		t.Errorf("series = %q, want Mistborn", series)
	}
	if title != "The Final Empire" {
		t.Errorf("title = %q, want The Final Empire", title)
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
// multi-file scan flow depends on. The series layout is one level
// deeper and is detected by isSeriesBookPath, so isMultiFileBookPath
// must return false for it — otherwise the scanner picks the wrong
// parent for the book (author instead of series).
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
			name: "author / series / book / file is series-shaped, not multi-file",
			path: "/media/Audiobooks/Brandon Sanderson/Mistborn/The Final Empire/01.mp3",
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

// TestIsSeriesBookPath covers the deepest layout. The series branch
// must NOT match the shallower three-segment layout (multi-file
// without series) — otherwise every multi-file book would be
// mis-classified as a series and the author would lose its books.
func TestIsSeriesBookPath(t *testing.T) {
	roots := []string{"/media/Audiobooks"}
	cases := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "author / series / book / file matches",
			path: "/media/Audiobooks/Brandon Sanderson/Mistborn/The Final Empire/01.mp3",
			want: true,
		},
		{
			name: "author / book / file does not match",
			path: "/media/Audiobooks/Brandon Sanderson/Mistborn/01.mp3",
			want: false,
		},
		{
			name: "author / file does not match",
			path: "/media/Audiobooks/Stephen King/It.m4b",
			want: false,
		},
		{
			name: "loose at root does not match",
			path: "/media/Audiobooks/Dune.m4b",
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isSeriesBookPath(c.path, roots); got != c.want {
				t.Errorf("isSeriesBookPath(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}
