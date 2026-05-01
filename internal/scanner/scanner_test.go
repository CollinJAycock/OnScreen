package scanner

import (
	"fmt"
	"testing"
)

func TestCleanTitle(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantTitle string
		wantYear  *int
	}{
		{
			name:      "dots with year and quality tags",
			input:     "The.Matrix.1999.1080p.BluRay.x264",
			wantTitle: "The Matrix",
			wantYear:  intPtr(1999),
		},
		{
			name:      "underscores with year and group",
			input:     "The_Lovely_Bones_2009_1080p_PMTP_WEB-DL_H264-PiRaTeS",
			wantTitle: "The Lovely Bones",
			wantYear:  intPtr(2009),
		},
		{
			name:      "parenthesised year",
			input:     "Inception.(2010).720p",
			wantTitle: "Inception",
			wantYear:  intPtr(2010),
		},
		{
			name:      "square-bracketed year",
			input:     "Hoppers [2026] 720p WEBRip-LAMA",
			wantTitle: "Hoppers",
			wantYear:  intPtr(2026),
		},
		{
			name:      "no year",
			input:     "Some.Movie.Title",
			wantTitle: "Some Movie Title",
			wantYear:  nil,
		},
		{
			name:      "spaces with year (first match wins)",
			input:     "Blade Runner 2049 2017 UHD",
			wantTitle: "Blade Runner",
			wantYear:  intPtr(2049),
		},
		{
			name:      "year too old",
			input:     "Title.1800.Stuff",
			wantTitle: "Title 1800 Stuff",
			wantYear:  nil,
		},
		{
			name:      "year too new",
			input:     "Title.2200.Stuff",
			wantTitle: "Title 2200 Stuff",
			wantYear:  nil,
		},
		{
			name:      "empty string",
			input:     "",
			wantTitle: "Unknown",
			wantYear:  nil,
		},
		{
			name:      "only year",
			input:     ".2020.",
			wantTitle: "Unknown",
			wantYear:  intPtr(2020),
		},
		{
			name:      "mixed separators",
			input:     "Movie_Title.2023 720p",
			wantTitle: "Movie Title",
			wantYear:  intPtr(2023),
		},
		{
			name:      "year boundary 1888",
			input:     "Old.Film.1888.Silent",
			wantTitle: "Old Film",
			wantYear:  intPtr(1888),
		},
		{
			name:      "year boundary 2100",
			input:     "Future.Film.2100.SciFi",
			wantTitle: "Future Film",
			wantYear:  intPtr(2100),
		},
		{
			name:      "html-escaped ampersand",
			input:     "Mike.&amp;.Nick.&amp;.Nick.&amp;.Alice.2026.1080p.WEB-DL",
			wantTitle: "Mike & Nick & Nick & Alice",
			wantYear:  intPtr(2026),
		},
		{
			name:      "html-escaped apostrophe",
			input:     "It&#39;s.A.Wonderful.Life.1946",
			wantTitle: "It's A Wonderful Life",
			wantYear:  intPtr(1946),
		},
		// QA bug 2026-05-01: shows with a [release-group] prefix never
		// matched on TMDB because the bracket leaked into the search query.
		// cleanTitle now strips the prefix so enrichment can find the show.
		{
			name:      "release-group bracket prefix",
			input:     "[ToonsHub] My Hero Academia",
			wantTitle: "My Hero Academia",
			wantYear:  nil,
		},
		{
			name:      "release-group bracket plus year",
			input:     "[ToonsHub] Frieren Beyond Journeys End 2023",
			wantTitle: "Frieren Beyond Journeys End",
			wantYear:  intPtr(2023),
		},
		{
			name:      "release-group bracket no inner space",
			input:     "[DKB]Sentai Daishikkaku",
			wantTitle: "Sentai Daishikkaku",
			wantYear:  nil,
		},
		// Year inside brackets — not a release group, not stripped.
		// "Hoppers [2026]" already covered above; this confirms the
		// new strip is anchored to a leading [text] only.
		{
			name:      "embedded brackets stay (only leading stripped)",
			input:     "Foo Bar [Director's Cut] 2015",
			wantTitle: "Foo Bar [Director's Cut]",
			wantYear:  intPtr(2015),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTitle, gotYear := cleanTitle(tt.input)
			if gotTitle != tt.wantTitle {
				t.Errorf("title: got %q, want %q", gotTitle, tt.wantTitle)
			}
			if !intPtrEqual(gotYear, tt.wantYear) {
				t.Errorf("year: got %v, want %v", fmtIntPtr(gotYear), fmtIntPtr(tt.wantYear))
			}
		})
	}
}

