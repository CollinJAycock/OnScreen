package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Show auto-match. Ids, names, first air dates, origin countries and
// alternative titles are TMDB's, read off themoviedb.org on 2026-10-02;
// result order follows the site's searches, filtered to the first-air year
// for the first_air_date_year queries.

type fixtureShow struct {
	id        int
	name      string
	firstAir  string
	country   string
	altTitles []string
}

var fixtureShows = []fixtureShow{
	{id: 2316, name: "The Office", firstAir: "2005-03-24", country: "US"},
	{id: 2996, name: "The Office", firstAir: "2001-07-09", country: "GB"},
	{id: 90762, name: "The Office", firstAir: "2019-09-15", country: "IN"},
	{id: 256480, name: "The Office", firstAir: "2024-10-18", country: "AU"},
	{id: 24876, name: "The Office", firstAir: "1995-03-11", country: "US"},
	{id: 128822, name: "The Office PL", firstAir: "2021-10-22", country: "PL"},
	{id: 37054, name: "The Office", firstAir: "2010-08-10", country: "IL"},

	{id: 57243, name: "Doctor Who", firstAir: "2005-03-26", country: "GB"},
	{id: 121, name: "Doctor Who", firstAir: "1963-11-23", country: "GB"},
	{id: 239770, name: "Doctor Who", firstAir: "2024-05-11", country: "GB"},
	{id: 1056, name: "Doctor Who Confidential", firstAir: "2005-03-26", country: "GB"},

	{id: 126308, name: "Shōgun", firstAir: "2024-02-27", country: "US"},
	{id: 13862, name: "Shōgun", firstAir: "1980-09-15", country: "US"},

	{id: 82452, name: "Avatar: The Last Airbender", firstAir: "2024-02-22", country: "US",
		altTitles: []string{"Avatar the Last Airbender", "Avatar - Der Herr der Elemente"}},
	{id: 246, name: "Avatar: The Last Airbender", firstAir: "2005-02-21", country: "US",
		altTitles: []string{"ATLA", "Avatar", "Avatar: The Legend of Aang", "Avatar: Der Herr der Elemente"}},
	{id: 284833, name: "Avatar: Seven Havens", firstAir: "2026-10-09", country: "US"},
	{id: 71194, name: "The King's Avatar", firstAir: "2017-04-07", country: "CN"},
}

// fixtureShowSearches maps "query|first_air_date_year" ("" for none) to
// result ids in TMDB's order.
var fixtureShowSearches = map[string][]int{
	"The Office|":                     {2316, 2996, 90762, 256480, 24876, 128822, 37054},
	"Doctor Who|":                     {57243, 121, 239770, 1056},
	"Doctor Who|2005":                 {57243, 1056},
	"Shogun|2024":                     {126308},
	"Shogun|1980":                     {13862},
	"Avatar|":                         {82452, 246, 284833, 71194},
	"Avatar: The Last Airbender|":     {82452, 246},
	"Avatar: The Last Airbender|2005": {246},
	// A made-up longer name, answered with the real shorter one.
	"The Office: An American Workplace|": {2316, 2996},
}

// fakeTMDBTV serves /search/tv from fixtureShowSearches and /tv/{id}/...
// from fixtureShows, counting requests per path ("search:<key>" for
// searches).
type fakeTMDBTV struct {
	mu   sync.Mutex
	hits map[string]int
}

func newFakeTMDBTV(t *testing.T) (*Client, *fakeTMDBTV) {
	t.Helper()
	f := &fakeTMDBTV{hits: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return testClient(t, srv), f
}

func (f *fakeTMDBTV) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[key]
}

