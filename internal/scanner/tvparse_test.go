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
		{
			name:        "hyphenated title keeps its dash",
			path:        "/media/tv/Show-Name S01E05 [BD].mkv",
			wantTitle:   "Show-Name",
			wantSeason:  1,
			wantEpisode: 5,
			wantOK:      true,
		},
		// An NxM token inside a quality block is an aspect ratio, not a
		// season/episode: the match is skipped so the anime rules get
		// their turn, and a later balanced match still wins.
		{
			name:        "1x03 inside a quality block is not a season/episode",
			path:        "/media/tv/[Group] Show-01 [BD 4x3].mkv",
			wantTitle:   "",
			wantSeason:  0,
			wantEpisode: 0,
			wantOK:      false,
		},
		{
			name:        "1x03 after a bracketed aspect ratio",
			path:        "/media/tv/[BD 4x3] Show 1x03.mkv",
			wantTitle:   "Show",
			wantSeason:  1,
			wantEpisode: 3,
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

func TestDetectEpisodeKind(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		seasonNum int
		want      string
	}{
		// ── Filename keyword (most specific signal) ──────────────────────────
		{"explicit OVA in filename", "/anime/Show/Show OVA - 01.mkv", 1, "ova"},
		{"explicit ONA", "/anime/Show/[Group] Show ONA - 03.mkv", 1, "ona"},
		{"SPECIAL keyword", "/anime/Show/Show - SPECIAL - 01.mkv", 1, "special"},
		{"SP shorthand", "/tv/Show/Show.SP.01.mkv", 1, "special"},
		{"OAD (a kind of OVA)", "/anime/Show/Show OAD 02.mkv", 1, "oad"},
		{"PV (promotional video)", "/anime/Show/Show PV 01.mkv", 1, "pv"},
		{"MV (music video)", "/anime/Show/Show MV.mkv", 1, "pv"},
		{"plural OVAs in filename", "/anime/Show/Show OVAs Vol 1.mkv", 1, "ova"},
		{"case-insensitive ova", "/anime/Show/show ova ep1.mkv", 1, "ova"},

		// ── Folder fallback when filename has no kind ────────────────────────
		{"Specials folder", "/anime/Show/Specials/01.mkv", 1, "special"},
		{"OVAs folder", "/anime/Show/OVAs/01.mkv", 1, "ova"},
		{"Extras folder", "/tv/Show/Extras/03.mkv", 1, "special"},
		{"ONAs folder lower-case", "/anime/Show/onas/02.mkv", 1, "ona"},

		// ── Season 0 convention (TMDB / TheTVDB) ─────────────────────────────
		{"season 0 = special even with no other signal", "/tv/Show/Season 00/E01.mkv", 0, "special"},

		// ── Reject cases ─────────────────────────────────────────────────────
		{"regular episode", "/tv/Show/Season 1/Show S01E01.mkv", 1, ""},
		{"quality marker doesn't match", "/anime/Show/Show - 12 [1080p].mkv", 1, ""},
		// "OVA" inside a longer word should NOT match.
		{"keyword embedded in word — boundary check", "/anime/Show/Recovary - 01.mkv", 1, ""},
		{"plain anime episode", "/anime/Cowboy Bebop/Cowboy Bebop - 01.mkv", 1, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectEpisodeKind(tt.path, tt.seasonNum); got != tt.want {
				t.Errorf("DetectEpisodeKind(%q, seasonNum=%d) = %q, want %q",
					tt.path, tt.seasonNum, got, tt.want)
			}
		})
	}
}

