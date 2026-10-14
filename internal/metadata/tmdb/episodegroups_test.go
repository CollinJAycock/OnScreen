package tmdb

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/onscreen/onscreen/internal/metadata"
)

// Recorded shapes of /tv/{id}/episode_groups and /tv/episode_group/{id}
// (trimmed to the fields the client reads).
const episodeGroupsBody = `{
  "id": 1437,
  "results": [
    {"id": "aaa", "name": "Original Air Date", "type": 1, "episode_count": 14},
    {"id": "dvd-small", "name": "DVD (partial)", "type": 3, "episode_count": 2},
    {"id": "dvd-full", "name": "DVD Order", "type": 3, "episode_count": 15},
    {"id": "abs", "name": "Absolute", "type": 2, "episode_count": 15}
  ]
}`

const dvdGroupBody = `{
  "id": "dvd-full", "name": "DVD Order", "type": 3,
  "groups": [
    {"name": "Season 1", "order": 1, "episodes": [
      {"order": 1, "season_number": 1, "episode_number": 1, "name": "The Train Job", "overview": "Aired first.", "air_date": "2002-09-20", "still_path": "/train.jpg", "vote_average": 7.9},
      {"order": 0, "season_number": 1, "episode_number": 11, "name": "Serenity", "overview": "The real pilot.", "air_date": "2002-12-20", "still_path": "/serenity.jpg", "vote_average": 8.4}
    ]},
    {"name": "Specials", "order": 0, "episodes": [
      {"order": 0, "season_number": 0, "episode_number": 1, "name": "Here's How It Was", "air_date": "2003-12-09"}
    ]}
  ]
}`

func TestGetEpisodeOrdering_DVDPicksFullestGroup(t *testing.T) {
	srv, counts := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/tv/1437/episode_groups":
			_, _ = w.Write([]byte(episodeGroupsBody))
		case "/3/tv/episode_group/dvd-full":
			_, _ = w.Write([]byte(dvdGroupBody))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer srv.Close()

	c := testClient(t, srv).WithCacheDir(t.TempDir())
	o, err := c.GetEpisodeOrdering(context.Background(), 1437, metadata.EpisodeOrderDVD)
	if err != nil {
		t.Fatalf("GetEpisodeOrdering: %v", err)
	}
	// Group order is the season, episode order + 1 the episode.
	if r := o.Lookup(1, 1); r == nil || r.Title != "Serenity" || r.SeasonNum != 1 || r.EpisodeNum != 11 {
		t.Errorf("DVD S1E1 = %+v, want Serenity (aired S1E11)", r)
	}
	if r := o.Lookup(1, 2); r == nil || r.Title != "The Train Job" || r.ThumbURL != imageBaseURL+"/train.jpg" || r.AirDate.Year() != 2002 {
		t.Errorf("DVD S1E2 = %+v", r)
	}
	if r := o.Lookup(0, 1); r == nil || r.Title != "Here's How It Was" {
		t.Errorf("DVD S0E1 = %+v", r)
	}
	// Absolute numbers skip aired specials: specials sort first (order 0)
	// but take no number.
	for _, oe := range o.Episodes {
		if oe.Episode.SeasonNum == 0 && oe.Absolute != 0 {
			t.Errorf("special got absolute number %d", oe.Absolute)
		}
	}

	// Second call is served from the disk cache.
	if _, err := c.GetEpisodeOrdering(context.Background(), 1437, metadata.EpisodeOrderDVD); err != nil {
		t.Fatalf("cached GetEpisodeOrdering: %v", err)
	}
	if (*counts)["/tv/1437/episode_groups"] != 1 || (*counts)["/tv/episode_group/dvd-full"] != 1 {
		t.Errorf("requests = %v, want one of each (second call cached)", *counts)
	}
}

func TestGetEpisodeOrdering_Unavailable(t *testing.T) {
	srv, _ := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id": 1, "results": [{"id": "x", "type": 1, "episode_count": 10}]}`))
	})
	defer srv.Close()
	c := testClient(t, srv)
	_, err := c.GetEpisodeOrdering(context.Background(), 1, metadata.EpisodeOrderAbsolute)
	if !errors.Is(err, metadata.ErrEpisodeOrderUnavailable) {
		t.Fatalf("err = %v, want ErrEpisodeOrderUnavailable", err)
	}
	if _, err := c.GetEpisodeOrdering(context.Background(), 1, metadata.EpisodeOrderAired); !errors.Is(err, metadata.ErrEpisodeOrderUnavailable) {
		t.Fatalf("aired: err = %v, want ErrEpisodeOrderUnavailable", err)
	}
}

func TestGetEpisodeOrdering_AbsoluteRunsAcrossGroups(t *testing.T) {
	srv, _ := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/tv/9/episode_groups":
			_, _ = w.Write([]byte(`{"results": [{"id": "abs", "type": 2, "episode_count": 3}]}`))
		case "/3/tv/episode_group/abs":
			_, _ = w.Write([]byte(`{"groups": [
			  {"order": 2, "episodes": [{"order": 0, "season_number": 2, "episode_number": 1, "name": "Third"}]},
			  {"order": 1, "episodes": [
			    {"order": 0, "season_number": 1, "episode_number": 1, "name": "First"},
			    {"order": 1, "season_number": 0, "episode_number": 4, "name": "A special"},
			    {"order": 2, "season_number": 1, "episode_number": 2, "name": "Second"}
			  ]}
			]}`))
		}
	})
	defer srv.Close()
	o, err := testClient(t, srv).GetEpisodeOrdering(context.Background(), 9, metadata.EpisodeOrderAbsolute)
	if err != nil {
		t.Fatalf("GetEpisodeOrdering: %v", err)
	}
	for n, want := range map[int]string{1: "First", 2: "Second", 3: "Third"} {
		if r := o.Lookup(1, n); r == nil || r.Title != want {
			t.Errorf("absolute %d = %+v, want %q", n, r, want)
		}
	}
}
