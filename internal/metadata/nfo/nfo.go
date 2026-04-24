// Package nfo parses Kodi-style XML sidecar metadata files. NFO files
// are the de-facto sidecar format across Kodi, Jellyfin, Emby, and
// Plex (via the XBMCnfoAgent) — users who have curated their
// libraries over years have these everywhere, and importing them
// gives migrants their edits for free on first scan.
//
// The format is loose. Different scrapers and editors emit different
// subsets of fields, different capitalizations, and different
// conventions for multi-value fields (repeated <genre> elements vs
// comma-separated in one element). Parsers here aim to be forgiving:
// unknown elements are ignored, common field drift is accepted, and
// a missing NFO is a no-op (not an error) — a caller that wants to
// fall through to online agents uses the "has this file?" signal,
// not parse failures.
//
// Supported types:
//   movie.nfo           → Movie
//   tvshow.nfo          → Show
//   <episode-file>.nfo  → Episode  (same basename as the media file)
//
// Music NFO (album.nfo / artist.nfo) is intentionally skipped for
// now — real-world curation of music libraries favors embedded tags
// + MusicBrainz IDs over NFO sidecars; we can add it later if we
// see demand on the beta.
package nfo

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrNoSidecar is returned by the Find helpers when no NFO file
// exists at the expected location. Callers fall through to their
// next metadata source without logging this as an error.
var ErrNoSidecar = errors.New("nfo: no sidecar found")

// Movie captures the subset of movie.nfo fields OnScreen uses.
// Rating is the critic/audience score on a 0–10 scale; leave at 0
// to signal "no rating in the NFO."
type Movie struct {
	Title         string
	OriginalTitle string
	SortTitle     string
	Year          int
	Plot          string
	Tagline       string
	RuntimeMin    int      // <runtime> is already in minutes in Kodi output
	MPAA          string   // content rating ("R", "PG-13", "TV-MA")
	Rating        float64  // 0–10 scale
	Genres        []string // flattened + deduplicated
	Directors     []string
	Writers       []string
	Studios       []string
	Tags          []string
	Premiered     *time.Time // parsed <premiered>YYYY-MM-DD</premiered>
	TMDBID        int        // from <uniqueid type="tmdb">
	IMDBID        string     // from <uniqueid type="imdb">
	TVDBID        int        // some scrapers emit this on movie.nfo too
}

// Show captures tvshow.nfo. Season overrides live in separate
// season.nfo files which Kodi also supports; those aren't parsed
// here yet because OnScreen doesn't surface per-season metadata
// beyond the auto-derived "Season N" title.
type Show struct {
	Title         string
	OriginalTitle string
	SortTitle     string
	Year          int
	Plot          string
	Status        string // "Continuing", "Ended"
	Rating        float64
	ContentRating string // <mpaa>
	Genres        []string
	Studios       []string
	Tags          []string
	Premiered     *time.Time
	TMDBID        int
	IMDBID        string
	TVDBID        int
}

// Episode captures <episodedetails> — the per-episode NFO that lives
// next to the video file with a matching basename (e.g.
// `S01E03 - Title.nfo` alongside `S01E03 - Title.mkv`).
type Episode struct {
	Title      string
	Season     int
	Episode    int
	Plot       string
	Rating     float64
	Aired      *time.Time
	RuntimeMin int
	TMDBID     int
	IMDBID     string
	TVDBID     int
}

// ---- raw XML shapes. Kept internal because Kodi's XML has enough
// oddities (uniqueid is a mixed element, genres are repeated elems,
// etc.) that translating to the flat domain structs above is
// clearer than exposing the raw shape to callers.

type uniqueID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr"`
	Value   string `xml:",chardata"`
}

type rawMovie struct {
	XMLName       xml.Name   `xml:"movie"`
	Title         string     `xml:"title"`
	OriginalTitle string     `xml:"originaltitle"`
	SortTitle     string     `xml:"sorttitle"`
	Year          string     `xml:"year"`
	Plot          string     `xml:"plot"`
	Tagline       string     `xml:"tagline"`
	Runtime       string     `xml:"runtime"`
	MPAA          string     `xml:"mpaa"`
	Rating        string     `xml:"rating"`
	Genres        []string   `xml:"genre"`
	Directors     []string   `xml:"director"`
	Writers       []string   `xml:"credits"`
	Studios       []string   `xml:"studio"`
	Tags          []string   `xml:"tag"`
	Premiered     string     `xml:"premiered"`
	ReleaseDate   string     `xml:"releasedate"`
	TMDBIDLegacy  string     `xml:"tmdbid"`
	IMDBIDLegacy  string     `xml:"imdbid"`
	TVDBIDLegacy  string     `xml:"tvdbid"`
	UniqueIDs     []uniqueID `xml:"uniqueid"`
}

