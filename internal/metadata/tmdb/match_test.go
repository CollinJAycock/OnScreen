package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Movie auto-match against the films a fresh server mismatched on
// 2026-10-01 ("Spring (2019)" → Spring Breakers, "Hero (2018)" →
// Chestnut: Hero of Central Park, "Singularity (2026)" → a 21-minute
// namesake). Ids, titles, dates and runtimes are TMDB's, read off
// themoviedb.org that day; result order follows the site's searches,
// except that "Hero" with year=2018 puts Chestnut first, as the server's
// API call did (the site ranks it third).

type fixtureMovie struct {
	id          int
	title       string
	releaseDate string
	runtimeMin  int // details only; 0 = TMDB lists none
	altTitles   []string
}

var fixtureMovies = []fixtureMovie{
	{id: 593048, title: "Spring", releaseDate: "2019-04-04", runtimeMin: 8},
	{id: 657894, title: "Spring", releaseDate: "2019-08-08", runtimeMin: 8},
	{id: 122081, title: "Spring Breakers", releaseDate: "2013-03-15"},
	{id: 487320, title: "Red Spring", releaseDate: "2017-11-23"},
	{id: 639410, title: "Spring Follows Winter", releaseDate: "2019-10-04"},
	{id: 588399, title: "Spring Breakaway", releaseDate: "2019-03-15"},
	{id: 631886, title: "Spring Fever", releaseDate: "2019-06-06"},

	{id: 1070322, title: "Hero", releaseDate: "2018-06-20", runtimeMin: 14},
	{id: 615324, title: "HERO", releaseDate: "2018-04-16", runtimeMin: 4},
	{id: 665974, title: "HERO", releaseDate: "2018-07-30", runtimeMin: 7},
	{id: 1759847, title: "Hero", releaseDate: "2018-08-10", runtimeMin: 19},
	{id: 614603, title: "Hero", releaseDate: "2018-05-08", runtimeMin: 5},
	{id: 34672, title: "Chestnut: Hero of Central Park", releaseDate: "2004-10-21"},
	{id: 983254, title: "Hero", releaseDate: "2017-09-15"},
	{id: 505262, title: "My Hero Academia: Two Heroes", releaseDate: "2018-09-25"},

	{id: 1735105, title: "Singularity", releaseDate: "2026-08-08", runtimeMin: 21},
	{id: 1687677, title: "Singularity", releaseDate: "2026-05-08", runtimeMin: 6},
	{id: 1750174, title: "The Singularity Protocol", releaseDate: "2026-01-31"},

	{id: 671, title: "Harry Potter and the Philosopher's Stone", releaseDate: "2001-11-16", runtimeMin: 152,
		altTitles: []string{"Harry Potter and the Sorcerer's Stone"}},
}

// fixtureSearches maps "query|filter=year" to result ids in TMDB's order.
var fixtureSearches = map[string][]int{
	"Spring|year=2019":                      {122081, 593048, 487320, 657894, 639410, 588399, 631886},
	"Spring|primary_release_year=2019":      {593048, 657894, 639410, 588399, 631886},
	"Hero|year=2018":                        {34672, 1070322, 615324, 983254, 665974, 1759847, 505262, 614603},
	"Hero|primary_release_year=2018":        {1070322, 615324, 665974, 1759847, 505262, 614603},
	"Singularity|year=2026":                 {1735105, 1687677, 1750174},
	"Singularity|primary_release_year=2026": {1735105, 1687677, 1750174},

	"Harry Potter and the Sorcerer's Stone|primary_release_year=2001": {671},
}

// fakeTMDB serves /search/movie from a searches table and /movie/{id}
// (+ /alternative_titles) from fixtureMovies, counting requests per path.
type fakeTMDB struct {
	searches map[string][]int
	mu       sync.Mutex
	hits     map[string]int // path, or "search:<key>" for searches
}

func newFakeTMDB(t *testing.T, searches map[string][]int) (*Client, *fakeTMDB) {
	t.Helper()
	f := &fakeTMDB{searches: searches, hits: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return testClient(t, srv), f
}

func (f *fakeTMDB) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[key]
}

func (f *fakeTMDB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/3")
	key := path
	if path == "/search/movie" {
		q := r.URL.Query()
		key = "search:" + q.Get("query") + "|"
		for _, p := range []string{"primary_release_year", "year"} {
			if v := q.Get(p); v != "" {
				key += p + "=" + v
			}
		}
	}
	f.mu.Lock()
	f.hits[key]++
	f.mu.Unlock()

	if path == "/search/movie" {
		rows := []map[string]any{}
		for _, id := range f.searches[strings.TrimPrefix(key, "search:")] {
			m := fixtureByID(id)
			rows = append(rows, map[string]any{"id": m.id, "title": m.title, "original_title": m.title, "release_date": m.releaseDate})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": rows})
		return
	}
	rest, alt := strings.CutSuffix(strings.TrimPrefix(path, "/movie/"), "/alternative_titles")
	id, err := strconv.Atoi(rest)
	m := fixtureByID(id)
	if err != nil || m.id == 0 {
		http.NotFound(w, r)
		return
	}
	if alt {
		titles := []map[string]any{}
		for _, a := range m.altTitles {
			titles = append(titles, map[string]any{"iso_3166_1": "US", "title": a})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": m.id, "titles": titles})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": m.id, "title": m.title, "original_title": m.title,
		"release_date": m.releaseDate, "runtime": m.runtimeMin,
		"release_dates": map[string]any{"results": []any{}},
	})
}

func fixtureByID(id int) fixtureMovie {
	for _, m := range fixtureMovies {
		if m.id == id {
			return m
		}
	}
	return fixtureMovie{}
}

// detailCalls counts /movie/{id} requests, alternative titles excluded.
func (f *fakeTMDB) detailCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for k, v := range f.hits {
		if strings.HasPrefix(k, "/movie/") && !strings.HasSuffix(k, "/alternative_titles") {
			n += v
		}
	}
	return n
}