func TestParseFilename(t *testing.T) {
	tests := []struct {
		path      string
		wantTitle string
		wantYear  *int
	}{
		{"/media/movies/The.Matrix.1999.1080p.BluRay.mkv", "The Matrix", intPtr(1999)},
		{"/media/movies/Inception (2010).mp4", "Inception", intPtr(2010)},
		{"/media/movies/NoYear.mkv", "NoYear", nil},
		{"C:\\media\\Movie_Title_2020_WEB.mkv", "Movie Title", intPtr(2020)},
		// Blu-ray numeric filename falls back to parent dir
		{"C:\\movies\\War Machine (2026)\\00000.m2ts", "War Machine", intPtr(2026)},
		{"/media/movies/Some Movie (2020)/00001.m2ts", "Some Movie", intPtr(2020)},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			gotTitle, gotYear := parseFilename(tt.path)
			if gotTitle != tt.wantTitle {
				t.Errorf("title: got %q, want %q", gotTitle, tt.wantTitle)
			}
			if !intPtrEqual(gotYear, tt.wantYear) {
				t.Errorf("year: got %v, want %v", fmtIntPtr(gotYear), fmtIntPtr(tt.wantYear))
			}
		})
	}
}

func TestIsMediaFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"movie.mkv", true},
		{"movie.mp4", true},
		{"movie.MKV", true},
		{"movie.avi", true},
		{"movie.flac", true},
		{"movie.mp3", true},
		{"movie.txt", false},
		{"movie.jpg", true},
		{"movie.png", true},
		{"movie.gif", true},
		{"movie.webp", true},
		{"movie.exe", false},
		{"movie", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isMediaFile(tt.path); got != tt.want {
				t.Errorf("isMediaFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestFileTypeForLibrary(t *testing.T) {
	tests := []struct {
		libraryType string
		want        string
	}{
		{"movie", "movie"},
		{"show", "episode"},
		{"music", "track"},
		{"photo", "photo"},
		{"unknown", "movie"},
		{"", "movie"},
	}
	for _, tt := range tests {
		t.Run(tt.libraryType, func(t *testing.T) {
			if got := fileTypeForLibrary(tt.libraryType); got != tt.want {
				t.Errorf("fileTypeForLibrary(%q) = %q, want %q", tt.libraryType, got, tt.want)
			}
		})
	}
}

func TestBadPosterPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"relative movie", "Send Help (2026)/poster.jpg", false},
		{"relative tv", "Breaking Bad/Season 01/poster.jpg", false},
		{"just filename", "poster.jpg", false},
		{"legacy .artwork movie", ".artwork/movies/The Matrix (1999)/poster.jpg", true},
		{"legacy .artwork tv", ".artwork/tv/Good Eats (1999)/poster.jpg", true},
		{"absolute unix", "/tmp/poster.jpg", true},
		{"absolute windows", "C:\\poster.jpg", true},
		{"traversal", "../etc/poster.jpg", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := badPosterPath(tt.path); got != tt.want {
				t.Errorf("badPosterPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsAllowedPath(t *testing.T) {
	s := &Scanner{}
	tests := []struct {
		name  string
		path  string
		roots []string
		want  bool
	}{
		{
			name:  "path under root",
			path:  "/media/movies/film.mkv",
			roots: []string{"/media/movies"},
			want:  true,
		},
		{
			name:  "path is root itself",
			path:  "/media/movies",
			roots: []string{"/media/movies"},
			want:  true,
		},
		{
			name:  "path outside root",
			path:  "/etc/passwd",
			roots: []string{"/media/movies"},
			want:  false,
		},
		{
			name:  "traversal attack",
			path:  "/media/movies/../../../etc/passwd",
			roots: []string{"/media/movies"},
			want:  false,
		},
		{
			name:  "multiple roots first matches",
			path:  "/data/music/song.flac",
			roots: []string{"/media/movies", "/data/music"},
			want:  true,
		},
		{
			name:  "prefix trick no separator",
			path:  "/media/movies-extra/file.mkv",
			roots: []string{"/media/movies"},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.isAllowedPath(tt.path, tt.roots); got != tt.want {
				t.Errorf("isAllowedPath(%q, %v) = %v, want %v", tt.path, tt.roots, got, tt.want)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

func intPtrEqual(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func fmtIntPtr(p *int) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d", *p)
}
