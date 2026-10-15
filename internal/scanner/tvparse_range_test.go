package scanner

import "testing"

// TestParseTVFilenameRange pins the multi-episode forms: Sonarr's repeat,
// scene, range and duplicate styles on S##E##, and the range forms on
// 1x03. Every case also runs through ParseTVFilename, which must keep
// reporting the first episode exactly as before.
func TestParseTVFilenameRange(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantTitle   string
		wantSeason  int
		wantEpisode int
		wantEnd     int
		wantOK      bool
	}{
		// ── S##E## multi-episode styles ─────────────────────────────────
		{"repeat style", "/tv/Show Name/Season 1/Show Name S01E01E02.mkv", "Show Name", 1, 1, 2, true},
		{"repeat style three episodes", "/tv/Show.Name.S01E01E02E03.mkv", "Show Name", 1, 1, 3, true},
		{"scene style", "/tv/Show Name - S01E01-E02 - Pilot.mkv", "Show Name", 1, 1, 2, true},
		{"scene style three episodes", "/tv/Show Name - S01E01-E02-E03.mkv", "Show Name", 1, 1, 3, true},
		{"range style", "/tv/Show Name - S01E01-02 - Pilot.mkv", "Show Name", 1, 1, 2, true},
		{"range style two-digit", "/tv/Show.Name.S01E09-10.1080p.WEB.mkv", "Show Name", 1, 9, 10, true},
		{"duplicate style dotted", "/tv/Show.Name.S01E01.S01E02.720p.mkv", "Show Name", 1, 1, 2, true},
		{"duplicate style dashed", "/tv/Show Name S02E05-S02E06.mkv", "Show Name", 2, 5, 6, true},
		{"repeat style then quality", "/tv/Show Name S01E01E02 720p.mkv", "Show Name", 1, 1, 2, true},
		{"lowercase scene style", "/tv/show.name.s03e07-e08.mkv", "show name", 3, 7, 8, true},
		{"gap is ignored, range covers it", "/tv/Show Name S01E01E03.mkv", "Show Name", 1, 1, 3, true},

		// ── Not a range ─────────────────────────────────────────────────
		{"single episode", "/tv/Show Name S01E05.mkv", "Show Name", 1, 5, 0, true},
		{"descending end rejected", "/tv/Show Name S01E05-03.mkv", "Show Name", 1, 5, 0, true},
		{"equal end rejected", "/tv/Show Name S01E05-E05.mkv", "Show Name", 1, 5, 0, true},
		{"year after the episode", "/tv/Show Name S01E01-2024.mkv", "Show Name", 1, 1, 0, true},
		{"resolution after the episode", "/tv/Show.Name.S01E01-1080p.mkv", "Show Name", 1, 1, 0, true},
		{"bit depth glued", "/tv/Show.Name.S01E01-10bit.mkv", "Show Name", 1, 1, 0, true},
		{"bit depth spaced", "/tv/Show Name S01E01-10 bit.mkv", "Show Name", 1, 1, 0, true},
		{"release group with digits", "/tv/Show.Name.S01E01-2HD.mkv", "Show Name", 1, 1, 0, true},
		{"spaced number is a title", "/tv/Show Name - S01E01 - 02 Title.mkv", "Show Name", 1, 1, 0, true},
		{"duplicate style other season", "/tv/Show.Name.S01E01.S02E01.mkv", "Show Name", 1, 1, 0, true},
		{"scene style then descending", "/tv/Show Name S01E02-E03-E01.mkv", "Show Name", 1, 2, 3, true},
		// A four-digit "season" is not an S##E## at all (Sonarr's daily
		// seasons), so "S2024E01-2024" never reads as a range either.
		{"four-digit season is not a range", "/tv/Show.Name.S2024E01-2024.mkv", "", 0, 0, 0, false},

		// ── 1x03 range forms ────────────────────────────────────────────
		{"cross repeated season", "/tv/Show Name 1x01-1x02.mkv", "Show Name", 1, 1, 2, true},
		{"cross range", "/tv/Show Name 1x01-02.mkv", "Show Name", 1, 1, 2, true},
		{"cross single", "/tv/Show Name 1x03.mkv", "Show Name", 1, 3, 0, true},
		{"cross spaced number is a title", "/tv/Show/Show 1x03 - 05.mkv", "Show", 1, 3, 0, true},
		{"cross other season", "/tv/Show Name 1x01-2x01.mkv", "Show Name", 1, 1, 0, true},
		{"cross descending", "/tv/Show Name 2x05-03.mkv", "Show Name", 2, 5, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, season, episode, end, ok := ParseTVFilenameRange(tt.path)
			if ok != tt.wantOK {
				t.Fatalf("ParseTVFilenameRange(%q) ok = %v, want %v", tt.path, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if title != tt.wantTitle || season != tt.wantSeason || episode != tt.wantEpisode || end != tt.wantEnd {
				t.Errorf("ParseTVFilenameRange(%q) = (%q, %d, %d, %d), want (%q, %d, %d, %d)",
					tt.path, title, season, episode, end, tt.wantTitle, tt.wantSeason, tt.wantEpisode, tt.wantEnd)
			}
			t2, s2, e2, ok2 := ParseTVFilename(tt.path)
			if !ok2 || t2 != title || s2 != season || e2 != episode {
				t.Errorf("ParseTVFilename(%q) = (%q, %d, %d, %v), want the first episode (%q, %d, %d)",
					tt.path, t2, s2, e2, ok2, title, season, episode)
			}
		})
	}
}

// TestParseEpisodeIdentity_Range checks the range reaches the identity
// the hierarchy is built from, and that the anime rules' unspaced
// double-episode form stays an orphan — a deliberate, separate rule.
func TestParseEpisodeIdentity_Range(t *testing.T) {
	id, ok := parseEpisodeIdentity("/tv/Show/Season 1/Show - S01E01-E02 - Pilot.mkv")
	if !ok || id.episode != 1 || id.episodeEnd != 2 {
		t.Fatalf("got %+v ok=%v, want episode 1 end 2", id, ok)
	}
	id, ok = parseEpisodeIdentity("/tv/Show/Season 1/Show - S01E03.mkv")
	if !ok || id.episodeEnd != 0 {
		t.Fatalf("single episode: got %+v ok=%v, want episodeEnd 0", id, ok)
	}
	// Anime spaced form: an absolute number, never a range.
	id, ok = parseEpisodeIdentity("/anime/Show/[Group] Show - 05 [1080p].mkv")
	if !ok || id.episode != 5 || id.episodeEnd != 0 {
		t.Fatalf("anime spaced: got %+v ok=%v, want episode 5 end 0", id, ok)
	}
	if _, ok := parseEpisodeIdentity("/anime/[Moozzi2] Title-01-02 [BD].mkv"); ok {
		t.Fatalf("unspaced Title-01-02 must stay an orphan")
	}
}