func TestStripLeadingFansubGroups(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Single bracketed prefix
		{"[SubsPlease] Cowboy Bebop", "Cowboy Bebop"},
		{"[jaaj] Solo Leveling", "Solo Leveling"},
		// Parenthesised prefix (less common but exists)
		{"(Group) Show", "Show"},
		// Multiple consecutive prefixes (re-encode chains)
		{"[SubsPlease][Erai-raws] Show", "Show"},
		{"[A] [B] [C] Show", "Show"},
		// No prefix to strip — passthrough
		{"Cowboy Bebop", "Cowboy Bebop"},
		// Trailing brackets must NOT be eaten — quality / source markers
		// downstream needs them, or they're already past the SxxExx by
		// the time this runs.
		{"Show Name [1080p]", "Show Name [1080p]"},
		// Bracket-only input collapses so the caller falls back to the
		// folder name.
		{"[OnlyGroup]", ""},
		{"   [WithSpaces]   ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := stripLeadingFansubGroups(tt.input); got != tt.want {
				t.Errorf("stripLeadingFansubGroups(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestCleanShowTitle_SeasonMarkersStripped guards the rule that
// trailing season indicators are dropped before the title lands in
// the DB. Without this, anime cours folder-named "One Punch Man S2"
// scan as a separate top-level show from "One Punch Man" and the
// dedupe SQL can't find the pair (its normalization didn't strip
// "S2" either, so the two never share a key).
//
// Subtitle-style suffixes like "Season Subtitle" or dash-bracketed
// cour names ("-Dive to the Future-") are intentionally NOT stripped
// here — they're sometimes part of the canonical AniList title
// ("Code Geass: Lelouch of the Rebellion"). Those need a different
// treatment if we ever want to fold them.
func TestCleanShowTitle_SeasonMarkersStripped(t *testing.T) {
	cases := map[string]string{
		"One Punch Man S2":                     "One Punch Man",
		"One Punch Man S 2":                    "One Punch Man",
		"Spy x Family S2":                      "Spy x Family",
		"Fire Force Season 2":                  "Fire Force",
		"Demon Slayer 2nd Season":              "Demon Slayer",
		"Demon Slayer 3rd Season":              "Demon Slayer",
		"Some Show Cour 2":                     "Some Show",
		"Show - S2":                            "Show", // marker after a " - ": the dash goes too
		"Show - Season 2":                      "Show",
		"Show Name":                            "Show Name",                            // no marker — passthrough
		"Code Geass: Lelouch of the Rebellion": "Code Geass: Lelouch of the Rebellion", // colon subtitle preserved
		"Final Fantasy VII":                    "Final Fantasy VII",                    // roman numeral inside title preserved
	}
	for in, want := range cases {
		if got := cleanShowTitle(in); got != want {
			t.Errorf("cleanShowTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCleanShowTitle_FansubGroupStripped is a regression guard for
// the user-visible bug where a filename like
// `[jaaj] Solo Leveling S01E04 (2024) (BD 1080p AV1 AAC).mkv` landed
// in the DB as title "[jaaj] Solo Leveling". The strip happens
// inside cleanShowTitle, so any code path that funnels filename
// prefixes through cleanShowTitle (the S##E## parser and the anime
// absolute parser both do, plus the folder-name fallback) inherits
// the fix.
func TestCleanShowTitle_FansubGroupStripped(t *testing.T) {
	cases := map[string]string{
		"[jaaj] Solo Leveling":              "Solo Leveling",
		"[SubsPlease] Cowboy Bebop":         "Cowboy Bebop",
		"[Erai-raws][Trix] Attack on Titan": "Attack on Titan",
		"Solo Leveling":                     "Solo Leveling",
		"jaaj.Solo.Leveling":                "jaaj Solo Leveling", // no leading bracket — stays as-is
	}
	for in, want := range cases {
		if got := cleanShowTitle(in); got != want {
			t.Errorf("cleanShowTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseAnimeAbsoluteFilename(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantTitle   string
		wantEpisode int
		wantOK      bool
	}{
		// ── Common fansub release patterns ───────────────────────────────────
		{
			name:        "fansub group prefix + bracketed quality suffix",
			path:        "/anime/[SubsPlease] Cowboy Bebop - 12 [1080p].mkv",
			wantTitle:   "Cowboy Bebop",
			wantEpisode: 12,
			wantOK:      true,
		},
		{
			name:        "long-runner with 4-digit absolute",
			path:        "/anime/[Erai-raws] One Piece - 1071 [1080p][HDR][AAC].mkv",
			wantTitle:   "One Piece",
			wantEpisode: 1071,
			wantOK:      true,
		},
		{
			name:        "no group prefix, no quality suffix",
			path:        "/anime/Naruto - 245.mkv",
			wantTitle:   "Naruto",
			wantEpisode: 245,
			wantOK:      true,
		},
		{
			name:        "dot-separated title with dash separator",
			path:        "/anime/Attack.on.Titan - 12.mkv",
			wantTitle:   "Attack on Titan",
			wantEpisode: 12,
			wantOK:      true,
		},
		{
			name:        "single-digit episode",
			path:        "/anime/[Group] Show - 1.mkv",
			wantTitle:   "Show",
			wantEpisode: 1,
			wantOK:      true,
		},
		{
			name:        "parens-style trailing tag instead of bracket",
			path:        "/anime/[SubsPlease] Show - 24 (HDR).mkv",
			wantTitle:   "Show",
			wantEpisode: 24,
			wantOK:      true,
		},
		{
			name:        "glued revision token on the spaced form",
			path:        "/anime/[SubsPlease] Show - 01v2 (1080p) [ABCD1234].mkv",
			wantTitle:   "Show",
			wantEpisode: 1,
			wantOK:      true,
		},

		// The rows from here down use the positional form of the same
		// struct — this table is long, and five lines per row would bury
		// the pattern each block is pinning.

		// ── Spaced dash — widened forms and their guards ─────────────────────
		// Uppercase revision letter, and a glued revision binding to the
		// FIRST number when a later " - NN" follows (was ("Show - 12v3", 5)
		// before the token was accepted; the new reading is the right one).
		{"uppercase revision letter", "[SubsPlease] Show - 01V2 (1080p).mkv", "Show", 1, true},
		{"glued revision binds to the first number", "Show - 12v3 - 05.mkv", "Show", 12, true},
		{"space before the revision token, spaced form", "[SubsPlease] Show - 01 v2 (1080p).mkv", "Show", 1, true},
		// A revision token on a single-digit number is a score / format,
		// not a revision — these were rejected before the token existed.
		{"NvN score is not a revision", "Rocket League - 3v3 Finals [1080p].mkv", "", 0, false},
		{"NvN score, uppercase V", "Rocket League - 3V3 Finals [1080p].mkv", "", 0, false},
		{"NvN format is not a revision", "Overwatch League - 5v5 Grand Finals (2023).mkv", "", 0, false},
		{"1vN format is not a revision", "MrBeast - 1v100 Challenge.mkv", "", 0, false},
		// The spaced rule is not retried at a later " - NN" after a guard
		// rejection: a score followed by a real episode number stays an
		// orphan rather than show "Show - 3v3" (accepted loss).
		{"accepted loss: score then episode, no retry", "Show - 3v3 - 05.mkv", "", 0, false},
		// A glued revision equal to the episode number is a score at any
		// width, not a re-release.
		{"NvN score, two digits", "Show - 10v10 Custom Games.mkv", "", 0, false},
		{"NvN score, two digits, nothing after", "Show - 12v12.mkv", "", 0, false},
		// Both anime rules honour cleanTitle's year window: a spaced
		// hyphen-year is the movie parser's ("Altered Carbon - 2018"), not
		// episode 2018.
		{"spaced year is not an episode", "Show - 2019.mkv", "", 0, false},
		{"spaced hyphen-year movie name", "Altered Carbon - 2018.mkv", "", 0, false},
		{"underscore-spaced year is not an episode", "Show_-_2019_[BD].mkv", "", 0, false},
		// A fractional number is a recap and must not land on the real
		// episode; the tight rule already refuses "Title-12.5".
		{"recap episode, SubsPlease", "[SubsPlease] Show - 12.5 (1080p) [ABCD1234].mkv", "", 0, false},
		{"recap episode, revised", "[Erai-raws] Show - 12.5v2 [1080p][HEVC].mkv", "", 0, false},
		{"dot then a word after the number is still an episode", "Show - 12.Episode.Title.mkv", "Show", 12, true},
		// Underscore-spaced dash: the 2008-2014 fansub archives (UTW, Doki,
		// Chihiro, gg) replaced every space with "_".
		{"underscore-spaced dash, UTW", "[UTW]_Fate_Zero_-_01_[BD][h264-1080p_FLAC][A1B2C3D4].mkv", "Fate Zero", 1, true},
		{"underscore-spaced dash, Doki", "[Doki]_Clannad_-_01_(1920x1080_Hi10P_BD_FLAC)_[A1B2C3D4].mkv", "Clannad", 1, true},
		{"underscore-spaced dash, Chihiro", "[Chihiro]_Kanon_-_01_[1280x720_Blu-ray_FLAC][A1B2C3D4].mkv", "Kanon", 1, true},
		{"underscore-spaced dash, no group", "Fate_Zero_-_01.mkv", "Fate Zero", 1, true},
		// An episode word between the dash and the number.
		{"E-prefixed number, Reaktor", "[Reaktor] Dungeon Meshi - E01 [1080p][x265][10-bit].mkv", "Dungeon Meshi", 1, true},
		{"Ep-prefixed number", "Show Name - Ep 03 [1080p].mkv", "Show Name", 3, true},
		{"Ep.-prefixed number", "Show Name - Ep.03.mkv", "Show Name", 3, true},
		{"Episode-prefixed number", "Show Name - Episode 03.mkv", "Show Name", 3, true},
		// A bare "E" only marks a zero-padded number — "E3" is the games
		// expo — and must be glued to the digits. "Ep 3" / "Episode3" are
		// not bare and keep parsing.
		{"bare E on a single digit is the games expo", "Giant Bomb - E3 2019 Day 1.mkv", "", 0, false},
		{"bare E on a single digit, with quality", "IGN - E3 2019 Press Conference [1080p].mkv", "", 0, false},
		{"bare E on a single digit, more words", "Show - E3 2019 Coverage.mkv", "", 0, false},
		{"bare E must be glued to the digits", "Show - E 2019.mkv", "", 0, false},
		{"Ep word on a single digit", "Show - Ep 3.mkv", "Show", 3, true},
		{"Episode word glued to a single digit", "Show - Episode3.mkv", "Show", 3, true},
		// A title that ran into the quality block is not a title; the
		// tight rule then reads the name correctly (before either rule had
		// guards this parsed as show "Dungeon Meshi-01 (x265", episode 10).
		{"spaced number inside a paren quality block", "[Group] Dungeon Meshi-01 (x265 - 10 bit).mkv", "Dungeon Meshi", 1, true},
		{"spaced number inside a bracket quality block", "[Group] Dungeon Meshi-01 [BD 1080p - 10 Bit].mkv", "Dungeon Meshi", 1, true},
		{"spaced number inside a quality block, no tight fallback", "Show Name (x265 - 10 bit).mkv", "", 0, false},
		// A Sonarr-style unspaced name whose episode title starts with a
		// single unpadded digit matches the spaced rule at the wrong dash
		// (show "Dungeon Meshi-13", episode 7); spacedMatchAtWrongDash
		// prefers the tight rule's reading. Whitespace and underscore
		// spacing alike, a quality block between the number and the
		// title, and an explicit episode word all count.
		{"Sonarr-style title starting with a number", "Dungeon Meshi-13 - 7 Days [BD].mkv", "Dungeon Meshi", 13, true},
		{"Sonarr-style title starting with a number, group and quality", "[Moozzi2] Dungeon Meshi-13 - 7 Days [BD 1920x1080].mkv", "Dungeon Meshi", 13, true},
		{"Sonarr-style title starting with a number, no folder", "[Group] Show-01 - 2 Hours Later [BD].mkv", "Show", 1, true},
		{"Sonarr-style title starting with an ordinal", "Dungeon Meshi-13 - 100th Day [BD].mkv", "Dungeon Meshi", 13, true},
		{"Sonarr-style title starting with a number, spaced first number", "Dungeon Meshi - 13 - 7 Days [BD].mkv", "Dungeon Meshi", 13, true},
		{"Sonarr-style title starting with a number, underscore-spaced", "[Group]_Show-01_-_7_Days_[BD].mkv", "Show", 1, true},
		{"Sonarr-style title starting with a number, underscore-spaced, two-word show", "[Group]_Dungeon_Meshi-13_-_7_Days_[BD].mkv", "Dungeon Meshi", 13, true},
		{"Sonarr-style title starting with a number, quality block before the title", "Dungeon Meshi-13 [BD 1920x1080 x265-10Bit 2Audio] - 7 Days.mkv", "Dungeon Meshi", 13, true},
		{"Sonarr-style episode word after the unspaced number", "Dungeon Meshi-13 - Episode 7 [BD].mkv", "Dungeon Meshi", 13, true},
		// A hyphen-number title's spaced form keeps parsing as itself:
		// its zero-padded number IS the episode, whatever follows it — an
		// anchor, a revision, a marker word, a bare word. This is the
		// documented workaround for the unspaced forms rejected further
		// down, and what main did with every one of these names.
		{"hyphen-number title, spaced episode", "Catch-22 - 01 [BD].mkv", "Catch-22", 1, true},
		{"hyphen-number title, spaced episode, three digits", "Mob-Psycho-100 - 05 [BD].mkv", "Mob-Psycho-100", 5, true},
		{"hyphen-year title, spaced episode", "Ghost in the Shell SAC-2045 - 01.mkv", "Ghost in the Shell SAC-2045", 1, true},
		{"hyphen-digit title, spaced episode", "Ranma 1-2 - 01 [BD].mkv", "Ranma 1-2", 1, true},
		{"two-digit title tail, spaced episode", "Digimon Adventure 02 - 01 [BD].mkv", "Digimon Adventure 02", 1, true},
		{"hyphen-number title, finale marker after the number", "R-15 - 01 END [BD].mkv", "R-15", 1, true},
		{"hyphen-number title, kind word after the number", "R-15 - 13 OVA [BD].mkv", "R-15", 13, true},
		{"hyphen-number title, spaced revision after the number", "R-15 - 01 v2 [BD].mkv", "R-15", 1, true},
		{"hyphen-number title, spaced revision then episode title", "R-15 - 01 v2 - Pilot.mkv", "R-15", 1, true},
		{"hyphen-number title, finale marker, no quality block", "Catch-22 - 01 END.mkv", "Catch-22", 1, true},
		{"hyphen-number title, spaced revision, no folder", "Catch-22 - 01 v2 [BD].mkv", "Catch-22", 1, true},
		{"hyphen-number title, three digits, finale marker", "Mob-Psycho-100 - 05 END [BD].mkv", "Mob-Psycho-100", 5, true},
		{"hyphen-number title, three digits, spaced revision", "Mob-Psycho-100 - 05 v2.mkv", "Mob-Psycho-100", 5, true},
		{"hyphen-year title, finale marker, Moozzi2", "[Moozzi2] Ghost in the Shell SAC-2045 - 12 END (BD 1920x1080 x.264 FLAC).mkv", "Ghost in the Shell SAC-2045", 12, true},
		{"hyphen-year title, finale marker then episode title", "Ghost in the Shell SAC-2045 - 12 END - Episode Title [BD].mkv", "Ghost in the Shell SAC-2045", 12, true},
		{"hyphen-number title, bare word after a zero-padded number", "Catch-22 - 01 Pilot.mkv", "Catch-22", 1, true},
		{"hyphen-number title, bare word after an unpadded two-digit number", "Catch-22 - 12 Pilot.mkv", "Catch-22", 12, true},
		{"hyphen-number title, bare token after the number", "Catch-22 - 01 BD.mkv", "Catch-22", 1, true},
		// The hand-off never orphans: when the tight re-read fails (here
		// on the year window) the spaced reading stands.
		{"hyphen-year title, unpadded single digit then a word", "Ghost in the Shell SAC-2045 - 1 Word.mkv", "Ghost in the Shell SAC-2045", 1, true},
		// Residuals of the hand-off, pinned so a change to them is a
		// deliberate one: an unpadded two-or-more-digit number followed by
		// a word is ambiguous and keeps the spaced reading (main's), and
		// an unpadded single-digit episode of a hyphen-number show with no
		// second dash is read at the title's own hyphen.
		{"residual: Sonarr-style title starting with a two-digit number", "Dungeon Meshi-13 - 24 Hours [BD].mkv", "Dungeon Meshi-13", 24, true},
		{"residual: quality block then a two-digit number", "[Group] Show-01 [BD] - 10 Bit.mkv", "Show-01 [BD]", 10, true},
		{"residual: unpadded single digit then a marker word", "Dungeon Meshi-13 - 7 END [BD].mkv", "Dungeon Meshi-13", 7, true},
		{"residual: hyphen-number title, unpadded single-digit episode then a word", "Catch-22 - 1 Pilot.mkv", "Catch", 22, true},
		// A glued revision after an E-prefixed number, and a season marker
		// after a spaced dash ("Show - S2 - 01" used to leave "Show -"
		// behind as the title).
		{"E-prefixed number with a glued revision", "Show - E01v2 [BD].mkv", "Show", 1, true},
		{"season marker between two spaced dashes", "/anime/Show/Show - S2 - 01.mkv", "Show", 1, true},
		// A bare episode word as the whole title names the episode, not a
		// show called "Episode": folder fallback, as for a tag-only stem.
		{"bare Episode word as the title, spaced", "/anime/Show Name/Episode - 01.mkv", "Show Name", 1, true},
		// A bracketed subtitle inside the title is fine here (the tight
		// rule's title can't contain one — see its documented divergence).
		{"bracketed subtitle inside the title, spaced", "[Moozzi2] Fate stay night [Unlimited Blade Works] - 01 (BD 1920x1080).mkv", "Fate stay night [Unlimited Blade Works]", 1, true},

		// ── Unspaced dash (BD-rip / Moozzi2 form) ────────────────────────────
		// QA orphans: every one of these reached the "No parser matched"
		// fallback and became a parentless flat episode. The tight rule
		// (tvAnimeTightDashRE) owns them now.
		{"Moozzi2 BD-rip, bracketed quality block", "/anime/Delicious in Dungeon/Season 1/[Moozzi2] Dungeon Meshi-13 [BD 1920x1080 x265-10Bit 2Audio].mkv", "Dungeon Meshi", 13, true},
		{"Moozzi2 finale marker END", "/anime/Delicious in Dungeon/Season 1/[Moozzi2] Dungeon Meshi-24 END [BD 1920x1080 x265-10Bit 2Audio].mkv", "Dungeon Meshi", 24, true},
		{"Moozzi2 Mushishi", "[Moozzi2] Mushishi-01 [BD 1920x1080 x264 FLAC].mkv", "Mushishi", 1, true},
		{"Moozzi2 KILL la KILL", "[Moozzi2] KILL la KILL-12 [BD 1920x1080 x265-10Bit 2Audio].mkv", "KILL la KILL", 12, true},
		{"Moozzi2 Clannad After Story finale", "[Moozzi2] Clannad After Story-22 END [BD 1920x1080 x265-10Bit 2Audio].mkv", "Clannad After Story", 22, true},
		{"paren quality block", "[Moozzi2] Dungeon Meshi-13 (BD 1920x1080 x265-10Bit 2Audio).mkv", "Dungeon Meshi", 13, true},
		{"no space after the group tag", "[Moozzi2]Dungeon Meshi-13 [BD].mkv", "Dungeon Meshi", 13, true},
		{"no space before the quality bracket", "[Moozzi2] Dungeon Meshi-13[BD 1920x1080].mkv", "Dungeon Meshi", 13, true},
		{"CRC hash suffix", "[Moozzi2] Dungeon Meshi-13 [BD 1920x1080][A1B2C3D4].mkv", "Dungeon Meshi", 13, true},
		{"zero-padded to three digits", "[Moozzi2] Dungeon Meshi-007 [BD].mkv", "Dungeon Meshi", 7, true},
		{"no group tag", "Dungeon Meshi-13 [BD 1920x1080 x265-10Bit 2Audio].mkv", "Dungeon Meshi", 13, true},
		{"dash inside the group tag", "[Erai-raws] Show-01 [1080p][Multiple Subtitle][ABCD1234].mkv", "Show", 1, true},
		{"stacked group tags", "[A][B] Title-13 [BD].mkv", "Title", 13, true},
		{"stacked group tags with spaces", "[A] [B] Title-13 [BD].mkv", "Title", 13, true},
		{"dash then end of stem", "Title-07.mkv", "Title", 7, true},
		{"dash then paren", "Title-07 (1080p).mkv", "Title", 7, true},
		{"4-digit absolute outside the year window", "Title-1071 [1080p].mkv", "Title", 1071, true},
		{"glued revision token", "Title-13v2 [BD].mkv", "Title", 13, true},
		{"glued revision token, uppercase", "Title-13V2 [BD].mkv", "Title", 13, true},
		{"space before the revision token, tight form", "Title-13 v2 [BD].mkv", "Title", 13, true},
		{"revision then finale marker", "[Moozzi2] Dungeon Meshi-24v2 END [BD].mkv", "Dungeon Meshi", 24, true},
		{"revision after the finale marker", "Title-13 END v2 [BD].mkv", "Title", 13, true},
		{"revision after the finale marker, Moozzi2", "[Moozzi2] Dungeon Meshi-24 END v2 [BD 1920x1080].mkv", "Dungeon Meshi", 24, true},
		{"finale marker mixed case", "Title-13 End [BD].mkv", "Title", 13, true},
		{"finale marker FIN", "Title-13 FIN [BD].mkv", "Title", 13, true},
		{"finale marker FINAL", "Title-13 FINAL [BD].mkv", "Title", 13, true},
		{"finale marker then end of stem", "Title-13 END.mkv", "Title", 13, true},
		{"finale marker glued to the number", "Title-13END [BD].mkv", "Title", 13, true},
		// Underscore-everything spellings: RE2 counts "_" as a word
		// character, so a marker word needs the underscore accepted
		// explicitly after it ("Title-13 Ending" stays rejected below).
		{"finale marker then underscore", "Title-13 END_[BD].mkv", "Title", 13, true},
		{"underscore before and after the finale marker", "Title-13_END_[BD].mkv", "Title", 13, true},
		{"underscore before the finale marker", "Title-13_END [BD].mkv", "Title", 13, true},
		{"underscore before and after the revision", "Title-13_v2_[BD].mkv", "Title", 13, true},
		// Non-ASCII spaces (no-break U+00A0, ideographic U+3000) read as
		// plain spaces; RE2's \s would not cross them.
		{"no-break space before the quality bracket", "[Moozzi2] Dungeon Meshi-13\u00a0[BD].mkv", "Dungeon Meshi", 13, true},
		{"ideographic space before the quality bracket", "進撃の巨人-13\u3000[BD].mkv", "進撃の巨人", 13, true},
		{"no-break space inside the title", "[Moozzi2] Dungeon\u00a0Meshi-13 [BD].mkv", "Dungeon Meshi", 13, true},
		{"tab before the quality bracket", "[Moozzi2] Dungeon Meshi-13\t[BD].mkv", "Dungeon Meshi", 13, true},
		// Episode-kind words after the number: Moozzi2's KILL la KILL BD-BOX
		// ends "- 24 END" then "- 25 OVA" (nyaa.si/view/907923). The spaced
		// control is the original release name.
		{"Moozzi2 OVA after the number", "[Moozzi2] KILL la KILL-25 OVA [BD 1920x1080 x.264 FLACx2].mkv", "KILL la KILL", 25, true},
		{"Moozzi2 OVA after the number, spaced original", "[Moozzi2] KILL la KILL - 25 OVA (BD 1920x1080 x.264 FLACx2).mkv", "KILL la KILL", 25, true},
		{"revision after a kind word", "[Moozzi2] KILL la KILL-25 OVA v2 [BD].mkv", "KILL la KILL", 25, true},
		{"PV after the number", "Title-13 PV [BD].mkv", "Title", 13, true},
		{"plural OVAs after the number", "Title-13 OVAs [BD].mkv", "Title", 13, true},
		{"SP after the number", "Title-13 SP [BD].mkv", "Title", 13, true},
		{"Special after the number", "Title-13 Special [BD].mkv", "Title", 13, true},
		{"TV after the number", "Title-13 TV [BD].mkv", "Title", 13, true},
		{"RAW after the number", "[Group] Title-13 RAW [1080p].mkv", "Title", 13, true},
		{"finale then kind word", "Title-24 END TV [BD].mkv", "Title", 24, true},
		// A spaced dash after the number is a Sonarr-style episode title.
		{"episode title after the number", "Dungeon Meshi-01 - Hot Pot.mkv", "Dungeon Meshi", 1, true},
		{"episode title after the number, with group and quality", "[Moozzi2] Dungeon Meshi-01 - Hot Pot [BD].mkv", "Dungeon Meshi", 1, true},
		{"episode title after the number, bracketed", "Show-01 - Episode Title [BD].mkv", "Show", 1, true},
		{"underscore-spaced episode title after the number", "[Group]_Show-01_-_Episode_Title_[BD].mkv", "Show", 1, true},
		// An episode word glued to the dash is consumed, not kept as the
		// last word of the show title.
		{"Ep word before the dash", "Show Name Ep-03 [1080p].mkv", "Show Name", 3, true},
		{"Episode word before the dash", "Show Name Episode-05 [WEB].mkv", "Show Name", 5, true},
		{"Ep word after a spaced dash", "Show Name - Ep-03.mkv", "Show Name", 3, true},
		{"Ep word in a hyphenated title", "Show-Name-Ep-01.mkv", "Show-Name", 1, true},
		{"word ending in ep is a title, not an episode word", "Sleep-01.mkv", "Sleep", 1, true},
		// Tag-only stems fall back to the folder name, as the spaced rule
		// and the S##E## rule already do. So does a stem whose title is
		// nothing but an episode word: "Episode-01" names the episode, not
		// a show called "Episode".
		{"bracket group only, title from folder", "/anime/Dungeon Meshi/[Moozzi2]-13 [BD].mkv", "Dungeon Meshi", 13, true},
		{"paren group only, title from folder", "/anime/Show Folder/(Group)-13.mkv", "Show Folder", 13, true},
		{"number only, title from folder", "/anime/Dungeon Meshi/Season 1/-13 [BD].mkv", "Dungeon Meshi", 13, true},
		{"bare Ep word as the title", "/anime/Show Name/Ep-01.mkv", "Show Name", 1, true},
		{"bare Episode word as the title", "/anime/Show Name/Episode-01.mkv", "Show Name", 1, true},
		{"bare Episode word after a group tag", "/anime/Show Name/[Group] Episode-12 [1080p].mkv", "Show Name", 12, true},
		{"bare Ep word after a group tag", "/anime/Show Name/[Group] Ep-12 [1080p].mkv", "Show Name", 12, true},
		{"leading whitespace before the group tag", " [Moozzi2] Dungeon Meshi-13 [BD].mkv", "Dungeon Meshi", 13, true},
		{"dot-separated title", "Show.Name-13.mkv", "Show Name", 13, true},
		{"underscore-separated title", "Show_Name-13.mkv", "Show Name", 13, true},
		{"underscore before the quality bracket", "Title-13_[BD].mkv", "Title", 13, true},
		{"underscore-everything name", "[Group]_Show_Name-13_[BD].mkv", "Show Name", 13, true},
		{"dot-everything name", "[Group].Show.Name-13.[BD].mkv", "Show Name", 13, true},
		{"paren group tag", "(Group) Title-13 [BD].mkv", "Title", 13, true},
		{"year in parens is part of the title", "Kanon (2006)-01 [BD].mkv", "Kanon (2006)", 1, true},
		{"year in parens, longer title", "Hunter x Hunter (2011)-13 [BD].mkv", "Hunter x Hunter (2011)", 13, true},
		{"year in parens, Moozzi2 Dororo", "[Moozzi2] Dororo (2019)-20 [BD].mkv", "Dororo (2019)", 20, true},
		// A bare series year at the end of the title is fine as long as the
		// episode isn't the NEXT year's tail with nothing after it (the
		// season-range reject below); a revision or marker word after the
		// number says episode, since a season-range file never carries one.
		{"title ending in a bare year", "Dororo 2019-01 [BD].mkv", "Dororo 2019", 1, true},
		{"title ending in a bare year, two-digit episode", "Show Name 2024-13 [BD].mkv", "Show Name 2024", 13, true},
		{"title ending in a bare year, next-year episode with a finale marker", "Dororo 2019-20 END [BD].mkv", "Dororo 2019", 20, true},
		{"title ending in a bare year, next-year episode with a revision", "Hunter x Hunter 2011-12v2 [BD].mkv", "Hunter x Hunter 2011", 12, true},
		{"title ending in a bare year, next-year episode with a spaced revision", "Show Name 2019-20 v2 [BD].mkv", "Show Name 2019", 20, true},
		// A four-digit title tail of a different width than the episode is
		// a title, not a range ("Gundam 0083" is the show).
		{"title ending in a four-digit number", "Gundam 0083-01 [BD].mkv", "Gundam 0083", 1, true},
		// An aspect ratio inside the quality block is skipped by the 1x03
		// rule (see TestParseTVFilename) and the name lands here.
		{"aspect ratio inside the quality block", "[Group] Show-01 [BD 4x3].mkv", "Show", 1, true},
		// Two bracket groups between the unspaced number and a
		// Sonarr-style " - 7 Days": spacedMatchAtWrongDash strips them all
		// before looking for the tight marker, not just the last one.
		{"stacked quality and CRC groups before a Sonarr title", "[Group] Show-01 [BD][A1B2C3D4] - 7 Days.mkv", "Show", 1, true},
		// Dot-everything spelling of a finale marker / revision after the
		// number, the one separator style the suffix used to refuse.
		{"dot-spelled finale marker", "Title-13.END.[BD].mkv", "Title", 13, true},
		{"dot-spelled finale marker, dotted group name", "[Group].Show.Name-24.END.[BD].mkv", "Show Name", 24, true},
		{"dot-spelled revision", "Title-13.v2.mkv", "Title", 13, true},
		// A resolution written with spaces after the number is not a
		// movie year: the group carries another number.
		{"spaced resolution after the number", "[Moozzi2] Dungeon Meshi-13 (1920 x 1080 x265).mkv", "Dungeon Meshi", 13, true},
		{"Ep. word before the dash", "Show Name Ep.-01.mkv", "Show Name", 1, true},
		{"space before the dash only", "Title -13 [BD].mkv", "Title", 13, true},
		{"hyphenated title Re-Zero", "Re-Zero-13 [BD].mkv", "Re-Zero", 13, true},
		{"hyphenated title K-On!", "K-On!-05 [BD].mkv", "K-On!", 5, true},
		{"hyphenated title, no quality block", "Kill-la-Kill-12.mkv", "Kill-la-Kill", 12, true},
		{"title starting with digits", "86-Eighty-Six-01.mkv", "86-Eighty-Six", 1, true},
		{"title ending in a spaced number", "Mob Psycho 100-05 [BD].mkv", "Mob Psycho 100", 5, true},
		// Residuals, pinned so a change is deliberate: a one-digit title
		// tail cannot be told from a compact season-episode ("Show Name
		// 2-01" is either show "Show Name 2" or S2E01, and "Steins;Gate
		// 0-05" must keep parsing), and a kind word before the dash names
		// a separate AniList entry (see tightDashJunkTailRE).
		{"residual: one-digit title tail reads as the title", "Show Name 2-01 [BD].mkv", "Show Name 2", 1, true},
		{"kind word before the dash is part of the show", "Show Name OVA-01 [BD].mkv", "Show Name OVA", 1, true},
		{"dot inside the title", "Dr. Stone-05 [BD].mkv", "Dr Stone", 5, true},
		{"all-caps hyphenated title", "ID-INVADED-03.mkv", "ID-INVADED", 3, true},
		{"semicolon and trailing digit in the title", "Steins;Gate 0-05 [BD].mkv", "Steins;Gate 0", 5, true},
		{"season token inside the title", "Code Geass R2-13 [BD].mkv", "Code Geass R2", 13, true},
		{"cour name inside the title", "Attack on Titan Final Season Part 2-01 [BD].mkv", "Attack on Titan Final Season Part 2", 1, true},
		{"non-ASCII title", "進撃の巨人-13 [BD].mkv", "進撃の巨人", 13, true},
		// Year-window edges (the window itself is pinned under the rejects),
		// including the edge of the "(YYYY)" / "[YYYY]"-after-the-number
		// movie check.
		{"just below the year window", "Detective Conan-1887 [BD].mkv", "Detective Conan", 1887, true},
		{"just above the year window", "Title-2101 [BD].mkv", "Title", 2101, true},
		{"movie year outside the window is not a year", "Title-07 (1080).mkv", "Title", 7, true},
		{"bracketed movie year outside the window is not a year", "Title-07 [1080].mkv", "Title", 7, true},
		// Residuals, pinned so a change to them is a deliberate one: a
		// non-year group before a movie year is not looked through, and a
		// descending pair of different widths is not a range.
		{"residual: country tag before the movie year", "Catch-22 (US) (1970).mkv", "Catch", 22, true},
		{"residual: descending pair of different widths", "Celtics vs Mavericks 107-89 [1080p].mkv", "Celtics vs Mavericks 107", 89, true},
		// More residuals of the guards' exact widths: the bare-pair clause
		// of the title-tail guard wants two digits after the E, the
		// ascending-pair check is strict, and the episode word in front of
		// the dash is consumed even when it is a title's last word.
		{"residual: one-digit E-prefixed title tail", "Show Name E1-02 [BD].mkv", "Show Name E1", 2, true},
		{"residual: equal pair is not ascending", "Show 100-100 [BD].mkv", "Show 100", 100, true},
		{"residual: title ending in the word Episode", "The Final Episode-01.mkv", "The Final", 1, true},
		// Accepted cost of the title-tail guard being two digits wide: a
		// title ending in ONE digit still parses, so the compact
		// season-episode form "1-13" reads as show "Show Name 1". Guarding
		// it would break "Steins;Gate 0-05" above.
		{"accepted cost: one-digit title tail", "Show Name 1-13 [BD].mkv", "Show Name 1", 13, true},

		// ── Unspaced dash — rejects ──────────────────────────────────────────
		// The "-10" in "x265-10Bit" is a codec token; the title capture
		// can't cross a bracket to reach it, and the strict trailing
		// anchor refuses the rest of the scene vocabulary.
		{"codec token inside a bracketed quality block", "[Group] Show Name [BD 1920x1080 x265-10Bit 2Audio].mkv", "", 0, false},
		{"codec token inside a paren quality block", "[Group] Show Name (BD 1920x1080 x265-10Bit 2Audio).mkv", "", 0, false},
		{"scene codec token H.264-AAC", "Show.Name.1080p.H.264-AAC.mkv", "", 0, false},
		{"WEB-DL source tag", "Show Name [WEB-DL 1080p].mkv", "", 0, false},
		{"hyphen-attached quality", "Show Name-1080p.mkv", "", 0, false},
		{"scene group suffix -2HD", "Show.Name.2019.1080p.BluRay.x264-2HD.mkv", "", 0, false},
		{"scene codec token x265-10bit-GROUP", "Show.Name.1080p.BluRay.x265-10bit-GROUP.mkv", "", 0, false},
		// Bare quality token after the number: unsupported by the tight
		// rule on purpose — its strict anchor is what keeps the codec
		// tokens above out, and the words it does accept after the number
		// are a closed list. The spaced form of this name still parses.
		{"bare quality token after the number (documented divergence)", "Title-13 1080p.mkv", "", 0, false},
		{"dot-attached quality token after the number (documented divergence)", "Show.Name-13.1080p.mkv", "", 0, false},
		{"unlisted word after the number", "Title-13 Ending [BD].mkv", "", 0, false},
		{"listed word followed by more words", "Title-13 Special Edition [BD].mkv", "", 0, false},
		// The lazy title must not walk past a number whose anchor failed
		// to a later codec token that has one: "Show-13 x265-10 [BD]" is
		// not episode 10 of "Show-13 x265". Orphans without the tight
		// rule, and they stay orphans.
		{"codec token with an anchor after a bare quality token", "Show-13 x265-10 [BD].mkv", "", 0, false},
		{"codec token with an anchor, dotted", "Show.Name-13.x265-10.mkv", "", 0, false},
		{"codec token with an anchor, group and resolution", "[Group] Show-13 1080p x265-10 [BD].mkv", "", 0, false},
		{"codec token with an anchor after a revised number", "Show-13v2 x265-10 [BD].mkv", "", 0, false},
		{"two unspaced numbers", "Title-13 vs Title-14.mkv", "", 0, false},
		// An episode word glued AFTER the dash is not read by this rule
		// (documented: before the dash only; the spaced "- E13" is).
		{"E glued after the dash (documented divergence)", "Dungeon Meshi-E13 [BD].mkv", "", 0, false},
		{"Ep glued after the dash (documented divergence)", "Dungeon Meshi-Ep13 [BD].mkv", "", 0, false},
		// A bracketed subtitle inside the title can't be a title here
		// (documented divergence — the spaced form above parses).
		{"bracketed subtitle inside the title (documented divergence)", "[Moozzi2] Fate stay night [Unlimited Blade Works]-01 [BD 1920x1080 x.264 FLAC].mkv", "", 0, false},
		// Years: a hyphen-attached year belongs to the movie parser
		// ("Altered Carbon-2018" is a QA fixture in scanner_test.go).
		// Same 1888..2100 window as cleanTitle; the boundary accepts above
		// pin its edges.
		{"hyphen-attached year", "Show Name-2024.mkv", "", 0, false},
		{"hyphen-attached year, QA fixture shape", "Altered Carbon-2018.mkv", "", 0, false},
		{"hyphenated title with a year", "Blade-Runner-2049.mkv", "", 0, false},
		{"episode inside the year window (accepted cost)", "One Piece-1999 [BD].mkv", "", 0, false},
		{"year window lower bound 1888", "Title-1888 [BD].mkv", "", 0, false},
		{"year window upper bound 2100", "Title-2100 [BD].mkv", "", 0, false},
		// Season ranges: a title ending in a year followed by the NEXT
		// year's last two digits is the per-file naming of sports leagues
		// and broadcast-year archives, not episode 24 of "Premier League
		// 2023". Every one of these was an orphan before the tight rule.
		{"season range, spaced episode title", "Premier League 2023-24 - Arsenal vs Chelsea.mkv", "", 0, false},
		{"season range, quality block", "NBA 2023-24 [1080p].mkv", "", 0, false},
		{"season range, broadcast years", "Top Gear 2010-11 - Middle East Special.mkv", "", 0, false},
		{"season range, end of stem", "EPL 2023-24.mkv", "", 0, false},
		{"season range, generic", "Show Name 2019-20 - Episode Title.mkv", "", 0, false},
		{"season range, last century", "Sherlock Holmes 1984-85 - The Speckled Band.mkv", "", 0, false},
		{"season range, four-digit second year", "Show Name 2019-2020 [BD].mkv", "", 0, false},
		{"season range, digits-only stem", "/anime/Premier League/2023-24 - Final.mkv", "", 0, false},
		{"season range, zero-padded second year", "Show 2008-09 - Title.mkv", "", 0, false},
		// The documented cost of the season-range reject: an
		// unparenthesised series year whose episode is the next year's
		// tail, with nothing after the number. One episode per such show.
		{"accepted cost: series year then next-year episode, bare", "Dororo 2019-20 [BD].mkv", "", 0, false},
		{"accepted cost: series year then next-year episode, Hunter x Hunter", "Hunter x Hunter 2011-12 [BD].mkv", "", 0, false},
		{"accepted cost: series year then next-year episode, with episode title", "Hunter x Hunter 2011-12 - Episode Title [BD].mkv", "", 0, false},
		// "(YYYY)" or "[YYYY]" right after the number is the Plex / Radarr
		// movie signature: a film whose title carries the hyphen, dropped
		// into a show library. The movie parser titles these correctly
		// ("Catch-22", 1970); reading them as show "Catch" episode 22 would
		// not be.
		{"movie with a hyphen-number title", "Catch-22 (1970).mkv", "", 0, false},
		{"movie with a hyphen-number title, group and quality", "[Group] Catch-22 (2019) [1080p].mkv", "", 0, false},
		{"movie with a hyphen-number title, bracketed year", "Catch-22 [1970].mkv", "", 0, false},
		{"movie with a hyphen-number title, year with a suffix", "Catch-22 (1970 Remaster).mkv", "", 0, false},
		{"movie with a one-letter hyphen-number title", "U-571 (2000).mkv", "", 0, false},
		{"movie with a hyphen-number title, 3 digits", "Room-237 (2012).mkv", "", 0, false},
		{"movie with a hyphen-number title and quality", "Apollo-13 (1995) [1080p].mkv", "", 0, false},
		// Residual: a release year placed AFTER the number has the exact
		// shape of the movie signature and reads as the movie.
		{"residual: year tag after the number, bracketed", "[Group] Title-13 [2019][1080p].mkv", "", 0, false},
		{"residual: year tag after the number, parens", "[Group] Title-13 (2019) [1080p].mkv", "", 0, false},
		// A digits-only title is never a show in the unspaced form —
		// including the anime "86" ("86 - 01" and "86 EIGHTY-SIX-01" parse).
		{"digits-only title, double episode", "/anime/Show Name/01-02.mkv", "", 0, false},
		{"digits-only title, compact season-episode", "/anime/Show Name/Season 2/2-01.mkv", "", 0, false},
		{"digits-only title, two digits", "24-01.mkv", "", 0, false},
		{"accepted loss: the anime 86", "86-01 [BD].mkv", "", 0, false},
		{"accepted loss: the anime 86, inside its folder", "/anime/86 EIGHTY-SIX/86-01 [BD].mkv", "", 0, false},
		// Hyphen-number titles: a single digit is never an episode here.
		{"Spider-Man-2", "Spider-Man-2.mkv", "", 0, false},
		{"Ranma 1-2", "Ranma 1-2 [BD].mkv", "", 0, false},
		{"Part-1", "Show Name Part-1.mkv", "", 0, false},
		{"Vol-1", "Show Name Vol-1 [BD].mkv", "", 0, false},
		{"Spider-Man 2 (no dash before the digit)", "Spider-Man 2.mkv", "", 0, false},
		{"hyphenated title, no number", "Re-Zero.mkv", "", 0, false},
		{"hyphenated title with punctuation, no number", "K-On!.mkv", "", 0, false},
		{"Fate-Zero", "Fate-Zero.mkv", "", 0, false},
		// Zero-padded part / volume / disc / BD-extra indices: the number
		// is that index, not an episode, and "<Show> Part" is not a show.
		{"Part-01", "Show Name Part-01.mkv", "", 0, false},
		{"Part-02 with quality", "Show Name Part-02 [BD].mkv", "", 0, false},
		{"hyphenated Part-02", "Show Name-Part-02.mkv", "", 0, false},
		{"Pt-03", "Show Name Pt-03.mkv", "", 0, false},
		{"Vol-02", "Show Name Vol-02 [BD].mkv", "", 0, false},
		{"Vol.01-13", "Show Name Vol.01-13.mkv", "", 0, false},
		{"Disc-02", "Show Name Disc-02.mkv", "", 0, false},
		{"Disc 1-01", "Show Name Disc 1-01.mkv", "", 0, false},
		{"Disc1-01", "Show Name Disc1-01.mkv", "", 0, false},
		{"CD-02", "Show Name CD-02.mkv", "", 0, false},
		{"NCOP-01", "[Group] Show Name NCOP-01 [BD 1080p].mkv", "", 0, false},
		{"NCED-01v2", "[Group] Show Name NCED-01v2 [BD].mkv", "", 0, false},
		{"OP-01 END", "[Group] Show Name OP-01 END [BD].mkv", "", 0, false},
		{"Creditless OP-01", "[Group] Show Name Creditless OP-01 [BD].mkv", "", 0, false},
		{"Menu-01", "[Group] Show Name Menu-01 [BD].mkv", "", 0, false},
		{"Preview-01", "[Group] Show Name Preview-01 [BD].mkv", "", 0, false},
		{"CM-01", "[Group] Show Name CM-01 [BD].mkv", "", 0, false},
		{"DVD-01", "Show Name DVD-01.mkv", "", 0, false},
		// Double episodes / title-tail guard: filing "Title-01-02" under
		// a show called "Title - 01" would be a wrong-show regression;
		// today these are orphans and they stay orphans. The bare-number
		// form ("Show Name 01-02") is the same file with the first
		// number attached by a space.
		{"double episode, tight", "Title-01-02 [BD].mkv", "", 0, false},
		{"double episode, spaced", "[Group] Show - 01-02 [BD].mkv", "", 0, false},
		{"double episode, first number revised", "Title-01v2-02 [BD].mkv", "", 0, false},
		{"double episode, first number marked END", "Title-01 END-02 [BD].mkv", "", 0, false},
		{"double episode, first number marked OVA", "Title-13 OVA-02 [BD].mkv", "", 0, false},
		{"double episode, first number marked SP", "Title-13 SP-02 [BD].mkv", "", 0, false},
		{"double episode, bare first number", "[Group] Show Name 01-02 [BD].mkv", "", 0, false},
		{"double episode, bare first number, no quality", "Show Name 01-02.mkv", "", 0, false},
		{"double episode, bare first number, dotted", "Show.Name.01-02.mkv", "", 0, false},
		{"double episode, bare first number revised", "[Group] Show Name 01v2-02 [BD].mkv", "", 0, false},
		{"double episode, bare second number revised", "[Group] Show Name 01-02v2 [BD].mkv", "", 0, false},
		{"double episode, E-prefixed first number", "Show Name E01-02 [BD].mkv", "", 0, false},
		{"double episode, Ep-prefixed first number", "Show Name Ep01-02 [BD].mkv", "", 0, false},
		{"double episode, E-prefixed first number, dotted", "Show.Name.E01-02.mkv", "", 0, false},
		{"double episode, space before the dash", "Show Name 01 -02 [BD].mkv", "", 0, false},
		// The spaced rule refuses " - 1-02" (a dash follows the digit), so
		// the name reaches the tight rule as title "Show - 1": the spaced
		// alternative of episodeMarkerTailRE is what catches it.
		{"double episode, one-digit spaced first number", "Show - 1-02 [BD].mkv", "", 0, false},
		{"batch range, bare first number", "Show Name 01-12 [BD Batch].mkv", "", 0, false},
		{"batch range, Ep word", "Show Name Ep 01-02.mkv", "", 0, false},
		// Long-runner numbering pads to three or four digits, so a double
		// episode or batch there is a same-width ascending pair.
		{"double episode, four-digit numbering", "[Group] One Piece 1071-1072 [1080p].mkv", "", 0, false},
		{"double episode, four-digit numbering, no group", "Detective Conan 1000-1001 [1080p].mkv", "", 0, false},
		{"batch range, three-digit numbering", "Show Name 001-012 [BD Batch].mkv", "", 0, false},
		{"double episode, three-digit numbering", "Mob Psycho 100-101 [BD].mkv", "", 0, false},
		// The batch that crosses the thousand mark pairs numbers of
		// different widths; width is not compared (tightDashEpisodeOK).
		{"batch crossing the thousand mark", "One Piece 999-1000 [1080p].mkv", "", 0, false},
		{"batch crossing the thousand mark, with group", "[Group] Detective Conan 999-1000 [1080p].mkv", "", 0, false},
		{"year then episode", "Show Name-2024-13.mkv", "", 0, false},
		// The season-range waiver is only for a finale marker or a
		// revision (Moozzi2's "-24 END" / "-24v2"); "Special" or "TV"
		// after a broadcast-year range is an ordinary word.
		{"season range with an ordinary word after it", "Top Gear 2010-11 Special.mkv", "", 0, false},
		{"season range with TV after it", "Top Gear 2010-11 TV.mkv", "", 0, false},
		// A codec / audio / rate word before the dash: the number is a
		// bit depth, sample width or scene-group id, not an episode.
		{"codec word before the dash, detached unit", "Show Name x265-10 [BD].mkv", "", 0, false},
		{"audio word before the dash", "Show Name FLAC-24 [BD].mkv", "", 0, false},
		{"all-numeric scene group", "Show.Name.720p.HDTV.x264-1337.mkv", "", 0, false},
		{"date with unspaced dashes (the daily parser owns it)", "Show Name-2024-01-15.mkv", "", 0, false},
		// Accepted losses of the title-tail guard, every one with a spaced
		// form that parses (see the spaced section above): titles that
		// themselves end in a hyphen-number, and titles that end in a bare
		// two-digit number — "Digimon Adventure 02" is the headline case.
		{"accepted loss: Catch-22", "Catch-22-01.mkv", "", 0, false},
		{"accepted loss: Mob-Psycho-100", "Mob-Psycho-100-05 [BD].mkv", "", 0, false},
		{"accepted loss: Ranma 1-2", "Ranma 1-2-01 [BD].mkv", "", 0, false},
		{"accepted loss: 22-7", "22-7-01 [BD].mkv", "", 0, false},
		{"accepted loss: SAC-2045", "Ghost in the Shell SAC-2045-01.mkv", "", 0, false},
		{"accepted loss: two-digit title tail, Digimon Adventure 02", "Digimon Adventure 02-01 [BD].mkv", "", 0, false},
		{"accepted loss: two-digit title tail, Digimon Adventure 02, Moozzi2", "[Moozzi2] Digimon Adventure 02-01 [BD 1920x1080 x264 FLAC].mkv", "", 0, false},
		{"accepted loss: two-digit title tail, Area 88", "Area 88-01 [BD].mkv", "", 0, false},
		{"accepted loss: two-digit title tail, Ultraman 80", "Ultraman 80-01 [BD].mkv", "", 0, false},
		{"accepted loss: two-digit title tail", "Ben 10-11 [BD].mkv", "", 0, false},
		{"accepted loss: two-digit title tail, Gundam 00", "Gundam 00-01 [BD].mkv", "", 0, false},
		// Season markers: cleanShowTitle folds "Show S2" onto "Show" and
		// the anime path slots everything into Season 1, so "Show S2-01"
		// would land on the real Episode 1. Refused so they stay healable
		// orphans until season markers are honoured (the spaced form keeps
		// its long-standing behaviour: see TestParseEpisodeIdentity_Precedence).
		{"season marker S02 before the dash", "Show Name S02-13 [BD].mkv", "", 0, false},
		{"season marker S2 before the dash", "[Moozzi2] Show S2-01 [BD].mkv", "", 0, false},
		{"season marker Season 2 before the dash", "Attack on Titan Season 2-01 [BD].mkv", "", 0, false},
		{"season marker 2nd Season before the dash", "Show Name 2nd Season-01 [BD].mkv", "", 0, false},
		{"season marker in a dotted title", "Show.Name.S2-13.mkv", "", 0, false},
		{"season marker Cour 2 before the dash", "Show Name Cour 2-01 [BD].mkv", "", 0, false},
		// The spaced rule ran into the quality block and the tight rule
		// has nothing to read either.
		{"spaced number inside a paren quality block, unspaced codec token", "Show (x265-10 - bit).mkv", "", 0, false},
		// Malformed numbers and separators.
		{"fractional episode", "Title-13.5 [BD].mkv", "", 0, false},
		{"five-digit run", "Title-00013 [BD].mkv", "", 0, false},
		{"single zero", "Title-0 [BD].mkv", "", 0, false},
		{"episode 00", "Title-00 [BD].mkv", "", 0, false},
		{"en dash is not the separator", "Title–13 [BD].mkv", "", 0, false},
		{"space after the dash only", "Title- 13 [BD].mkv", "", 0, false},
		{"number only, no folder to fall back to", " -13.mkv", "", 0, false},

		// ── Reject cases ─────────────────────────────────────────────────────
		{
			name:        "S##E## name has no dash before its digits → reject",
			path:        "/media/tv/Show Name S01E03.mkv",
			wantTitle:   "",
			wantEpisode: 0,
			wantOK:      false,
			// Neither rule sees a dash in front of a digit run ("S01E03"
			// has none), so the anime parser rejects outright. The scanner
			// never gets here for such a name anyway: ParseTVFilename
			// claims it first (see TestParseEpisodeIdentity_Precedence).
			// This row used to declare wantOK=true and was t.Skip'd to
			// hide that the declaration was wrong.
		},
		{
			name:        "no dash separator → reject",
			path:        "/anime/Show Name 245.mkv",
			wantTitle:   "",
			wantEpisode: 0,
			wantOK:      false,
		},
		{
			name:        "year suffix must not be parsed as episode (no dash)",
			path:        "/anime/Show 2024.mkv",
			wantTitle:   "",
			wantEpisode: 0,
			wantOK:      false,
		},
		{
			name:        "movie file with no episode hint → reject",
			path:        "/movies/Spirited Away (2001).mkv",
			wantTitle:   "",
			wantEpisode: 0,
			wantOK:      false,
		},
		{
			name: "quality marker should not match as episode",
			path: "/anime/Show - 1080p.mkv",
			// "Show - 1080" matches up to the digit run; lookahead
			// requires non-letter so 1080p (digit-letter) blocks
			// the match at 1080. Reject.
			wantTitle:   "",
			wantEpisode: 0,
			wantOK:      false,
		},
		{
			name:        "episode 0 → reject (reserved for synthetic placeholders)",
			path:        "/anime/Show - 0.mkv",
			wantTitle:   "",
			wantEpisode: 0,
			wantOK:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTitle, gotEp, gotOK := ParseAnimeAbsoluteFilename(tt.path)
			if gotOK != tt.wantOK {
				t.Errorf("ok: got %v, want %v", gotOK, tt.wantOK)
			}
			if gotOK {
				if gotTitle != tt.wantTitle {
					t.Errorf("title: got %q, want %q", gotTitle, tt.wantTitle)
				}
				if gotEp != tt.wantEpisode {
					t.Errorf("episode: got %d, want %d", gotEp, tt.wantEpisode)
				}
			}
		})
	}
}

func TestParseDailyFilename(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantTitle string
		wantYear  int
		wantMonth int
		wantDay   int
		wantOK    bool
	}{
		{
			name:      "sonarr daily format",
			path:      "/tv/The Daily Show/Season 2013/The Daily Show - 2013-10-30 - Guest Name WEBDL-1080p.mkv",
			wantTitle: "The Daily Show",
			wantYear:  2013, wantMonth: 10, wantDay: 30,
			wantOK: true,
		},
		{
			name:      "scene dotted date",
			path:      "/tv/The.Daily.Show.2024.01.15.Jon.Stewart.1080p.WEB.mkv",
			wantTitle: "The Daily Show",
			wantYear:  2024, wantMonth: 1, wantDay: 15,
			wantOK: true,
		},
		{
			name:      "underscore date",
			path:      "/tv/Conan 2019_06_03 Episode.mkv",
			wantTitle: "Conan",
			wantYear:  2019, wantMonth: 6, wantDay: 3,
			wantOK: true,
		},
		{
			name:   "invalid month rejected",
			path:   "/tv/Show - 2013-99-10 - Title.mkv",
			wantOK: false,
		},
		{
			name:   "invalid day rejected",
			path:   "/tv/Show - 2013-10-99 - Title.mkv",
			wantOK: false,
		},
		{
			name:   "bare year is not a date",
			path:   "/tv/Show Name 2024.mkv",
			wantOK: false,
		},
		{
			name:   "resolution digits are not a date",
			path:   "/tv/Show Name 1080p x265.mkv",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, y, mo, d, ok := ParseDailyFilename(tt.path)
			if ok != tt.wantOK {
				t.Fatalf("ok: got %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if title != tt.wantTitle || y != tt.wantYear || mo != tt.wantMonth || d != tt.wantDay {
				t.Fatalf("got (%q, %d, %d, %d), want (%q, %d, %d, %d)",
					title, y, mo, d, tt.wantTitle, tt.wantYear, tt.wantMonth, tt.wantDay)
			}
		})
	}
}

// A filename carrying both S##E## and a date must keep its S##E## identity —
// ParseTVFilename runs first in processShowHierarchy, and the date is just
// part of the episode title there.
func TestDailyParsing_SxxExxStillWins(t *testing.T) {
	path := "/tv/Show/Season 1/Show - S01E05 - 2013-10-30 Recap.mkv"
	if _, s, e, ok := ParseTVFilename(path); !ok || s != 1 || e != 5 {
		t.Fatalf("ParseTVFilename: got (s=%d e=%d ok=%v), want S01E05", s, e, ok)
	}
}

// Anime absolute numbering ("Show - 1030") must not be mistaken for a date,
// and a real date must not be mistaken for an absolute episode.
func TestDailyParsing_DisjointFromAnime(t *testing.T) {
	if _, _, _, _, ok := ParseDailyFilename("/tv/Show - 1030 [1080p].mkv"); ok {
		t.Fatal("bare absolute episode number parsed as a date")
	}
	if _, _, ok := ParseAnimeAbsoluteFilename("/tv/The Daily Show - 2013-10-30 - Guest.mkv"); ok {
		t.Fatal("date parsed as anime absolute episode")
	}
}

// TestParseEpisodeIdentity_Precedence pins the parser order that
// processShowHierarchy and processFile's orphan-heal gate both rely on:
// S##E## / 1x03, then date, then anime absolute. Each adjacent pair of
// the chain is pinned by a name both of its parsers accept: "Show -
// S01E05 - 2013-10-30 Recap" is also a daily name (show "Show - S01E05",
// season 2013, episode 1030), "Show 1x03 - 05" is also a spaced anime
// name (show "Show 1x03", episode 5) and "Show - 2024 01 15" is also a
// spaced anime name (episode 2024), so any reordering of the chain
// flips one of them. The S##E## row pins a hyphenated title surviving
// cleanShowTitle intact; a date written with unspaced dashes must land
// on the daily parser — the tight-dash rule runs last, and its
// title-tail guard refuses that shape anyway.
func TestParseEpisodeIdentity_Precedence(t *testing.T) {
	id, ok := parseEpisodeIdentity("/tv/Show/Season 1/Show - S01E05 - 2013-10-30 Recap.mkv")
	if !ok {
		t.Fatal("S##E## name with a trailing date did not resolve to an episode")
	}
	if id.showTitle != "Show" || id.season != 1 || id.episode != 5 || id.episodeTitle != "" {
		t.Errorf("S##E## beats daily: got %+v, want show %q, season 1, episode 5, no episode title", id, "Show")
	}

	id, ok = parseEpisodeIdentity("/tv/Show/Show 1x03 - 05.mkv")
	if !ok {
		t.Fatal("1x03 name with a trailing spaced number did not resolve to an episode")
	}
	if id.showTitle != "Show" || id.season != 1 || id.episode != 3 || id.episodeTitle != "" {
		t.Errorf("1x03 beats anime: got %+v, want show %q, season 1, episode 3, no episode title", id, "Show")
	}

	id, ok = parseEpisodeIdentity("/tv/Show/Show - 2024 01 15.mkv")
	if !ok {
		t.Fatal("spaced date name did not resolve to an episode")
	}
	if id.showTitle != "Show" || id.season != 2024 || id.episode != 115 || id.episodeTitle != "2024-01-15" {
		t.Errorf("daily beats anime: got %+v, want show %q, season 2024, episode 115, title %q", id, "Show", "2024-01-15")
	}

	id, ok = parseEpisodeIdentity("/tv/Show-Name S01E05 [BD].mkv")
	if !ok {
		t.Fatal("hyphenated-title S##E## name did not resolve to an episode")
	}
	if id.showTitle != "Show-Name" || id.season != 1 || id.episode != 5 || id.episodeTitle != "" {
		t.Errorf("S##E##: got %+v, want show %q, season 1, episode 5, no episode title", id, "Show-Name")
	}

	id, ok = parseEpisodeIdentity("/tv/Show Name/Season 2024/Show Name-2024-01-15.mkv")
	if !ok {
		t.Fatal("date-based name did not resolve to an episode")
	}
	if id.showTitle != "Show Name" || id.season != 2024 || id.episode != 115 || id.episodeTitle != "2024-01-15" {
		t.Errorf("daily: got %+v, want show %q, season 2024, episode 115, title %q", id, "Show Name", "2024-01-15")
	}

	id, ok = parseEpisodeIdentity("/anime/Delicious in Dungeon/Season 1/[Moozzi2] Dungeon Meshi-13 [BD 1920x1080 x265-10Bit 2Audio].mkv")
	if !ok {
		t.Fatal("Moozzi2 name did not resolve to an episode")
	}
	if id.showTitle != "Dungeon Meshi" || id.season != 1 || id.episode != 13 || id.episodeTitle != "" {
		t.Errorf("anime absolute: got %+v, want show %q, season 1, episode 13, no episode title", id, "Dungeon Meshi")
	}

	// The spaced rule's long-standing season-marker behaviour: the marker
	// is folded away and the file lands in the synthetic Season 1. Pinned
	// so the day season markers are honoured, this row is the one to flip.
	id, ok = parseEpisodeIdentity("/anime/Show Name S2 - 01 [1080p].mkv")
	if !ok {
		t.Fatal("spaced season-marker name did not resolve to an episode")
	}
	if id.showTitle != "Show Name" || id.season != 1 || id.episode != 1 {
		t.Errorf("spaced season marker: got %+v, want show %q, season 1, episode 1", id, "Show Name")
	}

	if id, ok := parseEpisodeIdentity("/media/tv/Some.Random.Video.2020.mkv"); ok {
		t.Errorf("unparseable name resolved to %+v; want ok=false", id)
	}
}

// TestParseEpisodeIdentity_SeasonFolder pins seasonFromFolder: an
// absolute-numbered (anime-rule) file takes the season of the "Season N"
// / "SNN" folder it sits in, else the synthetic Season 1. The headline
// case is QA's Clannad After Story — filed under "Clannad/Season 2"
// beside "Clannad.2007.S02E01…", it must be S2E20, not a second file on
// Clannad S1E20. Names whose season is in the name (S##E##, dates)
// ignore the folder.
func TestParseEpisodeIdentity_SeasonFolder(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantTitle   string
		wantSeason  int
		wantEpisode int
	}{
		{"sequel in a Season 2 folder", "/anime/Clannad/Season 2/[Moozzi2] Clannad After Story-20 [BD 1920x1080 x.264 Flac].mkv", "Clannad After Story", 2, 20},
		{"Season 1 folder", "/anime/Delicious in Dungeon/Season 1/[Moozzi2] Dungeon Meshi-13 [BD 1920x1080 x265-10Bit 2Audio].mkv", "Dungeon Meshi", 1, 13},
		{"no season folder", "/anime/Show/Show - 05.mkv", "Show", 1, 5},
		{"compact S03 folder, spaced form", "/anime/Show/S03/[Group] Show - 05 [1080p].mkv", "Show", 3, 5},
		{"two-digit season folder", "/anime/Show/Season 12/Show-05 [BD].mkv", "Show", 12, 5},
		{"zero-padded season folder", "/anime/Show/Season 02/Show - 05.mkv", "Show", 2, 5},
		{"Season 00 folder is the specials season", "/anime/Show/Season 00/Show - 01.mkv", "Show", 0, 1},
		{"Specials folder is the specials season", "/anime/Show/Specials/Show - 01.mkv", "Show", 0, 1},
		{"OVAs folder is the specials season", "/anime/Show/OVAs/[Moozzi2] Show-01 [BD].mkv", "Show", 0, 1},
		// Decorated season folders count (Jellyfin tolerates text after
		// the number); a three-digit number does not.
		{"decorated season folder", "/anime/Clannad/Season 2 - After Story/[Moozzi2] Clannad After Story-20 [BD].mkv", "Clannad After Story", 2, 20},
		{"decorated season folder with a year", "/anime/Show/Season 02 (2008)/[Group] Show - 05.mkv", "Show", 2, 5},
		{"three-digit season folder is not a season", "/anime/Show/Season 100/Show - 05.mkv", "Show", 1, 5},
		// Known gap: a cour-named SHOW folder is not a season folder.
		{"cour-named show folder is not read as a season", "/anime/One Punch Man S2/[Group] One Punch Man - 05.mkv", "One Punch Man", 1, 5},
		{"Windows path", `C:\anime\Show\Season 2\Show - 05.mkv`, "Show", 2, 5},
		// The folder only applies to the anime rules: an explicit S##E##
		// or a date carries its own season.
		{"S##E## in a Season 2 folder keeps its own season", "/anime/Show/Season 2/Show S01E05.mkv", "Show", 1, 5},
		{"date-based name in a Season folder keeps its year", "/tv/Show/Season 2/Show - 2013-10-30 - Guest.mkv", "Show", 2013, 1030},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, ok := parseEpisodeIdentity(tt.path)
			if !ok {
				t.Fatalf("%s did not resolve to an episode", tt.path)
			}
			if id.showTitle != tt.wantTitle || id.season != tt.wantSeason || id.episode != tt.wantEpisode {
				t.Errorf("got show %q season %d episode %d, want show %q season %d episode %d",
					id.showTitle, id.season, id.episode, tt.wantTitle, tt.wantSeason, tt.wantEpisode)
			}
		})
	}
}
