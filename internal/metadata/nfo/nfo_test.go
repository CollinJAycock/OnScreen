package nfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleMovieNFO is a Kodi-exported movie.nfo trimmed to fields
// OnScreen actually reads — full Kodi NFOs are much larger but we
// ignore the extras.
const sampleMovieNFO = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<movie>
  <title>The Matrix</title>
  <originaltitle>The Matrix</originaltitle>
  <sorttitle>Matrix, The</sorttitle>
  <year>1999</year>
  <runtime>136</runtime>
  <mpaa>R</mpaa>
  <rating>8.7</rating>
  <tagline>Welcome to the Real World.</tagline>
  <plot>A computer hacker learns from mysterious rebels about the true nature of his reality.</plot>
  <genre>Action</genre>
  <genre>Sci-Fi</genre>
  <genre>Action</genre>
  <director>Lana Wachowski</director>
  <director>Lilly Wachowski</director>
  <credits>Lana Wachowski</credits>
  <credits>Lilly Wachowski</credits>
  <studio>Warner Bros.</studio>
  <premiered>1999-03-31</premiered>
  <tag>classic</tag>
  <tag>cyberpunk</tag>
  <uniqueid type="tmdb" default="true">603</uniqueid>
  <uniqueid type="imdb">tt0133093</uniqueid>
</movie>
`

// TestParseMovie_Full reads every field we care about and confirms the
// common Kodi weirdness is handled: duplicate genres dedupe, the
// <credits> field populates Writers (Kodi uses <credits> not <writer>),
// uniqueid elements are preferred over the legacy per-type tags.
func TestParseMovie_Full(t *testing.T) {
	m, err := ParseMovie(strings.NewReader(sampleMovieNFO))
	if err != nil {
		t.Fatalf("ParseMovie: %v", err)
	}
	if m.Title != "The Matrix" || m.Year != 1999 {
		t.Errorf("title/year wrong: %+v", m)
	}
	if m.RuntimeMin != 136 {
		t.Errorf("runtime %d, want 136", m.RuntimeMin)
	}
	if m.Rating != 8.7 {
		t.Errorf("rating %v, want 8.7", m.Rating)
	}
	if m.MPAA != "R" {
		t.Errorf("mpaa %q, want R", m.MPAA)
	}
	if m.TMDBID != 603 {
		t.Errorf("tmdb %d, want 603", m.TMDBID)
	}
	if m.IMDBID != "tt0133093" {
		t.Errorf("imdb %q, want tt0133093", m.IMDBID)
	}
	// Genres: dedupe'd (Action appears twice in the NFO)
	if len(m.Genres) != 2 {
		t.Errorf("expected 2 dedupe'd genres, got %v", m.Genres)
	}
	if m.Premiered == nil || m.Premiered.Year() != 1999 {
		t.Errorf("premiered not parsed: %v", m.Premiered)
	}
	if len(m.Directors) != 2 || len(m.Writers) != 2 {
		t.Errorf("directors/writers: dirs=%v writers=%v", m.Directors, m.Writers)
	}
}

// TestParseMovie_LegacyIDs covers pre-v19 Kodi scrapers that emit
// <tmdbid> / <imdbid> as top-level elements instead of <uniqueid>.
func TestParseMovie_LegacyIDs(t *testing.T) {
	legacy := `<?xml version="1.0"?>
<movie>
  <title>Old Scrape</title>
  <year>1980</year>
  <tmdbid>1234</tmdbid>
  <imdbid>tt0080678</imdbid>
</movie>`
	m, err := ParseMovie(strings.NewReader(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if m.TMDBID != 1234 {
		t.Errorf("tmdb %d, want 1234", m.TMDBID)
	}
	if m.IMDBID != "tt0080678" {
		t.Errorf("imdb %q, want tt0080678", m.IMDBID)
	}
}

// TestParseMovie_HTMLInPlot exercises the forgiving parser — some
// scrapers inline HTML tags in <plot>. Strict XML would reject; we
// survive by decoding HTML entities and not tripping on <br>.
func TestParseMovie_HTMLInPlot(t *testing.T) {
	nfo := `<?xml version="1.0"?>