type rawShow struct {
	XMLName       xml.Name   `xml:"tvshow"`
	Title         string     `xml:"title"`
	OriginalTitle string     `xml:"originaltitle"`
	SortTitle     string     `xml:"sorttitle"`
	Year          string     `xml:"year"`
	Plot          string     `xml:"plot"`
	Status        string     `xml:"status"`
	Rating        string     `xml:"rating"`
	MPAA          string     `xml:"mpaa"`
	Genres        []string   `xml:"genre"`
	Studios       []string   `xml:"studio"`
	Tags          []string   `xml:"tag"`
	Premiered     string     `xml:"premiered"`
	TMDBIDLegacy  string     `xml:"tmdbid"`
	IMDBIDLegacy  string     `xml:"imdbid"`
	TVDBIDLegacy  string     `xml:"tvdbid"`
	UniqueIDs     []uniqueID `xml:"uniqueid"`
}

type rawEpisode struct {
	XMLName      xml.Name   `xml:"episodedetails"`
	Title        string     `xml:"title"`
	Season       string     `xml:"season"`
	Episode      string     `xml:"episode"`
	Plot         string     `xml:"plot"`
	Rating       string     `xml:"rating"`
	Runtime      string     `xml:"runtime"`
	Aired        string     `xml:"aired"`
	TMDBIDLegacy string     `xml:"tmdbid"`
	IMDBIDLegacy string     `xml:"imdbid"`
	TVDBIDLegacy string     `xml:"tvdbid"`
	UniqueIDs    []uniqueID `xml:"uniqueid"`
}

// ParseMovie reads a movie.nfo from an io.Reader and returns a Movie.
func ParseMovie(r io.Reader) (*Movie, error) {
	var raw rawMovie
	if err := decode(r, &raw); err != nil {
		return nil, err
	}
	m := &Movie{
		Title:         strings.TrimSpace(raw.Title),
		OriginalTitle: strings.TrimSpace(raw.OriginalTitle),
		SortTitle:     strings.TrimSpace(raw.SortTitle),
		Year:          parseInt(raw.Year),
		Plot:          strings.TrimSpace(raw.Plot),
		Tagline:       strings.TrimSpace(raw.Tagline),
		RuntimeMin:    parseInt(raw.Runtime),
		MPAA:          strings.TrimSpace(raw.MPAA),
		Rating:        parseFloat(raw.Rating),
		Genres:        dedupe(trimAll(raw.Genres)),
		Directors:     dedupe(trimAll(raw.Directors)),
		Writers:       dedupe(trimAll(raw.Writers)),
		Studios:       dedupe(trimAll(raw.Studios)),
		Tags:          dedupe(trimAll(raw.Tags)),
	}
	m.Premiered = parseDate(firstNonEmpty(raw.Premiered, raw.ReleaseDate))
	m.TMDBID, m.IMDBID, m.TVDBID = resolveIDs(raw.UniqueIDs, raw.TMDBIDLegacy, raw.IMDBIDLegacy, raw.TVDBIDLegacy)
	return m, nil
}

// ParseShow reads a tvshow.nfo and returns a Show.
func ParseShow(r io.Reader) (*Show, error) {
	var raw rawShow
	if err := decode(r, &raw); err != nil {
		return nil, err
	}
	s := &Show{
		Title:         strings.TrimSpace(raw.Title),
		OriginalTitle: strings.TrimSpace(raw.OriginalTitle),
		SortTitle:     strings.TrimSpace(raw.SortTitle),
		Year:          parseInt(raw.Year),
		Plot:          strings.TrimSpace(raw.Plot),
		Status:        strings.TrimSpace(raw.Status),
		Rating:        parseFloat(raw.Rating),
		ContentRating: strings.TrimSpace(raw.MPAA),
		Genres:        dedupe(trimAll(raw.Genres)),
		Studios:       dedupe(trimAll(raw.Studios)),
		Tags:          dedupe(trimAll(raw.Tags)),
	}
	s.Premiered = parseDate(raw.Premiered)
	s.TMDBID, s.IMDBID, s.TVDBID = resolveIDs(raw.UniqueIDs, raw.TMDBIDLegacy, raw.IMDBIDLegacy, raw.TVDBIDLegacy)
	return s, nil
}

