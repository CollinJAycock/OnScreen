package arr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestRadarrCalendar_QueryAndDecode(t *testing.T) {
	// The request must carry the window as UTC RFC3339 plus
	// unmonitored=false, and every release date must decode — Radarr sends
	// full timestamps, and an absent date arrives as null.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/calendar" {
			t.Errorf("path = %q, want /api/v3/calendar", r.URL.Path)
		}
		if got := r.Header.Get("X-Api-Key"); got != "k" {
			t.Errorf("X-Api-Key = %q", got)
		}
		q := r.URL.Query()
		if q.Get("start") != "2026-09-26T00:00:00Z" || q.Get("end") != "2026-10-27T00:00:00Z" {
			t.Errorf("window = %q..%q", q.Get("start"), q.Get("end"))
		}
		if q.Get("unmonitored") != "false" {
			t.Errorf("unmonitored = %q, want false", q.Get("unmonitored"))
		}
		_, _ = io.WriteString(w, `[{
			"id": 12, "title": "Brand New Day", "year": 2026, "tmdbId": 969681,
			"overview": "Peter is back.",
			"inCinemas": "2026-07-31T00:00:00Z",
			"digitalRelease": "2026-09-28T00:00:00Z",
			"physicalRelease": null,
			"hasFile": false, "monitored": true, "certification": "PG-13",
			"images": [{"coverType": "poster", "url": "/MediaCover/12/poster.jpg", "remoteUrl": "https://image.tmdb.org/p.jpg"}],
			"path": "/movies/Brand New Day (2026)"
		}]`)
	}))
	defer srv.Close()

	start := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	// A non-UTC end must still be sent as UTC.
	end := time.Date(2026, 10, 27, 2, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	got, err := newTestClient(srv, "k").RadarrCalendar(context.Background(), start, end)
	if err != nil {
		t.Fatalf("RadarrCalendar: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d movies, want 1", len(got))
	}
	m := got[0]
	if m.ID != 12 || m.TMDBID != 969681 || m.Year != 2026 || m.Certification != "PG-13" || m.HasFile || !m.Monitored {
		t.Errorf("movie = %+v", m)
	}
	if want := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC); !m.InCinemas.Equal(want) {
		t.Errorf("inCinemas = %v, want %v", m.InCinemas, want)
	}
	if want := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC); !m.DigitalRelease.Equal(want) {
		t.Errorf("digitalRelease = %v, want %v", m.DigitalRelease, want)
	}
	if !m.PhysicalRelease.IsZero() {
		t.Errorf("physicalRelease = %v, want zero for null", m.PhysicalRelease)
	}
	if len(m.Images) != 1 || m.Images[0].RemoteURL != "https://image.tmdb.org/p.jpg" {
		t.Errorf("images = %+v", m.Images)
	}
	if m.Path != "/movies/Brand New Day (2026)" {
		t.Errorf("path = %q", m.Path)
	}
}

func TestRadarrCalendar_UnauthorizedIsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := newTestClient(srv, "bad").RadarrCalendar(context.Background(), time.Now(), time.Now())
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("got %v, want ErrUnauthorized", err)
	}
}