<movie>
  <title>Plot With HTML</title>
  <plot>Line one.<br>Line two with &amp; ampersand.</plot>
</movie>`
	m, err := ParseMovie(strings.NewReader(nfo))
	if err != nil {
		t.Fatalf("ParseMovie rejected HTML-in-plot: %v", err)
	}
	if !strings.Contains(m.Plot, "Line one") || !strings.Contains(m.Plot, "ampersand") {
		t.Errorf("plot text lost HTML recovery: %q", m.Plot)
	}
}

// TestParseShow covers the tvshow.nfo shape — status + premiered are
// Show-specific fields.
func TestParseShow(t *testing.T) {
	nfo := `<?xml version="1.0"?>
<tvshow>
  <title>Breaking Bad</title>
  <year>2008</year>
  <status>Ended</status>
  <mpaa>TV-MA</mpaa>
  <rating>9.5</rating>
  <premiered>2008-01-20</premiered>
  <genre>Crime</genre>
  <genre>Drama</genre>
  <uniqueid type="tmdb">1396</uniqueid>
  <uniqueid type="tvdb">81189</uniqueid>
</tvshow>`
	s, err := ParseShow(strings.NewReader(nfo))
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "Breaking Bad" || s.Status != "Ended" {
		t.Errorf("bad fields: %+v", s)
	}
	if s.TMDBID != 1396 || s.TVDBID != 81189 {
		t.Errorf("ids: tmdb=%d tvdb=%d", s.TMDBID, s.TVDBID)
	}
	if s.ContentRating != "TV-MA" {
		t.Errorf("mpaa %q, want TV-MA", s.ContentRating)
	}
}

// TestParseEpisode covers the <episodedetails> shape — season +
// episode numbers and per-episode airdate.
func TestParseEpisode(t *testing.T) {
	nfo := `<?xml version="1.0"?>
<episodedetails>
  <title>Pilot</title>
  <season>1</season>
  <episode>1</episode>
  <aired>2008-01-20</aired>
  <runtime>58</runtime>
  <rating>9.0</rating>
  <plot>Walter White makes a fateful choice.</plot>
  <uniqueid type="tmdb">62085</uniqueid>
</episodedetails>`
	e, err := ParseEpisode(strings.NewReader(nfo))
	if err != nil {
		t.Fatal(err)
	}
	if e.Season != 1 || e.Episode != 1 {
		t.Errorf("season/episode: %d/%d", e.Season, e.Episode)
	}
	if e.TMDBID != 62085 {
		t.Errorf("tmdb %d", e.TMDBID)
	}
	if e.Aired == nil || e.Aired.Year() != 2008 {
		t.Errorf("aired: %v", e.Aired)
	}
}

// TestFindMovieNFO_BasenameFirst confirms the priority order —
// <basename>.nfo wins over movie.nfo when both exist, because the
// per-file NFO is typically more recent + more specific.
func TestFindMovieNFO_BasenameFirst(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "The Matrix (1999).mkv")
	writeFile(t, video, "fake")
	writeFile(t, filepath.Join(dir, "The Matrix (1999).nfo"), "specific")
	writeFile(t, filepath.Join(dir, "movie.nfo"), "generic")

	got, err := FindMovieNFO(video)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "The Matrix (1999).nfo" {
		t.Errorf("expected per-file NFO, got %q", got)
	}
}

// TestFindMovieNFO_FallbackToMovieNfo is the "operator used the
// generic filename" path.
func TestFindMovieNFO_FallbackToMovieNfo(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "The Matrix (1999).mkv")
	writeFile(t, video, "fake")
	writeFile(t, filepath.Join(dir, "movie.nfo"), "generic")

	got, err := FindMovieNFO(video)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "movie.nfo" {
		t.Errorf("expected movie.nfo fallback, got %q", got)
	}
}

// TestFindMovieNFO_Missing returns the sentinel so callers
// distinguish it from real errors.
func TestFindMovieNFO_Missing(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "alone.mkv")
	writeFile(t, video, "fake")
	_, err := FindMovieNFO(video)
	if err != ErrNoSidecar {
		t.Errorf("expected ErrNoSidecar, got %v", err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}