// ParseEpisode reads an <episodedetails> NFO.
func ParseEpisode(r io.Reader) (*Episode, error) {
	var raw rawEpisode
	if err := decode(r, &raw); err != nil {
		return nil, err
	}
	e := &Episode{
		Title:      strings.TrimSpace(raw.Title),
		Season:     parseInt(raw.Season),
		Episode:    parseInt(raw.Episode),
		Plot:       strings.TrimSpace(raw.Plot),
		Rating:     parseFloat(raw.Rating),
		RuntimeMin: parseInt(raw.Runtime),
		Aired:      parseDate(raw.Aired),
	}
	e.TMDBID, e.IMDBID, e.TVDBID = resolveIDs(raw.UniqueIDs, raw.TMDBIDLegacy, raw.IMDBIDLegacy, raw.TVDBIDLegacy)
	return e, nil
}

// FindMovieNFO returns the path of the NFO that pairs with a movie
// file. Kodi looks in this priority order:
//  1. <basename>.nfo   (e.g., "The Matrix (1999).nfo" next to .mkv)
//  2. movie.nfo        (in the same directory)
// Returns ErrNoSidecar when neither exists.
func FindMovieNFO(moviePath string) (string, error) {
	dir := filepath.Dir(moviePath)
	base := strings.TrimSuffix(filepath.Base(moviePath), filepath.Ext(moviePath))
	for _, name := range []string{base + ".nfo", "movie.nfo"} {
		p := filepath.Join(dir, name)
		if exists(p) {
			return p, nil
		}
	}
	return "", ErrNoSidecar
}

// FindShowNFO looks for tvshow.nfo at the given show directory.
func FindShowNFO(showDir string) (string, error) {
	p := filepath.Join(showDir, "tvshow.nfo")
	if exists(p) {
		return p, nil
	}
	return "", ErrNoSidecar
}

// FindEpisodeNFO looks for <basename>.nfo next to an episode file.
// No "episode.nfo" fallback — episodes are always paired 1:1.
func FindEpisodeNFO(episodePath string) (string, error) {
	dir := filepath.Dir(episodePath)
	base := strings.TrimSuffix(filepath.Base(episodePath), filepath.Ext(episodePath))
	p := filepath.Join(dir, base+".nfo")
	if exists(p) {
		return p, nil
	}
	return "", ErrNoSidecar
}

// ---- helpers

func decode(r io.Reader, into any) error {
	dec := xml.NewDecoder(r)
	// Kodi sometimes emits broken XML (unclosed entities, stray HTML
	// tags inside <plot>). CharsetReader must be defaulted; Strict
	// off lets us recover from minor malformation rather than
	// refusing the whole file.
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	return dec.Decode(into)
}

// resolveIDs pulls TMDB/IMDB/TVDB IDs from the modern <uniqueid>
// elements, falling back to the legacy per-type elements (<tmdbid>,
// <imdbid>, <tvdbid>) that older scrapers emit.
func resolveIDs(ids []uniqueID, legacyTMDB, legacyIMDB, legacyTVDB string) (tmdb int, imdb string, tvdb int) {
	for _, id := range ids {
		v := strings.TrimSpace(id.Value)
		if v == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(id.Type)) {
		case "tmdb", "themoviedb":
			if n, err := strconv.Atoi(v); err == nil && tmdb == 0 {
				tmdb = n
			}
		case "imdb":
			if imdb == "" {
				imdb = v
			}
		case "tvdb", "thetvdb":
			if n, err := strconv.Atoi(v); err == nil && tvdb == 0 {
				tvdb = n
			}
		}
	}
	if tmdb == 0 {
		tmdb = parseInt(legacyTMDB)
	}
	if imdb == "" {
		imdb = strings.TrimSpace(legacyIMDB)
	}
	if tvdb == 0 {
		tvdb = parseInt(legacyTVDB)
	}
	return
}

func parseInt(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

func parseDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Kodi emits YYYY-MM-DD almost universally; a handful of scrapers
	// emit YYYY-MM-DDTHH:MM:SS. Try both.
	for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

func trimAll(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if v := strings.TrimSpace(s); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func dedupe(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
