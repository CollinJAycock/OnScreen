package serverimport

import (
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// Candidates are this server's items an import can match against.
type Candidates struct {
	Items    []gen.ListImportCandidateItemsRow
	Episodes []gen.ListImportCandidateEpisodesRow
	Files    []gen.ListImportCandidateFilesRow
}

// maxPathSuffix is the most trailing path components a match compares:
// enough to tell "Show/Season 1/S01E01.mkv" from another show's S01E01.
const maxPathSuffix = 3

// ambiguous marks a path suffix shared by more than one item.
var ambiguous = uuid.Max

type seKey struct{ season, episode int }

// Index finds this server's item for a source item.
type Index struct {
	movieTMDB, movieTVDB map[int][]uuid.UUID
	movieIMDB            map[string][]uuid.UUID
	showTMDB, showTVDB   map[int][]uuid.UUID
	showIMDB             map[string][]uuid.UUID
	episodes             map[uuid.UUID]map[seKey]uuid.UUID
	paths                [maxPathSuffix + 1]map[string]uuid.UUID // by suffix length, 2..3
	// showDirs: a show by its folder, from where its episodes' files are
	// ("TV/Severance", or "Severance" alone), by suffix length 1..2. A show
	// has no file of its own to match by.
	showDirs  [3]map[string]uuid.UUID
	types     map[uuid.UUID]string
	durations map[uuid.UUID]int64
}

// NewIndex builds the lookup tables.
func NewIndex(c Candidates) *Index {
	x := &Index{
		movieTMDB: map[int][]uuid.UUID{}, movieTVDB: map[int][]uuid.UUID{}, movieIMDB: map[string][]uuid.UUID{},
		showTMDB: map[int][]uuid.UUID{}, showTVDB: map[int][]uuid.UUID{}, showIMDB: map[string][]uuid.UUID{},
		episodes:  map[uuid.UUID]map[seKey]uuid.UUID{},
		types:     map[uuid.UUID]string{},
		durations: map[uuid.UUID]int64{},
	}
	for n := 2; n <= maxPathSuffix; n++ {
		x.paths[n] = map[string]uuid.UUID{}
	}
	for n := 1; n <= 2; n++ {
		x.showDirs[n] = map[string]uuid.UUID{}
	}
	episodeShow := map[uuid.UUID]uuid.UUID{}
	for _, it := range c.Items {
		x.types[it.ID] = it.Type
		x.setDuration(it.ID, it.DurationMs)
		tmdb, tvdb, imdb := x.movieTMDB, x.movieTVDB, x.movieIMDB
		if it.Type == "show" {
			tmdb, tvdb, imdb = x.showTMDB, x.showTVDB, x.showIMDB
		}
		if it.TmdbID != nil && *it.TmdbID > 0 {
			tmdb[int(*it.TmdbID)] = append(tmdb[int(*it.TmdbID)], it.ID)
		}
		if it.TvdbID != nil && *it.TvdbID > 0 {
			tvdb[int(*it.TvdbID)] = append(tvdb[int(*it.TvdbID)], it.ID)
		}
		if it.ImdbID != nil && *it.ImdbID != "" {
			imdb[*it.ImdbID] = append(imdb[*it.ImdbID], it.ID)
		}
	}
	for _, e := range c.Episodes {
		episodeShow[e.ID] = e.ShowID
		x.types[e.ID] = "episode"
		x.setDuration(e.ID, e.DurationMs)
		eps := x.episodes[e.ShowID]
		if eps == nil {
			eps = map[seKey]uuid.UUID{}
			x.episodes[e.ShowID] = eps
		}
		k := seKey{int(e.SeasonNumber), int(e.EpisodeNumber)}
		if _, dup := eps[k]; dup {
			eps[k] = ambiguous // a double episode stored twice; leave it to paths
		} else {
			eps[k] = e.ID
		}
	}
	for _, f := range c.Files {
		x.types[f.MediaItemID] = f.Type
		x.setDuration(f.MediaItemID, f.FileDurationMs)
		x.setDuration(f.MediaItemID, f.ItemDurationMs)
		parts := pathParts(f.FilePath)
		for n := 2; n <= maxPathSuffix && n <= len(parts); n++ {
			setOrAmbiguous(x.paths[n], strings.Join(parts[len(parts)-n:], "/"), f.MediaItemID)
		}
		// The show's folder holds the file, or the season folder that does.
		// Both are indexed; a "Season 1" folder shared by every show ends
		// up ambiguous and never matches.
		if show, ok := episodeShow[f.MediaItemID]; ok {
			for up := 1; up <= 2; up++ {
				dir := parts[:max(len(parts)-up, 0)]
				for n := 1; n <= 2 && n <= len(dir); n++ {
					setOrAmbiguous(x.showDirs[n], strings.Join(dir[len(dir)-n:], "/"), show)
				}
			}
		}
	}
	return x
}

func setOrAmbiguous(m map[string]uuid.UUID, key string, id uuid.UUID) {
	if prev, ok := m[key]; ok && prev != id {
		m[key] = ambiguous
	} else {
		m[key] = id
	}
}

func (x *Index) setDuration(id uuid.UUID, ms *int64) {
	if ms != nil && *ms > 0 && x.durations[id] == 0 {
		x.durations[id] = *ms
	}
}

// pathParts splits a Unix or Windows path into lower-cased components.
func pathParts(p string) []string {
	p = strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Type is a matched item's type ("movie", "episode", "track", ...).
func (x *Index) Type(id uuid.UUID) string { return x.types[id] }

// Duration is a matched item's length in ms, 0 when unknown.
func (x *Index) Duration(id uuid.UUID) int64 { return x.durations[id] }

// Match returns this server's items for a source item, best first; nil when
// none matches. The file path is tried first: the two servers usually read
// the same files, and the path tells apart copies that share an ID (a 4K
// and an HD library). A show is found by its folder. Then external IDs: a
// movie or show by TMDB, TVDB or IMDb ID (every copy), an episode by its
// show's ID plus season and episode number.
func (x *Index) Match(it *Item) []uuid.UUID {
	if it.Kind == ItemShow {
		if parts := pathParts(it.Path); len(parts) > 0 {
			for n := min(2, len(parts)); n >= 1; n-- {
				if id, ok := x.showDirs[n][strings.Join(parts[len(parts)-n:], "/")]; ok {
					if id == ambiguous {
						break
					}
					return []uuid.UUID{id}
				}
			}
		}
	} else if parts := pathParts(it.Path); len(parts) > 0 {
		for n := min(maxPathSuffix, len(parts)); n >= minPathSuffix(it.Kind); n-- {
			id, ok := x.paths[n][strings.Join(parts[len(parts)-n:], "/")]
			if !ok {
				continue // the folders above differ; try fewer
			}
			if id == ambiguous {
				break // a shorter suffix is shared by at least as many
			}
			return []uuid.UUID{id}
		}
	}
	switch it.Kind {
	case ItemMovie, ItemVideo:
		return firstNonEmpty(x.byIDs(it.IDs, x.movieTMDB, x.movieTVDB, x.movieIMDB))
	case ItemShow:
		return firstNonEmpty(x.byIDs(it.IDs, x.showTMDB, x.showTVDB, x.showIMDB))
	case ItemEpisode:
		if !it.HasSE {
			return nil
		}
		var out []uuid.UUID
		for _, show := range x.byIDs(it.ShowIDs, x.showTMDB, x.showTVDB, x.showIMDB) {
			for _, sh := range show {
				if id, ok := x.episodes[sh][seKey{it.Season, it.Episode}]; ok && id != ambiguous && !contains(out, id) {
					out = append(out, id)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	case ItemTrack:
		// By path only: tracks carry no ID both servers share.
	}
	return nil
}

// minPathSuffix is the fewest trailing components a path match may rest on.
// A movie's "Alien (1979)/Alien.mkv" names it; an episode's
// "Season 1/S01E01.mkv" or a track's "Album/01 Intro.flac" doesn't, and a
// lone match on it could be another show's file when the source's show
// isn't here at all.
func minPathSuffix(k ItemKind) int {
	if k == ItemEpisode || k == ItemTrack {
		return 3
	}
	return 2
}

// byIDs looks up ids in each table, TVDB first for shows' sake (it's the
// ID TV agents agree on most) — callers take the first non-empty result.
func (x *Index) byIDs(ids ExternalIDs, tmdb, tvdb map[int][]uuid.UUID, imdb map[string][]uuid.UUID) [][]uuid.UUID {
	var out [][]uuid.UUID
	if ids.TVDB != 0 {
		out = append(out, tvdb[ids.TVDB])
	}
	if ids.TMDB != 0 {
		out = append(out, tmdb[ids.TMDB])
	}
	if ids.IMDB != "" {
		out = append(out, imdb[ids.IMDB])
	}
	return out
}

func firstNonEmpty(lists [][]uuid.UUID) []uuid.UUID {
	for _, l := range lists {
		if len(l) > 0 {
			return l
		}
	}
	return nil
}

func contains(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
