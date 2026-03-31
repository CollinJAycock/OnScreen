package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testClient creates a Client whose HTTP calls are redirected to the given
// httptest.Server (bypassing the hardcoded TMDB base URL).
func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := New("test-key", 1000, "en-US") // high rate limit for tests
	c.httpClient = srv.Client()
	// Rewrite transport to redirect all requests to the test server.
	c.httpClient.Transport = &rewriteTransport{target: srv.URL}
	return c
}

// rewriteTransport rewrites request URLs to point at the test server.
type rewriteTransport struct{ target string }

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.target, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

// ── SearchMovie ─────────────────────────────────────────────────────────────

func TestSearchMovie_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/search/movie") {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{{
				"id":            603,
				"title":         "The Matrix",
				"original_title": "The Matrix",
				"overview":      "A computer hacker learns about the true nature of reality.",
				"release_date":  "1999-03-31",
				"runtime":       136,
				"vote_average":  8.7,
				"poster_path":   "/poster.jpg",
				"backdrop_path": "/backdrop.jpg",
				"genres":        []map[string]any{{"id": 28, "name": "Action"}},
			}},
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	result, err := c.SearchMovie(context.Background(), "The Matrix", 1999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TMDBID != 603 {
		t.Errorf("TMDBID: got %d, want 603", result.TMDBID)
	}
	if result.Title != "The Matrix" {
		t.Errorf("Title: got %q, want %q", result.Title, "The Matrix")
	}
	if result.Year != 1999 {
		t.Errorf("Year: got %d, want 1999", result.Year)
	}
	if len(result.Genres) != 1 || result.Genres[0] != "Action" {
		t.Errorf("Genres: got %v, want [Action]", result.Genres)
	}
	if result.PosterURL == "" {
		t.Error("PosterURL should not be empty")
	}
}

func TestSearchMovie_NoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.SearchMovie(context.Background(), "Nonexistent Movie", 0)
	if err == nil {
		t.Fatal("expected error for no results")
	}
}

func TestSearchMovie_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.SearchMovie(context.Background(), "Test", 0)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestSearchMovie_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.SearchMovie(ctx, "Test", 0)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ── SearchTV ────────────────────────────────────────────────────────────────

func TestSearchTV_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/external_ids") {
			json.NewEncoder(w).Encode(map[string]any{"tvdb_id": 81189, "imdb_id": "tt0903747"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{{
				"id":             1396,
				"name":           "Breaking Bad",
				"original_name":  "Breaking Bad",
				"overview":       "A chemistry teacher turned meth producer.",
				"first_air_date": "2008-01-20",
				"vote_average":   9.5,
				"poster_path":    "/bb.jpg",
				"genres":         []map[string]any{{"id": 18, "name": "Drama"}},
			}},
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	result, err := c.SearchTV(context.Background(), "Breaking Bad", 2008)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TMDBID != 1396 {
		t.Errorf("TMDBID: got %d, want 1396", result.TMDBID)
	}
	if result.Title != "Breaking Bad" {
		t.Errorf("Title: got %q, want %q", result.Title, "Breaking Bad")
	}
	if result.TVDBID != 81189 {
		t.Errorf("TVDBID: got %d, want 81189", result.TVDBID)
	}
}

func TestSearchTV_NoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.SearchTV(context.Background(), "Nonexistent Show", 0)
	if err == nil {
		t.Fatal("expected error for no results")
	}
}

// ── GetSeason ───────────────────────────────────────────────────────────────

func TestGetSeason_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"season_number": 1,
			"name":          "Season 1",
			"overview":      "The first season.",
			"air_date":      "2008-01-20",
			"poster_path":   "/s1.jpg",
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	result, err := c.GetSeason(context.Background(), 1396, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Number != 1 {
		t.Errorf("Number: got %d, want 1", result.Number)
	}
	if result.Name != "Season 1" {
		t.Errorf("Name: got %q, want %q", result.Name, "Season 1")
	}
}

// ── GetEpisode ──────────────────────────────────────────────────────────────

