package scanner

import "testing"

func TestParseMovieFilename(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantTitle string
		wantYear  *int
		edition   string
		part      int
	}{
		// ── Editions ──────────────────────────────────────────────────────
		{"plex marker in file", "/m/Blade Runner (1982)/Blade Runner (1982) {edition-Final Cut}.mkv", "Blade Runner", intPtr(1982), "Final Cut", 0},
		{"plex marker before year stays out of the title", "/m/x/Blade Runner {edition-Final Cut} (1982).mkv", "Blade Runner", intPtr(1982), "Final Cut", 0},
		{"plex marker in folder", "/m/Aliens (1986) {edition-Special Edition}/Aliens (1986).mkv", "Aliens", intPtr(1986), "Special Edition", 0},
		{"file marker wins over folder", "/m/Aliens (1986) {edition-Special Edition}/Aliens (1986) {edition-Theatrical}.mkv", "Aliens", intPtr(1986), "Theatrical", 0},
		{"plex marker with ids", "/m/Heat (1995) {tmdb-949}/Heat (1995) {edition-Director's Definitive Edition} [Bluray-2160p].mkv", "Heat", intPtr(1995), "Director's Definitive Edition", 0},
		{"jellyfin suffix", "/m/Movie (2010)/Movie (2010) - Director's Cut.mkv", "Movie", intPtr(2010), "Director's Cut", 0},
		{"jellyfin resolution suffix", "/m/Movie (2010)/Movie (2010) - 1080p.mkv", "Movie", intPtr(2010), "1080p", 0},
		{"jellyfin suffix with quality brackets", "/m/Movie (2010)/Movie (2010) - Extended [1080p].mkv", "Movie", intPtr(2010), "Extended", 0},
		{"jellyfin bracket-only suffix", "/m/Movie (2010)/Movie (2010) - [4K].mkv", "Movie", intPtr(2010), "4K", 0},
		{"bracket tag", "/m/Movie (2010)/Movie (2010) [Extended].mkv", "Movie", intPtr(2010), "Extended", 0},
		{"scene directors cut", "/m/Movie.2010.Directors.Cut.1080p.BluRay.x264-GRP.mkv", "Movie", intPtr(2010), "Director's Cut", 0},
		{"scene unrated", "/m/Movie.2010.UNRATED.720p.BluRay.x264-GRP.mkv", "Movie", intPtr(2010), "Unrated", 0},
		{"scene imax", "/m/Movie.2010.IMAX.2160p.WEB-DL.mkv", "Movie", intPtr(2010), "IMAX", 0},
		{"extended edition phrase", "/m/Movie (2003) Extended Edition 1080p.mkv", "Movie", intPtr(2003), "Extended Edition", 0},
		{"theatrical cut", "/m/Movie (2003) Theatrical Cut.mkv", "Movie", intPtr(2003), "Theatrical", 0},
		{"remastered", "/m/Movie (1977) [Remastered] [1080p].mkv", "Movie", intPtr(1977), "Remastered", 0},
		{"earliest tag wins", "/m/Movie.2010.EXTENDED.REMASTERED.1080p.mkv", "Movie", intPtr(2010), "Extended", 0},
		{"tags in the title are not editions", "/m/Unrated (2009)/Unrated (2009).mkv", "Unrated", intPtr(2009), "", 0},
		{"imax in title", "/m/IMAX Hubble (2010).mkv", "IMAX Hubble", intPtr(2010), "", 0},
		{"scene group dash is no suffix", "/m/Movie.2010.1080p.BluRay.x264-GRP.mkv", "Movie", intPtr(2010), "", 0},
		{"no year no tag", "/m/Extended Family.mkv", "Extended Family", nil, "", 0},

		// ── Stacked parts ─────────────────────────────────────────────────
		{"cd1", "/m/Movie (2001)/Movie (2001) cd1.mkv", "Movie", intPtr(2001), "", 1},
		{"cd2 tight dash", "/m/Movie (2001)/Movie (2001)-cd2.mkv", "Movie", intPtr(2001), "", 2},
		{"part 2 spaced", "/m/Movie (2001)/Movie (2001) Part 2.mkv", "Movie", intPtr(2001), "", 2},
		{"pt3 dotted scene", "/m/Movie.2001.DVDRip.pt3.avi", "Movie", intPtr(2001), "", 3},
		{"disc 1", "/m/Movie (2001) disc 1.mkv", "Movie", intPtr(2001), "", 1},
		{"disk_2", "/m/Movie (2001) disk_2.mkv", "Movie", intPtr(2001), "", 2},
		{"dvd1", "/m/Movie (2001) dvd1.mkv", "Movie", intPtr(2001), "", 1},
		{"dvd9 is a disc format", "/m/Movie (2001) DVD9.mkv", "Movie", intPtr(2001), "", 0},
		{"dvd9 then cd2", "/m/Movie.2001.DVD9.cd2.mkv", "Movie", intPtr(2001), "", 2},
		{"part then quality", "/m/Movie (2001) cd1 [1080p].mkv", "Movie", intPtr(2001), "", 1},
		{"bare letter a", "/m/Movie (2001)/Movie (2001)-a.mkv", "Movie", intPtr(2001), "", 1},
		{"bare letter b spaced", "/m/Movie (2001)/Movie (2001) - b.mkv", "Movie", intPtr(2001), "", 2},
		{"lettered part", "/m/Movie (2001) part b.mkv", "Movie", intPtr(2001), "", 2},
		{"letter after a group is no part", "/m/Movie.2001.1080p.x264-a.mkv", "Movie", intPtr(2001), "", 0},
		{"1080p is no part", "/m/Movie (2001) Part 1080p.mkv", "Movie", intPtr(2001), "", 0},
		{"edition and part", "/m/Movie (2001)/Movie (2001) - Director's Cut - cd2.mkv", "Movie", intPtr(2001), "Director's Cut", 2},
		{"suffix that is only a part", "/m/Movie (2001)/Movie (2001) - cd1.mkv", "Movie", intPtr(2001), "", 1},
		{"plex edition and part", "/m/Movie (2001)/Movie (2001) {edition-Extended} pt2.mkv", "Movie", intPtr(2001), "Extended", 2},

		// Part words in the TITLE are never stacking markers.
		{"dune part one", "/m/Dune Part One (2021)/Dune Part One (2021).mkv", "Dune Part One", intPtr(2021), "", 0},
		{"deathly hallows part 1", "/m/Harry Potter and the Deathly Hallows Part 1 (2010)/Harry Potter and the Deathly Hallows Part 1 (2010).mkv", "Harry Potter and the Deathly Hallows Part 1", intPtr(2010), "", 0},
		{"deathly hallows part 2 scene", "/m/Harry.Potter.and.the.Deathly.Hallows.Part.2.2011.1080p.mkv", "Harry Potter and the Deathly Hallows Part 2", intPtr(2011), "", 0},
		{"kill bill vol 1", "/m/Kill Bill Vol. 1 (2003)/Kill Bill Vol. 1 (2003).mkv", "Kill Bill Vol  1", intPtr(2003), "", 0},
		{"deathly hallows no year anywhere", "/m/Harry Potter and the Deathly Hallows Part 1.mkv", "Harry Potter and the Deathly Hallows Part 1", nil, "", 0},
		{"deathly hallows year only on folder", "/m/Harry Potter and the Deathly Hallows Part 1 (2010)/Harry Potter and the Deathly Hallows Part 1.mkv", "Harry Potter and the Deathly Hallows Part 1", nil, "", 0},

		// No year in the file name: folder-split and safe markers.
		{"folder split part only", "/m/Movie (2001)/cd1.mkv", "Movie", intPtr(2001), "", 1},
		{"folder split matching title", "/m/Kill Bill (2003)/Kill Bill part2.mkv", "Kill Bill", intPtr(2003), "", 2},
		{"no year cd marker", "/m/Some Movie cd2.mkv", "Some Movie", nil, "", 2},
		{"no year part marker stays title", "/m/Some Movie part2.mkv", "Some Movie part2", nil, "", 0},
		{"part only without folder year", "/m/Movies/cd1.mkv", "cd1", nil, "", 0},

		// Unchanged behaviour.
		{"plain", "/m/Inception (2010).mp4", "Inception", intPtr(2010), "", 0},
		{"blu-ray stream", "/m/War Machine (2026)/00000.m2ts", "War Machine", intPtr(2026), "", 0},
		{"blu-ray stream edition folder", "/m/War Machine (2026) {edition-Extended}/00000.m2ts", "War Machine", intPtr(2026), "Extended", 0},
		{"windows path", `C:\media\Movie_Title_2020_WEB cd2.mkv`, "Movie Title", intPtr(2020), "", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, year, n := parseMovieFilename(tt.path)
			if title != tt.wantTitle || !intPtrEqual(year, tt.wantYear) {
				t.Errorf("title/year = %q/%s, want %q/%s", title, fmtIntPtr(year), tt.wantTitle, fmtIntPtr(tt.wantYear))
			}
			if n.Edition != tt.edition {
				t.Errorf("edition = %q, want %q", n.Edition, tt.edition)
			}
			if n.PartIndex != tt.part {
				t.Errorf("part = %d, want %d", n.PartIndex, tt.part)
			}
		})
	}
}