func (f *fakeTMDBTV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/3")
	key := path
	if path == "/search/tv" {
		key = "search:" + r.URL.Query().Get("query") + "|" + r.URL.Query().Get("first_air_date_year")
	}
	f.mu.Lock()
	f.hits[key]++
	f.mu.Unlock()

	if path == "/search/tv" {
		rows := []map[string]any{}
		for _, id := range fixtureShowSearches[strings.TrimPrefix(key, "search:")] {
			s := showByID(id)
			rows = append(rows, map[string]any{
				"id": s.id, "name": s.name, "original_name": s.name,
				"first_air_date": s.firstAir, "origin_country": []string{s.country},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": rows})
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/tv/"), "/")
	id, err := strconv.Atoi(parts[0])
	s := showByID(id)
	if err != nil || s.id == 0 || len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "alternative_titles":
		results := []map[string]any{}
		for _, a := range s.altTitles {
			results = append(results, map[string]any{"iso_3166_1": "US", "title": a})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": s.id, "results": results})
	case "external_ids":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": s.id})
	case "content_ratings":
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	default:
		http.NotFound(w, r)
	}
}

func showByID(id int) fixtureShow {
	for _, s := range fixtureShows {
		if s.id == id {
			return s
		}
	}
	return fixtureShow{}
}

func TestSearchTV_PicksExactNameAndYear(t *testing.T) {
	for _, tc := range []struct {
		name  string
		title string
		year  int
		want  int
	}{
		// Six shows are called The Office; a TVDB/Sonarr country qualifier
		// picks one, and without it TMDB's order stands.
		{"country qualifier US", "The Office (US)", 0, 2316},
		{"country qualifier UK", "The Office (UK)", 0, 2996},
		{"no qualifier", "The Office", 0, 2316},
		// The parsed year picks between the 1963 and 2005 Doctor Who; a
		// year qualifier stands in for a missing parsed year.
		{"parsed year", "Doctor Who", 2005, 57243},
		{"year qualifier", "Doctor Who (2005)", 0, 57243},
		{"older year via the unfiltered search", "Doctor Who", 1963, 121},
		{"a year off via the unfiltered search", "Doctor Who", 2006, 57243},
		// Accents fold away; the year picks the remake.
		{"accents", "Shogun", 2024, 126308},
		{"accents, 1980", "Shogun", 1980, 13862},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newFakeTMDBTV(t)
			res, err := c.SearchTV(context.Background(), tc.title, tc.year)
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if res.TMDBID != tc.want {
				t.Fatalf("got %d %q (%d), want %d", res.TMDBID, res.Title, res.FirstAirYear, tc.want)
			}
		})
	}
}

func TestSearchTV_QualifierIsNotSearchedFor(t *testing.T) {
	c, f := newFakeTMDBTV(t)
	if _, err := c.SearchTV(context.Background(), "The Office (US)", 0); err != nil {
		t.Fatalf("search: %v", err)
	}
	if f.count("search:The Office|") != 1 || f.count("search:The Office (US)|") != 0 {
		t.Fatalf("searches: %v", f.hits)
	}
}

// "Avatar" names no show exactly: TMDB's top hits are both Avatar: The
// Last Airbender. The old prefix rule took the first, the 2024 live-action
// series; the 2005 animated one lists "Avatar" among its US titles.
func TestSearchTV_MatchesAlternativeTitle(t *testing.T) {
	c, f := newFakeTMDBTV(t)
	res, err := c.SearchTV(context.Background(), "Avatar", 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.TMDBID != 246 {
		t.Fatalf("got %d %q (%d), want 246", res.TMDBID, res.Title, res.FirstAirYear)
	}
	if f.count("/tv/82452/alternative_titles") != 1 || f.count("/tv/246/alternative_titles") != 1 {
		t.Errorf("alternative title lookups: %v", f.hits)
	}
}

func TestSearchTV_RefusesNearMisses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		title string
		year  int
	}{
		// Three Doctor Whos, none first aired within a year of 2015.
		{"wrong year", "Doctor Who", 2015},
		// Only a longer name on offer (the old prefix rule took it).
		{"longer name only", "The Office: An American Workplace", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := newFakeTMDBTV(t)
			res, err := c.SearchTV(context.Background(), tc.title, tc.year)
			if err == nil {
				t.Fatalf("got %d %q (%d), want no match", res.TMDBID, res.Title, res.FirstAirYear)
			}
			if !strings.Contains(err.Error(), "no confident TV match") {
				t.Errorf("error: %v (want a refusal, not an empty search)", err)
			}
			if f.count("/tv/57243/external_ids") != 0 || f.count("/tv/2316/external_ids") != 0 {
				t.Errorf("a refused search must not fetch a show's ids: %v", f.hits)
			}
		})
	}
}
