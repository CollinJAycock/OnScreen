package arr

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMovieByID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie/7":
			_, _ = io.WriteString(w, `{"id":7,"title":"Heat","tmdbId":949,"monitored":true,"hasFile":false,"status":"released","sizeOnDisk":"n/a"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(srv, "k")

	m, err := c.MovieByID(context.Background(), 7)
	if err != nil {
		t.Fatalf("MovieByID: %v", err)
	}
	if m.ID != 7 || m.TMDBID != 949 || !m.Monitored || m.HasFile {
		t.Errorf("movie = %+v", m)
	}
	if _, err := c.MovieByID(context.Background(), 8); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing movie: err = %v, want ErrNotFound", err)
	}
}

func TestMovieByTMDB_RechecksID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/movie" {
			t.Errorf("path = %q", r.URL.Path)
		}
		switch r.URL.Query().Get("tmdbId") {
		case "949":
			// A build that ignored the filter returns the whole library.
			_, _ = io.WriteString(w, `[{"id":1,"tmdbId":603},{"id":7,"tmdbId":949,"monitored":true}]`)
		default:
			_, _ = io.WriteString(w, `[{"id":1,"tmdbId":603}]`)
		}
	}))
	defer srv.Close()
	c := newTestClient(srv, "k")

	m, err := c.MovieByTMDB(context.Background(), 949)
	if err != nil || m.ID != 7 {
		t.Fatalf("MovieByTMDB = %+v, %v; want id 7", m, err)
	}
	if _, err := c.MovieByTMDB(context.Background(), 1234); !errors.Is(err, ErrNotFound) {
		t.Errorf("unmanaged: err = %v, want ErrNotFound", err)
	}
}

func TestSeriesByIDAndTVDB(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series/3":
			_, _ = io.WriteString(w, `{"id":3,"title":"Severance","tvdbId":371980,"monitored":true,
				"statistics":{"episodeFileCount":2,"episodeCount":9,"totalEpisodeCount":19,"sizeOnDisk":1.5e9,"percentOfEpisodes":22.2}}`)
		case r.URL.Path == "/api/v3/series" && r.URL.Query().Get("tvdbId") == "371980":
			_, _ = io.WriteString(w, `[{"id":3,"tvdbId":371980,"title":"Severance"}]`)
		case r.URL.Path == "/api/v3/series":
			_, _ = io.WriteString(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(srv, "k")

	s, err := c.SeriesByID(context.Background(), 3)
	if err != nil {
		t.Fatalf("SeriesByID: %v", err)
	}
	if s.Statistics == nil || s.Statistics.EpisodeFileCount != 2 || s.Statistics.EpisodeCount != 9 {
		t.Errorf("series = %+v stats=%+v", s, s.Statistics)
	}
	if _, err := c.SeriesByID(context.Background(), 4); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing series: err = %v", err)
	}
	got, err := c.SeriesByTVDB(context.Background(), 371980)
	if err != nil || got.ID != 3 {
		t.Errorf("SeriesByTVDB = %+v, %v", got, err)
	}
	if _, err := c.SeriesByTVDB(context.Background(), 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("unmanaged: err = %v", err)
	}
}

func TestSeriesByID_MissingStatistics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":3,"monitored":false}`)
	}))
	defer srv.Close()
	s, err := newTestClient(srv, "k").SeriesByID(context.Background(), 3)
	if err != nil {
		t.Fatalf("SeriesByID: %v", err)
	}
	if s.Statistics != nil || s.Monitored {
		t.Errorf("series = %+v", s)
	}
}