// TestParseMovieFilename_TitleMatchesParseFilename pins that reading the
// edition and part never changes the title/year a name already parsed to:
// for names without an {edition-} marker or a yearless stacking marker the
// two parsers must agree, so no existing movie is split or merged.
func TestParseMovieFilename_TitleMatchesParseFilename(t *testing.T) {
	paths := []string{
		"/media/movies/The.Matrix.1999.1080p.BluRay.mkv",
		"/media/movies/Inception (2010).mp4",
		"/media/movies/NoYear.mkv",
		`C:\media\Movie_Title_2020_WEB.mkv`,
		"/m/Movie (2010) - Director's Cut.mkv",
		"/m/Movie (2001) cd1.mkv",
		"/m/Movie.2001.DVDRip.pt3.avi",
		"/m/Blade Runner 2049 (2017) [Extended].mkv",
		"/m/[Group] Movie (2001) - b.mkv",
		"/m/Dune Part One (2021).mkv",
		"/m/Kill Bill Vol. 1 (2003).mkv",
		"/m/Harry Potter and the Deathly Hallows Part 1.mkv",
		"/m/Some Movie part2.mkv",
		"/m/War Machine (2026)/00000.m2ts",
		"/m/Altered Carbon-2018.mkv",
	}
	for _, p := range paths {
		wantTitle, wantYear := parseFilename(p)
		gotTitle, gotYear, _ := parseMovieFilename(p)
		if gotTitle != wantTitle || !intPtrEqual(gotYear, wantYear) {
			t.Errorf("%s: parseMovieFilename = %q/%s, parseFilename = %q/%s",
				p, gotTitle, fmtIntPtr(gotYear), wantTitle, fmtIntPtr(wantYear))
		}
	}
}
