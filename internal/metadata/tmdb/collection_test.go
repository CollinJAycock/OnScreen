package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRefreshMovie_ParsesBelongsToCollection(t *testing.T) {
	srv, _ := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 348, "title": "Alien", "release_date": "1979-05-25",
			"belongs_to_collection": map[string]any{
				"id": 8091, "name": "Alien Collection",
				"poster_path": "/alienposter.jpg", "backdrop_path": "/alienbd.jpg",
			},
		})
	})
	defer srv.Close()

	res, err := testClient(t, srv).RefreshMovie(context.Background(), 348)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !res.CollectionChecked {
		t.Fatal("details result must be marked CollectionChecked")
	}
	if res.Collection == nil || res.Collection.TMDBID != 8091 || res.Collection.Name != "Alien Collection" {
		t.Fatalf("collection: got %+v", res.Collection)
	}
	if res.Collection.PosterURL != imageBaseURL+"/alienposter.jpg" || res.Collection.BackdropURL != imageBaseURL+"/alienbd.jpg" {
		t.Fatalf("collection art: got %+v", res.Collection)
	}
}

func TestRefreshMovie_NullCollectionIsDefinitiveNone(t *testing.T) {
	srv, _ := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id": 550, "title": "Fight Club", "belongs_to_collection": null}`))
	})
	defer srv.Close()

	res, err := testClient(t, srv).RefreshMovie(context.Background(), 550)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Collection != nil || !res.CollectionChecked {
		t.Fatalf("want checked + nil collection, got checked=%v collection=%+v", res.CollectionChecked, res.Collection)
	}
}

// SearchMovie upgrades its top hit to a details response: same two requests
// as before (search + one follow-up) but the follow-up now answers runtime,
// genres, certification AND belongs_to_collection — no standalone
// release_dates lookup.
func TestSearchMovie_UpgradesTopHitToDetails(t *testing.T) {
	srv, counts := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/search/movie"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
				"id": 348, "title": "Alien", "release_date": "1979-05-25",
			}}})
		case strings.HasSuffix(r.URL.Path, "/movie/348"):
			if r.URL.Query().Get("append_to_response") != "release_dates" {
				t.Errorf("details call must append release_dates, got %q", r.URL.Query().Get("append_to_response"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 348, "title": "Alien", "release_date": "1979-05-25", "runtime": 117,
				"genres":                []map[string]any{{"id": 27, "name": "Horror"}},
				"release_dates":         map[string]any{"results": []map[string]any{{"iso_3166_1": "US", "release_dates": []map[string]any{{"certification": "R"}}}}},
				"belongs_to_collection": map[string]any{"id": 8091, "name": "Alien Collection"},
			})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	defer srv.Close()

	res, err := testClient(t, srv).SearchMovie(context.Background(), "Alien", 1979)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.ContentRating != "R" || res.DurationMS != 117*60*1000 || len(res.Genres) != 1 {
		t.Fatalf("details fields not used: %+v", res)
	}
	if !res.CollectionChecked || res.Collection == nil || res.Collection.TMDBID != 8091 {
		t.Fatalf("collection: checked=%v %+v", res.CollectionChecked, res.Collection)
	}
	if (*counts)["/search/movie"] != 1 || (*counts)["/movie/348"] != 1 || (*counts)["/movie/348/release_dates"] != 0 {
		t.Fatalf("requests: %v (want one search + one details, no standalone certification call)", *counts)
	}
}

func TestSearchMovie_DetailsFailureFallsBackToSearchRow(t *testing.T) {
	srv, counts := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/search/movie"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
				"id": 348, "title": "Alien", "release_date": "1979-05-25",
			}}})
		case strings.HasSuffix(r.URL.Path, "/movie/348/release_dates"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"iso_3166_1": "US", "release_dates": []map[string]any{{"certification": "R"}}}}})
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	defer srv.Close()

	res, err := testClient(t, srv).SearchMovie(context.Background(), "Alien", 1979)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.TMDBID != 348 || res.ContentRating != "R" {
		t.Fatalf("fallback result: %+v", res)
	}
	if res.CollectionChecked {
		t.Fatal("a search-derived result must not claim the collection was checked")
	}
	if (*counts)["/movie/348/release_dates"] != 1 {
		t.Fatalf("fallback must fetch the certification: %v", *counts)
	}
}

