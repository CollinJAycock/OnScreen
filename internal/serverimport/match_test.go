package serverimport

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

func i32(n int32) *int32   { return &n }
func i64(n int64) *int64   { return &n }
func str(s string) *string { return &s }

// testLibrary is this server's side: an HD and a 4K copy of Alien, Heat,
// Severance with two episodes, two shows that both have an S01E01.mkv, and
// a track.
type testLibrary struct {
	alienHD, alien4K, heat, severance, sev101, sev102, showA, showB, aEp, bEp, track uuid.UUID
	idx                                                                              *Index
}

func newTestLibrary() *testLibrary {
	l := &testLibrary{}
	for _, p := range []*uuid.UUID{&l.alienHD, &l.alien4K, &l.heat, &l.severance, &l.sev101, &l.sev102,
		&l.showA, &l.showB, &l.aEp, &l.bEp, &l.track} {
		*p = uuid.New()
	}
	l.idx = NewIndex(Candidates{
		Items: []gen.ListImportCandidateItemsRow{
			{ID: l.alienHD, Type: "movie", TmdbID: i32(348), ImdbID: str("tt0078748"), DurationMs: i64(7000000)},
			{ID: l.alien4K, Type: "movie", TmdbID: i32(348)},
			{ID: l.heat, Type: "movie", ImdbID: str("tt0113277")},
			{ID: l.severance, Type: "show", TvdbID: i32(371980), TmdbID: i32(95396)},
		},
		Episodes: []gen.ListImportCandidateEpisodesRow{
			{ID: l.sev101, ShowID: l.severance, SeasonNumber: 1, EpisodeNumber: 1, DurationMs: i64(3000000)},
			{ID: l.sev102, ShowID: l.severance, SeasonNumber: 1, EpisodeNumber: 2},
		},
		Files: []gen.ListImportCandidateFilesRow{
			{MediaItemID: l.alienHD, FilePath: "/media/Media/Movies/Alien (1979)/Alien.mkv", Type: "movie"},
			{MediaItemID: l.alien4K, FilePath: "/media/Media/4k Movies/Alien (1979)/Alien.mkv", Type: "movie"},
			{MediaItemID: l.sev101, FilePath: "/media/Media/TV Shows/Severance/Season 1/S01E01.mkv", Type: "episode"},
			{MediaItemID: l.aEp, FilePath: "/media/Media/TV Shows/Show A/Season 1/S01E01.mkv", Type: "episode"},
			{MediaItemID: l.bEp, FilePath: "/media/Media/TV Shows/Show B/Season 1/S01E01.mkv", Type: "episode"},
			{MediaItemID: l.track, FilePath: "/media/Media/Music/Artist/Album/01 Song.flac", Type: "track", FileDurationMs: i64(200000)},
		},
	})
	return l
}

func ids(xs ...uuid.UUID) []uuid.UUID { return xs }

func sameIDs(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestIndexMatch(t *testing.T) {
	l := newTestLibrary()
	cases := []struct {
		name string
		item Item
		want []uuid.UUID
	}{
		// Paths: three trailing components, then two for a movie.
		{"movie by path (library folder differs)", Item{Kind: ItemMovie, Path: "/data/Films/Alien (1979)/Alien.mkv"}, nil},
		{"movie by full suffix", Item{Kind: ItemMovie, Path: "/mnt/x/4k Movies/Alien (1979)/Alien.mkv"}, ids(l.alien4K)},
		{"windows path, other case", Item{Kind: ItemMovie, Path: `D:\Media\MOVIES\Alien (1979)\alien.mkv`}, ids(l.alienHD)},
		{"episode by path", Item{Kind: ItemEpisode, Path: "/tv/Show B/Season 1/S01E01.mkv"}, ids(l.bEp)},
		{"episode path with only season/file is not enough", Item{Kind: ItemEpisode, Path: "/tv/Unknown/Season 1/S01E01.mkv"}, nil},
		{"track by path", Item{Kind: ItemTrack, Path: "/music/Artist/Album/01 Song.flac"}, ids(l.track)},
		{"track with another album folder", Item{Kind: ItemTrack, Path: "/music/Other/Album/01 Song.flac"}, nil},

		// IDs.
		{"movie by TMDB: every copy", Item{Kind: ItemMovie, IDs: ExternalIDs{TMDB: 348}}, ids(l.alienHD, l.alien4K)},
		{"movie by IMDb", Item{Kind: ItemMovie, IDs: ExternalIDs{IMDB: "tt0113277"}}, ids(l.heat)},
		{"movie whose path misses falls back to IDs", Item{Kind: ItemMovie, Path: "/x/y/z.mkv", IDs: ExternalIDs{IMDB: "tt0113277"}}, ids(l.heat)},
		{"show by TVDB", Item{Kind: ItemShow, IDs: ExternalIDs{TVDB: 371980}}, ids(l.severance)},
		{"show by TMDB", Item{Kind: ItemShow, IDs: ExternalIDs{TMDB: 95396}}, ids(l.severance)},
		{"a movie's TMDB ID is not a show's", Item{Kind: ItemShow, IDs: ExternalIDs{TMDB: 348}}, nil},
		// A show has no file: its folder is matched against where its
		// episodes' files are, before its IDs (the servers' metadata may
		// disagree about which show it is).
		{"show by folder", Item{Kind: ItemShow, Path: "/data/TV Shows/Severance", IDs: ExternalIDs{TVDB: 1}}, ids(l.severance)},
		{"show by folder name alone", Item{Kind: ItemShow, Path: `D:\Series\Severance\`}, ids(l.severance)},
		{"show folder not here", Item{Kind: ItemShow, Path: "/data/TV Shows/Other"}, nil},
		{"episode by show ID + number", Item{Kind: ItemEpisode, ShowIDs: ExternalIDs{TMDB: 95396}, Season: 1, Episode: 2, HasSE: true}, ids(l.sev102)},
		{"episode without numbers", Item{Kind: ItemEpisode, ShowIDs: ExternalIDs{TVDB: 371980}}, nil},
		{"episode not on this server", Item{Kind: ItemEpisode, ShowIDs: ExternalIDs{TVDB: 371980}, Season: 2, Episode: 1, HasSE: true}, nil},
		{"nothing to go on", Item{Kind: ItemMovie, Title: "Mystery"}, nil},
	}
	for _, c := range cases {
		if got := l.idx.Match(&c.item); !sameIDs(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIndexMatch_AmbiguousPathFallsBackToIDs(t *testing.T) {
	l := newTestLibrary()
	// "Alien (1979)/Alien.mkv" is in both movie libraries, and the source's
	// library folder matches neither: the IDs decide, and both copies match.
	it := Item{Kind: ItemMovie, Path: "/plex/Sci-Fi/Alien (1979)/Alien.mkv", IDs: ExternalIDs{TMDB: 348}}
	if got := l.idx.Match(&it); !sameIDs(got, ids(l.alienHD, l.alien4K)) {
		t.Errorf("got %v", got)
	}
}

func TestIndexTypesAndDurations(t *testing.T) {
	l := newTestLibrary()
	if l.idx.Type(l.sev101) != "episode" || l.idx.Type(l.alienHD) != "movie" || l.idx.Type(l.track) != "track" {
		t.Error("types not recorded")
	}
	if l.idx.Duration(l.alienHD) != 7000000 || l.idx.Duration(l.track) != 200000 || l.idx.Duration(l.heat) != 0 {
		t.Errorf("durations: %d %d %d", l.idx.Duration(l.alienHD), l.idx.Duration(l.track), l.idx.Duration(l.heat))
	}
}
