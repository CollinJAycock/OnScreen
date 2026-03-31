package audiodb

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
	c := New()
	c.http = srv.Client()
	c.http.Transport = &rewriteTransport{target: srv.URL}
	return c
}

type rewriteTransport struct{ target string }

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.target, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

// ── SearchArtist ────────────────────────────────────────────────────────────

func TestSearchArtist_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"artists": []map[string]any{{
				"strArtist":      "Pink Floyd",
				"strArtistThumb": "https://theaudiodb.com/images/pinkfloyd.jpg",
				"strArtistFanart": "https://theaudiodb.com/images/pinkfloyd_fanart.jpg",
				"strBiographyEN": "English rock band formed in London in 1965.",
			}},
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	result, err := c.SearchArtist(context.Background(), "Pink Floyd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "Pink Floyd" {
		t.Errorf("Name: got %q, want %q", result.Name, "Pink Floyd")
	}
	if result.ThumbURL == "" {
		t.Error("ThumbURL should not be empty")
	}
	if result.FanartURL == "" {
		t.Error("FanartURL should not be empty")
	}
	if !strings.Contains(result.Biography, "English rock band") {
		t.Errorf("Biography: got %q", result.Biography)
	}
}

func TestSearchArtist_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"artists": nil})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	result, err := c.SearchArtist(context.Background(), "ZZZZZ Nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for unknown artist, got %+v", result)
	}
}

func TestSearchArtist_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.SearchArtist(context.Background(), "Test")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestSearchArtist_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"artists": nil})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.SearchArtist(ctx, "Test")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ── SearchAlbum ─────────────────────────────────────────────────────────────

func TestSearchAlbum_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"album": []map[string]any{{
				"strAlbum":          "The Dark Side of the Moon",
				"strAlbumThumb":     "https://theaudiodb.com/images/dsotm.jpg",
				"strDescriptionEN":  "Iconic concept album.",
				"intYearReleased":   "1973",
				"strGenre":          "Progressive Rock",
			}},
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	result, err := c.SearchAlbum(context.Background(), "Pink Floyd", "The Dark Side of the Moon")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "The Dark Side of the Moon" {
		t.Errorf("Name: got %q", result.Name)
	}
	if result.Year != 1973 {
		t.Errorf("Year: got %d, want 1973", result.Year)
	}
	if len(result.Genres) != 1 || result.Genres[0] != "Progressive Rock" {
		t.Errorf("Genres: got %v", result.Genres)
	}
	if result.ThumbURL == "" {
		t.Error("ThumbURL should not be empty")
	}
}

func TestSearchAlbum_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"album": nil})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	result, err := c.SearchAlbum(context.Background(), "Unknown", "Unknown Album")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
}

func TestSearchAlbum_NoGenre(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"album": []map[string]any{{
				"strAlbum":         "Untitled",
				"strAlbumThumb":    "",
				"strDescriptionEN": "",
				"intYearReleased":  "0",
				"strGenre":         "",
			}},
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	result, err := c.SearchAlbum(context.Background(), "Artist", "Untitled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Genres) != 0 {
		t.Errorf("expected no genres for empty genre string, got %v", result.Genres)
	}
}
