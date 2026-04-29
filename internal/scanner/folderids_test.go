package scanner

import "testing"

func TestParseFolderIDs(t *testing.T) {
	tests := []struct {
		in       string
		wantTMDB int
		wantTVDB int
		wantIMDB string
	}{
		// TRaSH-recommended Sonarr variants:
		{"The Office (2005)", 0, 0, ""},
		{"The Office (2005) {imdb-tt0386676}", 0, 0, "tt0386676"},
		{"The Office (2005) {tvdb-73244}", 0, 73244, ""},
		{"Frieren {tmdb-209867}", 209867, 0, ""},
		// Jellyfin's bracketed long-form:
		{"The Office (2005) [tvdbid-73244]", 0, 73244, ""},
		{"Some Movie (2010) [tmdbid-12345]", 12345, 0, ""},
		{"Some Movie (2010) [imdbid-tt1234567]", 0, 0, "tt1234567"},
		// Mixed-case / whitespace tolerance:
		{"Show Name {TMDB-99}", 99, 0, ""},
		{"Show Name { tvdb-50 }", 0, 50, ""},
		// IMDb: case-folded to lowercase since "tt" is canonical lowercase:
		{"Movie {imdb-TT0123}", 0, 0, "tt0123"},
		// No markers anywhere — zero values.
		{"Just a plain folder", 0, 0, ""},
		// Multiple markers — both are picked up.
		{"Show {tmdb-1} {tvdb-2}", 1, 2, ""},
	}
	for _, tt := range tests {
		got := ParseFolderIDs(tt.in)
		if got.TMDBID != tt.wantTMDB || got.TVDBID != tt.wantTVDB || got.IMDBID != tt.wantIMDB {
			t.Errorf("ParseFolderIDs(%q) = %+v, want tmdb=%d tvdb=%d imdb=%q",
				tt.in, got, tt.wantTMDB, tt.wantTVDB, tt.wantIMDB)
		}
	}
}

func TestStripFolderIDMarkers(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"The Office (2005)", "The Office (2005)"},
		{"The Office (2005) {tvdb-73244}", "The Office (2005)"},
		{"The Office (2005) [tvdbid-73244]", "The Office (2005)"},
		{"Frieren {tmdb-209867}", "Frieren"},
		// Multiple markers — all stripped.
		{"Show {tmdb-1} {tvdb-2}", "Show"},
		// Marker mid-string — strip eats the preceding space too,
		// leaving a single-space gap.
		{"Foo {tmdb-1} Bar", "Foo Bar"},
	}
	for _, tt := range tests {
		if got := StripFolderIDMarkers(tt.in); got != tt.want {
			t.Errorf("StripFolderIDMarkers(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
