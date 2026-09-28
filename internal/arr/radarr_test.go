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
	// Caller treats ErrConflict as "already added" and switches to the
	// existing movie. Radarr's real answer is a 400 MovieExistsValidator;
	// some builds / proxies answer 409.
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"409", http.StatusConflict, ""},
		{"400 MovieExistsValidator", http.StatusBadRequest, `[{"propertyName":"TmdbId",
			"errorMessage":"This movie has already been added","attemptedValue":603,"severity":"error",
			"errorCode":"MovieExistsValidator","formattedMessagePlaceholderValues":{"propertyName":"Tmdb Id","propertyValue":603}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			_, err := newTestClient(srv, "k").AddMovie(context.Background(), AddMovieRequest{TMDBID: 603})
			if !errors.Is(err, ErrConflict) {
				t.Errorf("got %v, want ErrConflict", err)
			}
		})
	}
}

// monitorMovieRadarr serves one movie (id 7) for MonitorMovie, recording the
// PUT body and the commands sent.
type monitorMovieRadarr struct {
	movie      string
	put        map[string]any
	commands   []map[string]any
	commandErr bool
}

func (m *monitorMovieRadarr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/7":
		_, _ = io.WriteString(w, m.movie)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v3/movie/7":
		_ = json.Unmarshal(raw, &m.put)
		_, _ = w.Write(raw)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
		if m.commandErr {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		m.commands = append(m.commands, body)
		_, _ = io.WriteString(w, `{"id":31}`)
	default:
		http.NotFound(w, r)
	}
}

func TestMonitorMovie(t *testing.T) {
	search := []map[string]any{{"name": "MoviesSearch", "movieIds": []any{float64(7)}}}
	cases := []struct {
		name          string
		movie         string
		wantPut       bool
		wantSearch    bool
		wantMonitored bool
	}{
		{"unmonitored, missing", `{"id":7,"title":"Heat","monitored":false,"hasFile":false,
			"path":"/movies/Heat","qualityProfileId":4,"tags":[2],"addOptions":{"x":1}}`, true, true, true},
		{"monitored, missing (re-request after a failure)", `{"id":7,"title":"Heat","monitored":true,"hasFile":false}`, false, true, false},
		{"unmonitored, file on disk", `{"id":7,"title":"Heat","monitored":false,"hasFile":true}`, true, false, true},
		{"monitored, file on disk", `{"id":7,"title":"Heat","monitored":true,"hasFile":true}`, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &monitorMovieRadarr{movie: tc.movie}
			srv := httptest.NewServer(fake)
			defer srv.Close()
			res, err := newTestClient(srv, "k").MonitorMovie(context.Background(), 7)
			if err != nil {
				t.Fatalf("MonitorMovie: %v", err)
			}
			if res.MovieID != 7 || res.Title != "Heat" || res.Monitored != tc.wantMonitored || res.Searched != tc.wantSearch {
				t.Errorf("result = %+v", res)
			}
			if (fake.put != nil) != tc.wantPut {
				t.Fatalf("PUT sent = %v, want %v", fake.put != nil, tc.wantPut)
			}
			if tc.wantPut {
				// The movie goes back whole, unmodelled fields included.
				if fake.put["monitored"] != true || fake.put["title"] != "Heat" {
					t.Errorf("PUT = %v", fake.put)
				}
				if tc.name == "unmonitored, missing" && (fake.put["path"] != "/movies/Heat" ||
					fake.put["qualityProfileId"] != float64(4) || fake.put["addOptions"] == nil) {
					t.Errorf("PUT lost fields: %v", fake.put)
				}
			}
			var want []map[string]any
			if tc.wantSearch {
				want = search
			}
			if len(fake.commands) != len(want) || (len(want) > 0 && !reflectEqual(fake.commands, want)) {
				t.Errorf("commands = %v, want %v", fake.commands, want)
			}
		})
	}
}

func TestMonitorMovie_SearchFailureKeepsMonitoring(t *testing.T) {
	fake := &monitorMovieRadarr{movie: `{"id":7,"title":"Heat","monitored":false,"hasFile":false}`, commandErr: true}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	res, err := newTestClient(srv, "k").MonitorMovie(context.Background(), 7)
	if err != nil {
		t.Fatalf("MonitorMovie: %v", err)
	}
	if !res.Monitored || res.Searched || res.SearchErr == nil || fake.put == nil {
		t.Errorf("result = %+v put = %v, want monitored with the search error reported", res, fake.put)
	}
}

func TestMonitorMovie_Errors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		movie string
		want  error
	}{
		{"wrong movie returned", `{"id":8,"title":"Other"}`, ErrNotFound},
		{"no id", `{}`, ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &monitorMovieRadarr{movie: tc.movie}
			srv := httptest.NewServer(fake)
			defer srv.Close()
			if _, err := newTestClient(srv, "k").MonitorMovie(context.Background(), 7); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if fake.put != nil || len(fake.commands) != 0 {
				t.Error("nothing may be written for a movie that wasn't found")
			}
		})
	}
	srv := httptest.NewServer(&monitorMovieRadarr{movie: `not json`})
	defer srv.Close()
	if _, err := newTestClient(srv, "k").MonitorMovie(context.Background(), 7); err == nil {
		t.Error("undecodable movie: want an error")
	}
	if _, err := newTestClient(srv, "k").MonitorMovie(context.Background(), 0); err == nil {
		t.Error("id 0: want an error")
	}
	if _, err := newTestClient(srv, "k").MonitorMovie(context.Background(), 9); !errors.Is(err, ErrNotFound) {
		t.Errorf("movie missing from Radarr: err = %v, want ErrNotFound", err)
	}
}

func reflectEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
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