func TestGetCollection_ParsesAndOrdersParts(t *testing.T) {
	srv, _ := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/collection/8091") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 8091, "name": "Alien Collection", "overview": "Xenomorphs.",
			"poster_path": "/p.jpg", "backdrop_path": "/b.jpg",
			"parts": []map[string]any{
				{"id": 126889, "title": "Alien: Covenant", "release_date": "2017-05-09", "poster_path": "/cov.jpg", "overview": "Covenant."},
				{"id": 999999, "title": "Alien: Untitled", "release_date": ""},
				{"id": 348, "title": "Alien", "release_date": "1979-05-25", "media_type": "movie"},
				{"id": 348, "title": "Alien (dupe)", "release_date": "1979-05-25"},
				{"id": 679, "title": "Aliens", "release_date": "1986-07-18"},
				{"id": 1, "title": "Not a movie", "media_type": "tv", "release_date": "1990-01-01"},
			},
		})
	})
	defer srv.Close()

	col, err := testClient(t, srv).GetCollection(context.Background(), 8091)
	if err != nil {
		t.Fatalf("get collection: %v", err)
	}
	if col.TMDBID != 8091 || col.Name != "Alien Collection" || col.Overview != "Xenomorphs." ||
		col.PosterURL != imageBaseURL+"/p.jpg" || col.BackdropURL != imageBaseURL+"/b.jpg" {
		t.Fatalf("collection header: %+v", col)
	}
	var ids []int
	for _, p := range col.Parts {
		ids = append(ids, p.TMDBID)
	}
	want := []int{348, 679, 126889, 999999}
	if len(ids) != len(want) {
		t.Fatalf("parts: got %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("parts order: got %v, want %v (release order, undated last, dupes/non-movies dropped)", ids, want)
		}
	}
	if col.Parts[0].Year != 1979 || col.Parts[0].ReleaseDate != "1979-05-25" {
		t.Errorf("part date: %+v", col.Parts[0])
	}
	if col.Parts[2].PosterURL != imageBaseURL+"/cov.jpg" || col.Parts[2].Overview != "Covenant." {
		t.Errorf("part art/overview: %+v", col.Parts[2])
	}
	if col.Parts[3].Year != 0 || col.Parts[3].ReleaseDate != "" {
		t.Errorf("undated part: %+v", col.Parts[3])
	}
}

func TestGetCollection_NotFound(t *testing.T) {
	srv, _ := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()
	_, err := testClient(t, srv).GetCollection(context.Background(), 1)
	if !IsNotFound(err) {
		t.Fatalf("want IsNotFound, got %v", err)
	}
	if IsNotFound(ErrCircuitOpen) {
		t.Fatal("circuit-open must not read as not-found")
	}
}

// ageCacheEntry rewinds a cache entry's fetched_at so TTL expiry can be
// tested without sleeping.
func ageCacheEntry(t *testing.T, c *Client, path string, params url.Values, age time.Duration) {
	t.Helper()
	key := cacheKey(path + "?" + params.Encode())
	file := c.cache.file(key)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read cache entry %s: %v", path, err)
	}
	var env cacheEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode cache entry: %v", err)
	}
	env.FetchedAt = time.Now().Add(-age)
	raw, _ = json.Marshal(env)
	if err := os.WriteFile(file, raw, 0o644); err != nil {
		t.Fatalf("write cache entry: %v", err)
	}
}

// Collections use a shorter positive TTL than title metadata: a four-day-old
// collection entry is refetched (new sequels land in parts), while a
// four-day-old movie-details entry is still served from disk.
func TestGetCollection_ShorterCacheTTL(t *testing.T) {
	srv, counts := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/collection/") {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 8091, "name": "Alien Collection"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 348, "title": "Alien"})
	})
	defer srv.Close()

	c := testClient(t, srv).WithCacheDir(t.TempDir())
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := c.GetCollection(ctx, 8091); err != nil {
			t.Fatal(err)
		}
		if _, err := c.MovieCollection(ctx, 348); err != nil {
			t.Fatal(err)
		}
	}
	if (*counts)["/collection/8091"] != 1 || (*counts)["/movie/348"] != 1 {
		t.Fatalf("fresh entries must be served from cache: %v", *counts)
	}

	lang := url.Values{}
	lang.Set("language", "en-US")
	ageCacheEntry(t, c, "/collection/8091", lang, 4*24*time.Hour)
	details := url.Values{}
	details.Set("language", "en-US")
	details.Set("append_to_response", "release_dates")
	ageCacheEntry(t, c, "/movie/348", details, 4*24*time.Hour)

	if _, err := c.GetCollection(ctx, 8091); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MovieCollection(ctx, 348); err != nil {
		t.Fatal(err)
	}
	if (*counts)["/collection/8091"] != 2 {
		t.Errorf("4-day-old collection entry must be refetched (3-day TTL): %v", *counts)
	}
	if (*counts)["/movie/348"] != 1 {
		t.Errorf("4-day-old movie entry must still be cached (7-day TTL): %v", *counts)
	}
}

// MovieCollection issues RefreshMovie's exact request, so the two share one
// cache entry.
func TestMovieCollection_SharesCacheWithRefreshMovie(t *testing.T) {
	srv, counts := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 679, "title": "Aliens",
			"belongs_to_collection": map[string]any{"id": 8091, "name": "Alien Collection"},
		})
	})
	defer srv.Close()

	c := testClient(t, srv).WithCacheDir(t.TempDir())
	if _, err := c.RefreshMovie(context.Background(), 679); err != nil {
		t.Fatal(err)
	}
	ref, err := c.MovieCollection(context.Background(), 679)
	if err != nil {
		t.Fatal(err)
	}
	if ref == nil || ref.TMDBID != 8091 {
		t.Fatalf("ref: %+v", ref)
	}
	if (*counts)["/movie/679"] != 1 {
		t.Fatalf("MovieCollection must reuse RefreshMovie's cache entry: %v", *counts)
	}
}