func TestSearchMovie_ExactTitleAndYearBeatTMDBTopHit(t *testing.T) {
	c, f := newFakeTMDB(t, fixtureSearches)
	res, err := c.SearchMovie(context.Background(), "Spring", 2019)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.TMDBID != 593048 {
		t.Fatalf("Spring (2019): got %d %q (%d), want 593048 Spring (2019)", res.TMDBID, res.Title, res.Year)
	}
	if f.count("search:Spring|primary_release_year=2019") != 1 || f.count("search:Spring|year=2019") != 0 {
		t.Fatalf("searches: %v (want the primary_release_year query only)", f.hits)
	}

	// Without a runtime, "Hero" 2018 can only be narrowed to the five
	// films of that title and year; it must never be the 2004 Chestnut.
	res, err = c.SearchMovie(context.Background(), "Hero", 2018)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if foldTitle(res.Title) != "hero" || res.Year != 2018 {
		t.Fatalf("Hero (2018): got %d %q (%d), want a film titled Hero from 2018", res.TMDBID, res.Title, res.Year)
	}
}

// The year= fallback holds the old query's results, with the popular
// near-miss on top. Only the exact title and year may come out of it.
func TestSearchMovie_FallbackSkipsPartialTitleAndWrongYear(t *testing.T) {
	searches := map[string][]int{
		"Spring|year=2019": fixtureSearches["Spring|year=2019"],
		"Hero|year=2018":   fixtureSearches["Hero|year=2018"],
	}
	c, _ := newFakeTMDB(t, searches)
	for _, tc := range []struct {
		title string
		year  int
	}{{"Spring", 2019}, {"Hero", 2018}} {
		res, err := c.SearchMovie(context.Background(), tc.title, tc.year)
		if err != nil {
			t.Fatalf("%s: %v", tc.title, err)
		}
		if foldTitle(res.Title) != foldTitle(tc.title) || res.Year != tc.year {
			t.Errorf("%s (%d): got %d %q (%d)", tc.title, tc.year, res.TMDBID, res.Title, res.Year)
		}
	}
}

func TestSearchMovie_RefusesWhenOnlyNearMissesRemain(t *testing.T) {
	// The same "Spring" year=2019 results with both films actually named
	// Spring gone: a partial title from the right year (Spring Fever), a
	// partial title two years off (Red Spring), and Spring Breakers.
	searches := map[string][]int{"Spring|year=2019": {122081, 487320, 639410, 631886}}
	c, f := newFakeTMDB(t, searches)
	res, err := c.SearchMovie(context.Background(), "Spring", 2019)
	if err == nil {
		t.Fatalf("got %d %q (%d), want no match", res.TMDBID, res.Title, res.Year)
	}
	if !strings.Contains(err.Error(), "no confident match") {
		t.Errorf("error: %v", err)
	}
	if n := f.detailCalls(); n != 0 {
		t.Errorf("details calls: %d, want 0 for a refused match", n)
	}
	// Alternative titles are checked only for the rows within a year.
	if f.count("/movie/639410/alternative_titles") != 1 || f.count("/movie/631886/alternative_titles") != 1 ||
		f.count("/movie/122081/alternative_titles") != 0 || f.count("/movie/487320/alternative_titles") != 0 {
		t.Errorf("alternative title lookups: %v", f.hits)
	}
}

func TestSearchMovie_MatchesAlternativeTitle(t *testing.T) {
	c, _ := newFakeTMDB(t, fixtureSearches)
	res, err := c.SearchMovie(context.Background(), "Harry Potter and the Sorcerer's Stone", 2001)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.TMDBID != 671 {
		t.Fatalf("got %d %q, want 671", res.TMDBID, res.Title)
	}
}