func TestGetEpisode_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"episode_number": 1,
			"name":           "Pilot",
			"overview":       "Walter White begins his transformation.",
			"air_date":       "2008-01-20",
			"vote_average":   9.0,
			"still_path":     "/pilot.jpg",
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	result, err := c.GetEpisode(context.Background(), 1396, 1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Title != "Pilot" {
		t.Errorf("Title: got %q, want %q", result.Title, "Pilot")
	}
	if result.EpisodeNum != 1 {
		t.Errorf("EpisodeNum: got %d, want 1", result.EpisodeNum)
	}
}

func TestGetEpisode_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.GetEpisode(context.Background(), 1396, 1, 999)
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

// ── RefreshMovie ────────────────────────────────────────────────────────────

func TestRefreshMovie_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"id":            603,
			"title":         "The Matrix",
			"release_date":  "1999-03-31",
			"runtime":       136,
			"vote_average":  8.7,
			"poster_path":   "/poster.jpg",
			"backdrop_path": "/backdrop.jpg",
			"genres":        []map[string]any{},
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	result, err := c.RefreshMovie(context.Background(), 603)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TMDBID != 603 {
		t.Errorf("TMDBID: got %d, want 603", result.TMDBID)
	}
}

// ── GetTVExternalIDs ────────────────────────────────────────────────────────

func TestGetTVExternalIDs_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"tvdb_id": 81189,
			"imdb_id": "tt0903747",
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	tvdbID, imdbID, err := c.GetTVExternalIDs(context.Background(), 1396)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tvdbID != 81189 {
		t.Errorf("tvdbID: got %d, want 81189", tvdbID)
	}
	if imdbID != "tt0903747" {
		t.Errorf("imdbID: got %q, want %q", imdbID, "tt0903747")
	}
}

// ── pickBestTVMatch ─────────────────────────────────────────────────────────

func TestPickBestTVMatch_ExactMatch(t *testing.T) {
	results := []tmdbTV{
		{ID: 1, Name: "Good Eats: Reloaded"},
		{ID: 2, Name: "Good Eats"},
	}
	got := pickBestTVMatch(results, "Good Eats")
	if got.ID != 2 {
		t.Errorf("got ID %d, want 2 (exact match)", got.ID)
	}
}

func TestPickBestTVMatch_PrefixMatch(t *testing.T) {
	results := []tmdbTV{
		{ID: 1, Name: "The Office: An American Workplace"},
		{ID: 2, Name: "Office Hours"},
	}
	got := pickBestTVMatch(results, "The Office")
	if got.ID != 1 {
		t.Errorf("got ID %d, want 1 (prefix match)", got.ID)
	}
}

func TestPickBestTVMatch_FallbackToFirst(t *testing.T) {
	results := []tmdbTV{
		{ID: 1, Name: "Completely Different"},
		{ID: 2, Name: "Also Different"},
	}
	got := pickBestTVMatch(results, "Something Else")
	if got.ID != 1 {
		t.Errorf("got ID %d, want 1 (fallback to first)", got.ID)
	}
}

// ── normTitle ───────────────────────────────────────────────────────────────

func TestNormTitle(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Breaking Bad", "breaking bad"},
		{"The Office (US)", "the office us"},
		{"  HELLO  ", "hello"},
		{"CSI: Miami", "csi miami"},
	}
	for _, tt := range tests {
		got := normTitle(tt.input)
		if got != tt.want {
			t.Errorf("normTitle(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── imageURL ────────────────────────────────────────────────────────────────

func TestImageURL_Empty(t *testing.T) {
	if got := imageURL(""); got != "" {
		t.Errorf("imageURL(\"\") = %q, want empty", got)
	}
}

func TestImageURL_WithPath(t *testing.T) {
	got := imageURL("/abc.jpg")
	if got != imageBaseURL+"/abc.jpg" {
		t.Errorf("imageURL(/abc.jpg) = %q, want %q", got, imageBaseURL+"/abc.jpg")
	}
}
