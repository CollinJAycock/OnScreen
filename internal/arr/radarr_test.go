package arr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLookupMovieByTMDB_PrefersExactIDMatch(t *testing.T) {
	// Radarr returns multiple search results when the term resolves to
	// several near-matches (e.g. tmdb:603 returns Matrix + Matrix
	// Reloaded). The lookup must pick the row whose tmdbId actually
	// equals the requested id, not "the first result."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("term"); got != "tmdb:603" {
			t.Errorf("term = %q, want \"tmdb:603\"", got)
		}
		_, _ = io.WriteString(w, `[
			{"title":"Reloaded","tmdbId":604,"year":2003,"titleSlug":"matrix-reloaded"},
			{"title":"The Matrix","tmdbId":603,"year":1999,"titleSlug":"the-matrix"},
			{"title":"Revolutions","tmdbId":605,"year":2003,"titleSlug":"matrix-revolutions"}
		]`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").LookupMovieByTMDB(context.Background(), 603)
	if err != nil {
		t.Fatalf("LookupMovieByTMDB: %v", err)
	}
	if got.TMDBID != 603 || got.Title != "The Matrix" {
		t.Errorf("got %+v, want exact tmdb:603 match", got)
	}
}

func TestLookupMovieByTMDB_FallsBackToFirstResult(t *testing.T) {
	// If no result has the requested id (Radarr's fuzzy search may not
	// return the exact id at all on a misspelling), use the first row
	// rather than 404. Lets the admin see *something* and pick.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[
			{"title":"Closest Match","tmdbId":999,"year":2020,"titleSlug":"closest"}
		]`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").LookupMovieByTMDB(context.Background(), 603)
	if err != nil {
		t.Fatalf("expected first-result fallback, got %v", err)
	}
	if got.TMDBID != 999 {
		t.Errorf("got %+v, want first result", got)
	}
}

func TestLookupMovieByTMDB_EmptyResultIsErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv, "k").LookupMovieByTMDB(context.Background(), 1)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestAddMovie_PostsBodyAndDecodesResponse(t *testing.T) {
	var got AddMovieRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v3/movie" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = io.WriteString(w, `{"id":42,"title":"The Matrix"}`)
	}))
	defer srv.Close()

	req := AddMovieRequest{
		Title: "The Matrix", TMDBID: 603, Year: 1999, TitleSlug: "the-matrix",
		QualityProfileID: 1, RootFolderPath: "/movies", Monitored: true,
		MinimumAvailability: "released", Tags: []int32{7},
		AddOptions: AddMovieOptions{SearchForMovie: true},
	}
	resp, err := newTestClient(srv, "k").AddMovie(context.Background(), req)
	if err != nil {
		t.Fatalf("AddMovie: %v", err)
	}
	if resp.ID != 42 || resp.Title != "The Matrix" {
		t.Errorf("response = %+v", resp)
	}
	if got.TMDBID != 603 || !got.AddOptions.SearchForMovie {
		t.Errorf("body posted to upstream = %+v, want full request echoed", got)
	}
}

func TestAddMovie_ConflictBubblesErrConflict(t *testing.T) {
	// Caller treats ErrConflict as "already added" (no-op success).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	_, err := newTestClient(srv, "k").AddMovie(context.Background(), AddMovieRequest{TMDBID: 603})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("got %v, want ErrConflict", err)
	}
}