func TestSearchMovieWithRuntime_PicksNamesakeByRuntime(t *testing.T) {
	for _, tc := range []struct {
		title   string
		year    int
		runtime time.Duration
		want    int
	}{
		// Blender's HERO is 4 minutes; its namesakes run 14, 7, 19 and 5.
		{"Hero", 2018, 4 * time.Minute, 615324},
		// Blender's Singularity is 6 minutes; TMDB ranks a 21-minute one first.
		{"Singularity", 2026, 6 * time.Minute, 1687677},
		// Both 2019 Springs are 8 minutes: TMDB's order decides.
		{"Spring", 2019, 7*time.Minute + 44*time.Second, 593048},
		// A runtime no namesake is near (a long cut, a multi-part file)
		// falls back to TMDB's order rather than refusing the title match.
		{"Hero", 2018, 95 * time.Minute, 1070322},
	} {
		t.Run(fmt.Sprintf("%s_%s", tc.title, tc.runtime), func(t *testing.T) {
			c, f := newFakeTMDB(t, fixtureSearches)
			res, err := c.SearchMovieWithRuntime(context.Background(), tc.title, tc.year, tc.runtime)
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if res.TMDBID != tc.want {
				t.Fatalf("got %d %q (%d, %d min), want %d", res.TMDBID, res.Title, res.Year, res.DurationMS/60000, tc.want)
			}
			if n := f.count(fmt.Sprintf("/movie/%d", tc.want)); n != 1 {
				t.Errorf("details of the pick fetched %d times, want 1", n)
			}
		})
	}
}

func TestMatchByTitle(t *testing.T) {
	row := func(id int, title, date string) tmdbMovie {
		return tmdbMovie{ID: id, Title: title, ReleaseDate: date}
	}
	ids := func(rows []tmdbMovie) []int {
		out := []int{}
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	for _, tc := range []struct {
		name  string
		title string
		year  int
		rows  []tmdbMovie
		want  []int
	}{
		{"exact year beats a year off", "Example Film", 2021,
			[]tmdbMovie{row(1, "Example Film", "2020-12-01"), row(2, "Example Film", "2021-09-15")}, []int{2}},
		{"a year off when nothing is exact", "Example Film", 2021,
			[]tmdbMovie{row(1, "Example Film", "2020-12-01"), row(2, "Example Film", "2023-01-01")}, []int{1}},
		{"two years off is refused", "Example Film", 2021,
			[]tmdbMovie{row(1, "Example Film", "2019-01-01"), row(2, "Example Film", "2023-01-01")}, []int{}},
		{"undated is refused when a year was parsed", "Example Film", 2021,
			[]tmdbMovie{row(1, "Example Film", "")}, []int{}},
		{"no parsed year takes any year", "Example Film", 0,
			[]tmdbMovie{row(1, "Example Film Part Two", "2024-02-27"), row(2, "Example Film", "1984-12-14")}, []int{2}},
		{"partial titles are refused", "Spring", 2019,
			[]tmdbMovie{row(1, "Spring Breakers", "2019-01-01"), row(2, "Spring Fever", "2019-06-06")}, []int{}},
		{"title inside a longer title is refused", "Hero", 2018,
			[]tmdbMovie{row(1, "Chestnut: Hero of Central Park", "2018-01-01")}, []int{}},
		{"original title counts", "万引き家族", 2018,
			[]tmdbMovie{{ID: 505192, Title: "Shoplifters", OriginalTitle: "万引き家族", ReleaseDate: "2018-06-02"}}, []int{505192}},
		{"ties keep TMDB's order", "Singularity", 2026,
			[]tmdbMovie{row(1735105, "Singularity", "2026-08-08"), row(1687677, "Singularity", "2026-05-08")}, []int{1735105, 1687677}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ids(matchByTitle(tc.rows, tc.title, tc.year))
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFoldTitle(t *testing.T) {
	same := [][2]string{
		// Filename-derived title vs TMDB's, for the films the same server
		// matched correctly.
		{"Big Buck Bunny", "Big Buck Bunny"},
		{"Elephants Dream", "Elephants Dream"},
		{"Wing It!", "Wing It!"},
		{"Wing It", "Wing It!"},
		{"Coffee Run", "Coffee Run"},
		{"Tears of Steel", "Tears of Steel"},
		{"Hero", "HERO"},
		{"Spider Man", "Spider-Man"},
		{"Spiderman", "Spider-Man"},
		{"S W A T", "S.W.A.T."},
		{"Amelie", "Amélie"},
		{"Fast and Furious", "Fast & Furious"},
		{"Avengers", "The Avengers"},
		{"Schindlers List", "Schindler's List"},
		{"Mission Impossible Fallout", "Mission: Impossible - Fallout"},
		{"ＡＫＩＲＡ", "AKIRA"},
	}
	for _, p := range same {
		if foldTitle(p[0]) != foldTitle(p[1]) {
			t.Errorf("%q and %q should fold alike: %q vs %q", p[0], p[1], foldTitle(p[0]), foldTitle(p[1]))
		}
	}
	differ := [][2]string{
		{"Spring", "Spring Breakers"},
		{"Hero", "Chestnut: Hero of Central Park"},
		{"Singularity", "The Singularity Protocol"},
		{"千と千尋の神隠し", "もののけ姫"},
		{"Кино", "Сталкер"},
	}
	for _, p := range differ {
		if foldTitle(p[0]) == foldTitle(p[1]) {
			t.Errorf("%q and %q must not fold alike (both %q)", p[0], p[1], foldTitle(p[0]))
		}
	}
	if foldTitle("千と千尋の神隠し") == "" {
		t.Error("a title in another script must not fold to nothing")
	}
}
