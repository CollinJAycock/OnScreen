package tvdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testClient creates a Client whose HTTP calls are redirected to srv.
func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := New("test-api-key")
	c.httpClient = srv.Client()
	c.httpClient.Transport = &rewriteTransport{target: srv.URL}
	return c
}

type rewriteTransport struct{ target string }

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.target, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

// ── GetEpisode ──────────────────────────────────────────────────────────────

func TestGetEpisode_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/login") {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"token": "test-jwt-token"},
			})
			return
		}
		// Verify auth header.
		if r.Header.Get("Authorization") != "Bearer test-jwt-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"episodes": []map[string]any{{
					"id":           123,
					"seriesId":     81189,
					"name":         "Pilot",
					"aired":        "2008-01-20",
					"overview":     "Walter White begins cooking.",
					"seasonNumber": 1,
					"number":       1,
					"image":        "https://thetvdb.com/images/pilot.jpg",
				}},
			},
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	result, err := c.GetEpisode(context.Background(), 81189, 1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Title != "Pilot" {
		t.Errorf("Title: got %q, want %q", result.Title, "Pilot")
	}
	if result.EpisodeNum != 1 {
		t.Errorf("EpisodeNum: got %d, want 1", result.EpisodeNum)
	}
	if result.SeasonNum != 1 {
		t.Errorf("SeasonNum: got %d, want 1", result.SeasonNum)
	}
	if result.ThumbURL != "https://thetvdb.com/images/pilot.jpg" {
		t.Errorf("ThumbURL: got %q", result.ThumbURL)
	}
}

func TestGetEpisode_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"token": "tok"},
			})
			return
		}
		// Return episodes but none match the requested season/episode.
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"episodes": []map[string]any{{
					"seasonNumber": 1,
					"number":       1,
					"name":         "Pilot",
				}},
			},
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.GetEpisode(context.Background(), 81189, 2, 5)
	if err == nil {
		t.Fatal("expected error for episode not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found: %v", err)
	}
}

func TestGetEpisode_LoginFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.GetEpisode(context.Background(), 81189, 1, 1)
	if err == nil {
		t.Fatal("expected error for login failure")
	}
}

// ── Token caching ───────────────────────────────────────────────────────────

func TestTokenCaching(t *testing.T) {
	loginCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/login") {
			loginCount++
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"token": "cached-token"},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"episodes": []map[string]any{{
					"seasonNumber": 1,
					"number":       1,
					"name":         "Ep1",
				}},
			},
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)

	// Two calls should only trigger one login.
	c.GetEpisode(context.Background(), 100, 1, 1)
	c.GetEpisode(context.Background(), 100, 1, 1)

	if loginCount != 1 {
		t.Errorf("expected 1 login call (token cached), got %d", loginCount)
	}
}

func TestTokenClearedOn401(t *testing.T) {
	loginCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/login") {
			loginCount++
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"token": "token-" + strings.Repeat("x", loginCount)},
			})
			return
		}
		// First GET returns 401 to force token refresh.
		if loginCount == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"episodes": []map[string]any{{
					"seasonNumber": 1,
					"number":       1,
					"name":         "Ep1",
				}},
			},
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)

	// First call: login → 401 → token cleared.
	_, err := c.GetEpisode(context.Background(), 100, 1, 1)
	if err == nil {
		t.Fatal("expected error on 401")
	}

	// Second call should trigger a new login (token was cleared).
	_, _ = c.GetEpisode(context.Background(), 100, 1, 1)

	if loginCount != 2 {
		t.Errorf("expected 2 login calls (token cleared on 401), got %d", loginCount)
	}
}

// ── SearchSeries ────────────────────────────────────────────────────────────

func TestSearchSeries_MergesSearchHitAndExtendedArtwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/login"):
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"token": "tok"},
			})
		case strings.Contains(r.URL.Path, "/search"):
			// TVDB returns the id as a string here.
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"tvdb_id":   "81189",
					"name":      "Breaking Bad",
					"overview":  "A chemistry teacher cooks.",
					"image_url": "https://thetvdb.com/search-poster.jpg",
					"year":      "2008",
				}},
			})
		case strings.HasSuffix(r.URL.Path, "/extended"):
			// Type 2 = poster, type 3 = fanart; prefer higher score.
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"image": "https://thetvdb.com/fallback.jpg",
					"artworks": []map[string]any{
						{"image": "https://thetvdb.com/poster-low.jpg", "type": 2, "score": 100},
						{"image": "https://thetvdb.com/poster-hi.jpg", "type": 2, "score": 500},
						{"image": "https://thetvdb.com/fanart.jpg", "type": 3, "score": 10},
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := testClient(t, srv)
	res, err := c.SearchSeries(context.Background(), "Breaking Bad", 2008)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TVDBID != 81189 {
		t.Errorf("TVDBID: got %d, want 81189", res.TVDBID)
	}
	if res.Title != "Breaking Bad" {
		t.Errorf("Title: got %q", res.Title)
	}
	if res.FirstAirYear != 2008 {
		t.Errorf("FirstAirYear: got %d", res.FirstAirYear)
	}
	if res.PosterURL != "https://thetvdb.com/poster-hi.jpg" {
		t.Errorf("PosterURL: got %q, want the highest-scored poster", res.PosterURL)
	}
	if res.FanartURL != "https://thetvdb.com/fanart.jpg" {
		t.Errorf("FanartURL: got %q", res.FanartURL)
	}
}

func TestSearchSeries_FallsBackToSearchImageWhenExtendedFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"token": "tok"},
			})
		case strings.Contains(r.URL.Path, "/search"):
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"tvdb_id":   "999",
					"name":      "Obscure Show",
					"image_url": "https://thetvdb.com/obscure.jpg",
				}},
			})
		case strings.HasSuffix(r.URL.Path, "/extended"):
			// Extended endpoint broken — client should still return a result
			// using the search hit's image_url.
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := testClient(t, srv)
	res, err := c.SearchSeries(context.Background(), "Obscure Show", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.PosterURL != "https://thetvdb.com/obscure.jpg" {
		t.Errorf("PosterURL: got %q, want search image fallback", res.PosterURL)
	}
	if res.FanartURL != "" {
		t.Errorf("FanartURL: got %q, want empty when extended fails", res.FanartURL)
	}
}

func TestSearchSeries_NoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"token": "tok"},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.SearchSeries(context.Background(), "Nothing Matches", 0)
	if err == nil {
		t.Fatal("expected error when search returns no results")
	}
}

func TestGetEpisode_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"token": "tok"},
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GetEpisode(ctx, 100, 1, 1)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
