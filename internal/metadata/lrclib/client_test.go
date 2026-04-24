package lrclib

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGet_ExactMatch is the happy path — lrclib returns a full
// object, client extracts synced + plain lyrics.
func TestGet_ExactMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/get") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("track_name") != "Paranoid" {
			t.Errorf("missing track_name param")
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("expected User-Agent header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": 1,
			"trackName":"Paranoid",
			"artistName":"Black Sabbath",
			"albumName":"Paranoid",
			"duration": 169,
			"plainLyrics":"Finished with my woman cause she couldn't help me with my mind\nPeople think I'm insane...",
			"syncedLyrics":"[00:10.00]Finished with my woman cause she couldn't help me with my mind\n[00:15.50]People think I'm insane..."
		}`))
	}))
	defer srv.Close()

	testBaseURL = srv.URL
	defer func() { testBaseURL = "" }()

	c := NewWithClient(srv.Client())
	r, err := c.Get(context.Background(), Query{Track: "Paranoid", Artist: "Black Sabbath", Album: "Paranoid", Duration: 169})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if r == nil {
		t.Fatal("expected result, got nil")
	}
	if !strings.Contains(r.PlainLyrics, "Finished with my woman") {
		t.Errorf("plain lyrics missing expected text: %q", r.PlainLyrics)
	}
	if !strings.HasPrefix(r.SyncedLyrics, "[00:10.00]") {
		t.Errorf("synced lyrics missing timestamps: %q", r.SyncedLyrics)
	}
	if r.DurationSec != 169 {
		t.Errorf("duration = %v, want 169", r.DurationSec)
	}
}

// TestGet_NotFound turns a 404 into (nil, nil) so callers can fall
// through to /search. lrclib uses 404 semantically for "we don't
// have this track," not as an error.
func TestGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	testBaseURL = srv.URL
	defer func() { testBaseURL = "" }()

	c := NewWithClient(srv.Client())
	r, err := c.Get(context.Background(), Query{Track: "Nonexistent Song 123", Artist: "Unknown"})
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}
	if r != nil {
		t.Errorf("expected nil result on 404, got %+v", r)
	}
}

// TestGet_MissingRequired rejects the request without an HTTP call —
// Track + Artist are the minimum for the /get endpoint to return
// anything useful.
func TestGet_MissingRequired(t *testing.T) {
	c := New()
	if _, err := c.Get(context.Background(), Query{Track: "", Artist: ""}); err == nil {
		t.Error("expected error for empty required fields")
	}
	if _, err := c.Get(context.Background(), Query{Track: "Song"}); err == nil {
		t.Error("expected error when Artist missing")
	}
}

// TestSearch_FuzzyMatch picks the first entry from the returned
// array — lrclib ranks by match confidence.
func TestSearch_FuzzyMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":1,"trackName":"Paranoid (Live)","artistName":"Black Sabbath","plainLyrics":"Live version..."},
			{"id":2,"trackName":"Paranoid","artistName":"Black Sabbath","plainLyrics":"Studio version..."}
		]`))
	}))
	defer srv.Close()

	testBaseURL = srv.URL
	defer func() { testBaseURL = "" }()

	c := NewWithClient(srv.Client())
	r, err := c.Search(context.Background(), Query{Track: "Paranoid", Artist: "Sabbath"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if r == nil || !strings.Contains(r.PlainLyrics, "Live version") {
		t.Errorf("expected first (Live) entry, got %+v", r)
	}
}

// TestSearch_Empty returns (nil, nil) when the array is empty — not
// a 404 but still a miss.
func TestSearch_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	testBaseURL = srv.URL
	defer func() { testBaseURL = "" }()

	c := NewWithClient(srv.Client())
	r, err := c.Search(context.Background(), Query{Track: "Nothing", Artist: "Nobody"})
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}
	if r != nil {
		t.Errorf("expected nil for empty array, got %+v", r)
	}
}

// TestResolve_GetFirstSearchFallback exercises the chaining logic:
// /get 404s → client falls through to /search and returns its first
// result. This is the convenience path callers use.
func TestResolve_GetFirstSearchFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/get") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.Contains(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(`[{"id":1,"trackName":"Fallback","artistName":"X","plainLyrics":"fallback-body"}]`))
			return
		}
	}))
	defer srv.Close()

	testBaseURL = srv.URL
	defer func() { testBaseURL = "" }()

	c := NewWithClient(srv.Client())
	r, err := c.Resolve(context.Background(), Query{Track: "Fallback", Artist: "X"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r == nil || r.PlainLyrics != "fallback-body" {
		t.Errorf("expected fallback body, got %+v", r)
	}
}

// TestInstrumentalFlag surfaces the instrumental marker so clients
// can render "instrumental" instead of a blank lyrics panel.
func TestInstrumentalFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":1,"trackName":"T","artistName":"A","instrumental":true,"plainLyrics":""}`))
	}))
	defer srv.Close()

	testBaseURL = srv.URL
	defer func() { testBaseURL = "" }()

	c := NewWithClient(srv.Client())
	r, _ := c.Get(context.Background(), Query{Track: "T", Artist: "A"})
	if r == nil || !r.Instrumental {
		t.Errorf("instrumental flag lost: %+v", r)
	}
}
