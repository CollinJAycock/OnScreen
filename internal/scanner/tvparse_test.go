package scanner

import (
	"fmt"
	"testing"
)

func TestParseTVFilename(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantTitle   string
		wantSeason  int
		wantEpisode int
		wantOK      bool
	}{
		// ── S##E## patterns ──────────────────────────────────────────────────
		{
			name:        "dot separated S01E03",
			path:        "/media/tv/Show.Name.S01E03.mkv",
			wantTitle:   "Show Name",
			wantSeason:  1,
			wantEpisode: 3,
			wantOK:      true,
		},
		{
			name:        "space separated S01E03",
			path:        "/media/tv/Show Name S01E03.mkv",
			wantTitle:   "Show Name",
			wantSeason:  1,
			wantEpisode: 3,
			wantOK:      true,
		},
		{
			name:        "dash separated with episode title",
			path:        "/media/tv/Show Name - S01E03 - Episode Title.mkv",
			wantTitle:   "Show Name",
			wantSeason:  1,
			wantEpisode: 3,
			wantOK:      true,
		},
		{
			name:        "lowercase s01e03",
			path:        "/media/tv/show.name.s01e03.720p.mkv",
			wantTitle:   "show name",
			wantSeason:  1,
			wantEpisode: 3,
			wantOK:      true,
		},
		{
			name:        "S##E## no dots",
			path:        "/media/tv/ShowName S02E10.mp4",
			wantTitle:   "ShowName",
			wantSeason:  2,
			wantEpisode: 10,
			wantOK:      true,
		},
		{
			name:        "high episode number S01E100",
			path:        "/media/tv/Daily.Show.S01E100.mkv",
			wantTitle:   "Daily Show",
			wantSeason:  1,
			wantEpisode: 100,
			wantOK:      true,
		},

		// ── Folder structure patterns ────────────────────────────────────────
		{
			name:        "folder structure Season N",
			path:        "/media/tv/Show Name/Season 1/Show Name S01E03.mkv",
			wantTitle:   "Show Name",
			wantSeason:  1,
			wantEpisode: 3,
			wantOK:      true,
		},
		{
			name:        "folder structure with just S##E## filename",
			path:        "/media/tv/Breaking Bad/Season 3/S03E07.mkv",
			wantTitle:   "Breaking Bad",
			wantSeason:  3,
			wantEpisode: 7,
			wantOK:      true,
		},
		{
			name:        "folder structure Season01 no space",
			path:        "/media/tv/The Wire/Season01/The.Wire.S01E01.mkv",
			wantTitle:   "The Wire",
			wantSeason:  1,
			wantEpisode: 1,
			wantOK:      true,
		},

		// ── 1x03 patterns ────────────────────────────────────────────────────
		{
			name:        "cross pattern 1x03",
			path:        "/media/tv/Show Name 1x03.mkv",
			wantTitle:   "Show Name",
			wantSeason:  1,
			wantEpisode: 3,
			wantOK:      true,
		},
		{
			name:        "cross pattern dot separated",
			path:        "/media/tv/Show.Name.01x03.mkv",
			wantTitle:   "Show Name",
			wantSeason:  1,
			wantEpisode: 3,
			wantOK:      true,
		},

		// ── Underscore patterns ──────────────────────────────────────────────
		{
			name:        "underscore separated",
			path:        "/media/tv/Show_Name_S05E12.mkv",
			wantTitle:   "Show Name",
			wantSeason:  5,
			wantEpisode: 12,
			wantOK:      true,
		},

		// ── Edge cases ───────────────────────────────────────────────────────
		{
			name:        "movie file no episode pattern",
			path:        "/media/movies/Some.Movie.2020.mkv",
			wantTitle:   "",
			wantSeason:  0,
			wantEpisode: 0,
			wantOK:      false,
		},
		{
			name:        "Windows path",
			path:        `C:\media\tv\The Office\Season 2\The.Office.S02E05.mkv`,
			wantTitle:   "The Office",
			wantSeason:  2,
			wantEpisode: 5,
			wantOK:      true,
		},
		{
			name:        "show name with year in S##E## format",
			path:        "/media/tv/The.Flash.2014.S03E10.mkv",
			wantTitle:   "The Flash 2014",
			wantSeason:  3,
			wantEpisode: 10,
			wantOK:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTitle, gotSeason, gotEpisode, gotOK := ParseTVFilename(tt.path)
			if gotOK != tt.wantOK {
				t.Fatalf("ok: got %v, want %v", gotOK, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if gotTitle != tt.wantTitle {
				t.Errorf("title: got %q, want %q", gotTitle, tt.wantTitle)
			}
			if gotSeason != tt.wantSeason {
				t.Errorf("season: got %d, want %d", gotSeason, tt.wantSeason)
			}
			if gotEpisode != tt.wantEpisode {
				t.Errorf("episode: got %d, want %d", gotEpisode, tt.wantEpisode)
			}
		})
	}
}

func TestCleanShowTitle(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Show.Name", "Show Name"},
		{"Show_Name", "Show Name"},
		{"Show Name -", "Show Name"},
		{"  Show  Name  ", "Show Name"},
		{".Show.Name.", "Show Name"},
		{"", ""},
		{"...", ""},
		{"Show Name - ", "Show Name"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.input), func(t *testing.T) {
			got := cleanShowTitle(tt.input)
			if got != tt.want {
				t.Errorf("cleanShowTitle(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
