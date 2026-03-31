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
