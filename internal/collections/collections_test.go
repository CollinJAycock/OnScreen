package collections

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata/nfo"
)

func intp(v int) *int { return &v }

func TestParseRules(t *testing.T) {
	ok := []string{
		`{"types":["movie"]}`,
		`{"types":["movie","show"],"genres":["Horror"],"year_min":1980,"year_max":1989,"rating_min":7.5,"limit":50}`,
		`{"types":["show"],"library_ids":["00000000-0000-0000-0000-000000000001"]}`,
	}
	for _, raw := range ok {
		if _, err := ParseRules([]byte(raw)); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	bad := []string{
		`{}`,
		`{"types":["episode"]}`,
		`{"types":["movie"],"year_min":2000,"year_max":1990}`,
		`{"types":["movie"],"rating_min":11}`,
		`{"types":["movie"],"limit":0}`,
		`{"types":["movie"],"limit":501}`,
		`{"types":["movie"],"library_ids":["nope"]}`,
		`not json`,
	}
	for _, raw := range bad {
		if _, err := ParseRules([]byte(raw)); !errors.Is(err, ErrInvalidRules) {
			t.Errorf("%s: err %v, want ErrInvalidRules", raw, err)
		}
	}
}

type smartFake struct {
	libs     []gen.Library
	smart    []gen.ListSmartCollectionsRow
	matches  []uuid.UUID
	params   []gen.ListMediaItemsForSmartPlaylistParams
	deleted  []gen.DeleteCollectionItemsNotInParams
	upserted []gen.UpsertCollectionItemPositionsParams
}

func (f *smartFake) ListLibraries(context.Context) ([]gen.Library, error) { return f.libs, nil }
func (f *smartFake) ListSmartCollections(context.Context) ([]gen.ListSmartCollectionsRow, error) {
	return f.smart, nil
}
func (f *smartFake) ListMediaItemsForSmartPlaylist(_ context.Context, p gen.ListMediaItemsForSmartPlaylistParams) ([]gen.ListMediaItemsForSmartPlaylistRow, error) {
	f.params = append(f.params, p)
	out := make([]gen.ListMediaItemsForSmartPlaylistRow, 0, len(f.matches))
	for _, id := range f.matches {
		out = append(out, gen.ListMediaItemsForSmartPlaylistRow{ID: id})
	}
	return out, nil
}
func (f *smartFake) DeleteCollectionItemsNotIn(_ context.Context, p gen.DeleteCollectionItemsNotInParams) error {
	f.deleted = append(f.deleted, p)
	return nil
}
func (f *smartFake) UpsertCollectionItemPositions(_ context.Context, p gen.UpsertCollectionItemPositionsParams) error {
	f.upserted = append(f.upserted, p)
	return nil
}

func TestSmartRefresh(t *testing.T) {
	lib1, lib2 := uuid.New(), uuid.New()
	a, b := uuid.New(), uuid.New()
	f := &smartFake{libs: []gen.Library{{ID: lib1}, {ID: lib2}}, matches: []uuid.UUID{a, b}}
	s := NewSmart(f, slog.Default())
	col := uuid.New()
	y := 1980
	n, err := s.Refresh(context.Background(), col, Rules{Types: []string{"movie"}, Genres: []string{"Horror", "Comedy"}, YearMin: &y})
	if err != nil || n != 2 {
		t.Fatalf("refresh = %d, %v", n, err)
	}
	p := f.params[0]
	if len(p.LibraryIds) != 2 || p.ResultLimit != defaultSmartLimit || p.Genre == nil || *p.Genre != "Horror" || p.YearMin == nil || *p.YearMin != 1980 || p.YearMax != nil {
		t.Fatalf("query params %+v", p)
	}
	if f.deleted[0].CollectionID != col || len(f.deleted[0].ItemIds) != 2 {
		t.Fatalf("delete %+v", f.deleted)
	}
	if up := f.upserted[0]; up.CollectionID != col || up.ItemIds[0] != a || up.ItemIds[1] != b {
		t.Fatalf("upsert %+v", up)
	}

	// Library-limited rules query only those libraries.
	f.params = nil
	if _, err := s.Refresh(context.Background(), col, Rules{Types: []string{"show"}, LibraryIDs: []string{lib2.String()}, Limit: intp(5)}); err != nil {
		t.Fatal(err)
	}
	if p := f.params[0]; len(p.LibraryIds) != 1 || p.LibraryIds[0] != lib2 || p.ResultLimit != 5 {
		t.Fatalf("limited params %+v", p)
	}

	// Nothing matching empties the collection (and adds nothing).
	f.matches, f.upserted, f.deleted = nil, nil, nil
	if n, err := s.Refresh(context.Background(), col, Rules{Types: []string{"movie"}}); err != nil || n != 0 {
		t.Fatalf("empty refresh = %d, %v", n, err)
	}
	if len(f.deleted) != 1 || len(f.deleted[0].ItemIds) != 0 || len(f.upserted) != 0 {
		t.Fatalf("empty refresh wrote delete %+v upsert %+v", f.deleted, f.upserted)
	}

	if _, err := s.Refresh(context.Background(), col, Rules{}); !errors.Is(err, ErrInvalidRules) {
		t.Fatalf("invalid rules err = %v", err)
	}
}

func TestSmartRefreshAll(t *testing.T) {
	f := &smartFake{
		libs:    []gen.Library{{ID: uuid.New()}},
		matches: []uuid.UUID{uuid.New()},
		smart: []gen.ListSmartCollectionsRow{
			{ID: uuid.New(), Rules: []byte(`{"types":["movie"]}`)},
			{ID: uuid.New(), Rules: []byte(`{"types":["episode"]}`)}, // stored before a rule change: skipped
			{ID: uuid.New(), Rules: []byte(`{"types":["show"]}`)},
		},
	}
	got, err := NewSmart(f, slog.Default()).RefreshAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "2 smart collections refreshed (2 members), 1 failed"; got != want {
		t.Fatalf("summary %q, want %q", got, want)
	}
}

func TestGroupings(t *testing.T) {
	m := &nfo.Movie{Set: "Alien Collection", Tags: []string{"Halloween", " halloween ", "", "Favourites"}}
	if g := groupings(m, NFOOff); g != nil {
		t.Fatalf("off mode = %v", g)
	}
	if g := groupings(m, NFOSets); len(g) != 1 || g[0] != [2]string{"set:alien collection", "Alien Collection"} {
		t.Fatalf("sets mode = %v", g)
	}
	g := groupings(m, NFOSetsAndTags)
	if len(g) != 3 || g[1] != [2]string{"tag:halloween", "Halloween"} || g[2][0] != "tag:favourites" {
		t.Fatalf("sets_and_tags mode = %v", g)
	}
	if g := groupings(&nfo.Movie{}, NFOSetsAndTags); len(g) != 0 {
		t.Fatalf("no groupings = %v", g)
	}
}

type nfoFake struct {
	// knownSets: film series the server already tracks, by TMDB id or
	// lower-cased name.
	knownIDs   map[int32]bool
	knownNames map[string]bool
	libs       map[uuid.UUID]gen.Library
	files      map[uuid.UUID][]gen.MediaFile
	cols       map[string]uuid.UUID // source_key → id
	names      map[uuid.UUID]string
	members    map[uuid.UUID][]uuid.UUID
}

func newNFOFake() *nfoFake {
	return &nfoFake{
		libs:    map[uuid.UUID]gen.Library{},
		files:   map[uuid.UUID][]gen.MediaFile{},
		cols:    map[string]uuid.UUID{},
		names:   map[uuid.UUID]string{},
		members: map[uuid.UUID][]uuid.UUID{},
	}
}

func (f *nfoFake) IsKnownTMDBCollection(_ context.Context, p gen.IsKnownTMDBCollectionParams) (bool, error) {
	return f.knownIDs[p.TmdbID] || f.knownNames[strings.ToLower(p.Name)], nil
}

func (f *nfoFake) GetLibrary(_ context.Context, id uuid.UUID) (gen.Library, error) {
	l, ok := f.libs[id]
	if !ok {
		return gen.Library{}, pgx.ErrNoRows
	}
	return l, nil
}
func (f *nfoFake) ListLibraries(context.Context) ([]gen.Library, error) {
	var out []gen.Library
	for _, l := range f.libs {
		out = append(out, l)
	}
	return out, nil
}
func (f *nfoFake) ListActiveFilesForLibrary(_ context.Context, id uuid.UUID) ([]gen.MediaFile, error) {
	return f.files[id], nil
}
func (f *nfoFake) FindOrCreateSourceCollection(_ context.Context, p gen.FindOrCreateSourceCollectionParams) (uuid.UUID, error) {
	if *p.Source != "nfo" {
		return uuid.Nil, errors.New("wrong source")
	}
	if id, ok := f.cols[*p.SourceKey]; ok {
		return id, nil
	}
	id := uuid.New()
	f.cols[*p.SourceKey], f.names[id] = id, p.Name
	return id, nil
}
func (f *nfoFake) AddCollectionItem(_ context.Context, p gen.AddCollectionItemParams) (gen.CollectionItem, error) {
	for _, id := range f.members[p.CollectionID] {
		if id == p.MediaItemID {
			return gen.CollectionItem{}, pgx.ErrNoRows
		}
	}
	f.members[p.CollectionID] = append(f.members[p.CollectionID], p.MediaItemID)
	return gen.CollectionItem{}, nil
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNFOSweep(t *testing.T) {
	dir := t.TempDir()
	f := newNFOFake()
	lib := gen.Library{ID: uuid.New(), Name: "Movies", NfoCollections: NFOSetsAndTags}
	off := gen.Library{ID: uuid.New(), Name: "Other", NfoCollections: NFOOff}
	f.libs[lib.ID], f.libs[off.ID] = lib, off

	alien, aliens, plain, broken, episode := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	// movie.nfo and <basename>.nfo layouts; a Set with a tag; two versions of
	// one movie (read once); a movie without an NFO; an unparsable one; an
	// episode NFO (not a movie: skipped).
	writeFile(t, filepath.Join(dir, "Alien (1979)", "movie.nfo"),
		`<movie><title>Alien</title><set><name>Alien Collection</name></set><tag>Halloween</tag></movie>`)
	writeFile(t, filepath.Join(dir, "Aliens (1986)", "Aliens (1986).nfo"),
		`<movie><title>Aliens</title><set>Alien Collection</set></movie>`)
	writeFile(t, filepath.Join(dir, "Broken", "movie.nfo"), `<movie><title>`)
	writeFile(t, filepath.Join(dir, "Show", "S01E01.nfo"), `<episodedetails><title>Pilot</title></episodedetails>`)
	f.files[lib.ID] = []gen.MediaFile{
		{MediaItemID: alien, FilePath: filepath.Join(dir, "Alien (1979)", "Alien.mkv")},
		{MediaItemID: alien, FilePath: filepath.Join(dir, "Alien (1979)", "Alien 4K.mkv")},
		{MediaItemID: aliens, FilePath: filepath.Join(dir, "Aliens (1986)", "Aliens (1986).mkv")},
		{MediaItemID: plain, FilePath: filepath.Join(dir, "Plain", "Plain.mkv")},
		{MediaItemID: broken, FilePath: filepath.Join(dir, "Broken", "Broken.mkv")},
		{MediaItemID: episode, FilePath: filepath.Join(dir, "Show", "S01E01.mkv")},
	}
	f.files[off.ID] = f.files[lib.ID]

	imp := NewNFOImporter(f, slog.Default())
	summary, err := imp.SweepAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "Movies: 2 NFO files read, 3 collection memberships") || strings.Contains(summary, "Other") {
		t.Fatalf("summary %q", summary)
	}
	set := f.cols["set:alien collection"]
	if f.names[set] != "Alien Collection" || len(f.members[set]) != 2 {
		t.Fatalf("set collection %q members %v", f.names[set], f.members[set])
	}
	if tag := f.cols["tag:halloween"]; len(f.members[tag]) != 1 || f.members[tag][0] != alien {
		t.Fatalf("tag collection members %v", f.members[tag])
	}
	if len(f.cols) != 2 {
		t.Fatalf("collections %v", f.cols)
	}

	// Re-running joins the same collections without duplicating members.
	if _, err := imp.SweepLibrary(context.Background(), lib.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.cols) != 2 || len(f.members[set]) != 2 {
		t.Fatalf("re-sweep: collections %v, set members %v", f.cols, f.members[set])
	}

	// Sets-only mode ignores tags; the scanner hook honours the library mode.
	lib.NfoCollections = NFOSets
	f.libs[lib.ID] = lib
	newMovie := uuid.New()
	n, err := imp.ImportMovie(context.Background(), lib.ID, newMovie, &nfo.Movie{Set: "Alien Collection", Tags: []string{"Space"}})
	if err != nil || n != 1 {
		t.Fatalf("import = %d, %v", n, err)
	}
	if _, ok := f.cols["tag:space"]; ok {
		t.Fatal("sets mode created a tag collection")
	}
	if n, _ := imp.ImportMovie(context.Background(), off.ID, uuid.New(), &nfo.Movie{Set: "Alien Collection"}); n != 0 {
		t.Fatalf("off library imported %d", n)
	}
}

// A <set> the server already tracks as a film series (by TMDB id or name) is
// skipped; the movie's own sets and tags still import.
func TestNFOImportSkipsKnownFilmSeries(t *testing.T) {
	f := newNFOFake()
	f.knownIDs = map[int32]bool{10919: true}
	f.knownNames = map[string]bool{"alien collection": true}
	lib := gen.Library{ID: uuid.New(), Name: "Movies", NfoCollections: NFOSetsAndTags}
	f.libs[lib.ID] = lib
	imp := NewNFOImporter(f, slog.Default())
	ctx := context.Background()

	if n, err := imp.ImportMovie(ctx, lib.ID, uuid.New(), &nfo.Movie{Set: "The Omen Collection", SetTMDBID: 10919, Tags: []string{"Halloween"}}); err != nil || n != 1 {
		t.Fatalf("import = %d, %v; want only the tag", n, err)
	}
	if n, _ := imp.ImportMovie(ctx, lib.ID, uuid.New(), &nfo.Movie{Set: "Alien Collection"}); n != 0 {
		t.Fatalf("a series known by name imported %d", n)
	}
	if n, _ := imp.ImportMovie(ctx, lib.ID, uuid.New(), &nfo.Movie{Set: "Best Picture Winners"}); n != 1 {
		t.Fatalf("a hand-made set imported %d", n)
	}
	if _, ok := f.cols["set:the omen collection"]; ok {
		t.Fatal("a known film series became a collection")
	}
	if _, ok := f.cols["set:best picture winners"]; !ok || f.cols["tag:halloween"] == uuid.Nil {
		t.Fatalf("collections %v", f.cols)
	}
}
