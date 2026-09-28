package tmdb

import (
	"context"
	"net/http"
	"testing"
	"time"
)

const tvSeasonsBody = `{
  "id": 1399, "name": "Show",
  "seasons": [
    {"season_number": 0, "name": "Specials", "episode_count": 3, "air_date": "2010-01-01", "poster_path": "/sp.jpg"},
    {"season_number": 2, "name": "Season 2", "episode_count": 10, "air_date": "2012-04-01"},
    {"season_number": 1, "name": "Season 1", "episode_count": 10, "air_date": "2011-04-17", "poster_path": "/s1.jpg"},
    {"season_number": 3, "name": "Season 3", "episode_count": 8, "air_date": "2026-09-01"},
    {"season_number": 4, "name": "Season 4", "episode_count": 0, "air_date": null}
  ],
  "last_episode_to_air": {"season_number": 3, "episode_number": 4}
}`

func TestGetTVSeasons_ParsesAndOrdersSpecialsLast(t *testing.T) {
	srv, counts := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		// Same request RefreshTV makes, so the two share a cache entry.
		if got := r.URL.Query().Get("append_to_response"); r.URL.Path == "/3/tv/1399" && got != "content_ratings,external_ids" {
			t.Errorf("append_to_response = %q", got)
		}
		_, _ = w.Write([]byte(tvSeasonsBody))
	})
	defer srv.Close()

	c := testClient(t, srv).WithCacheDir(t.TempDir())
	list, err := c.GetTVSeasons(context.Background(), 1399)
	if err != nil {
		t.Fatalf("GetTVSeasons: %v", err)
	}
	var order []int
	for _, s := range list.Seasons {
		order = append(order, s.Number)
	}
	if want := []int{1, 2, 3, 4, 0}; !equalInts(order, want) {
		t.Errorf("season order = %v, want %v", order, want)
	}
	s1, _ := list.Season(1)
	if s1.PosterURL != imageBaseURL+"/s1.jpg" || s1.EpisodeCount != 10 || s1.AirDate.Year() != 2011 {
		t.Errorf("season 1 = %+v", s1)
	}
	if list.LastAiredSeason != 3 || list.LastAiredEpisode != 4 {
		t.Errorf("last aired = S%dE%d, want S3E4", list.LastAiredSeason, list.LastAiredEpisode)
	}

	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	for n, want := range map[int]int{0: 3, 1: 10, 2: 10, 3: 4, 4: 0, 9: 0} {
		if got := list.AiredEpisodes(n, now); got != want {
			t.Errorf("AiredEpisodes(%d) = %d, want %d", n, got, want)
		}
	}
	if got := list.AiredSeasons(now); !equalInts(got, []int{1, 2, 3}) {
		t.Errorf("AiredSeasons = %v, want [1 2 3]", got)
	}

	// RefreshTV answers from the entry GetTVSeasons cached.
	if _, err := c.RefreshTV(context.Background(), 1399); err != nil {
		t.Fatalf("RefreshTV: %v", err)
	}
	if (*counts)["/tv/1399"] != 1 {
		t.Errorf("TMDB /tv/1399 hit %d times, want 1 (shared cache entry)", (*counts)["/tv/1399"])
	}
}

func TestTVSeasonList_AiredWithoutAiringPosition(t *testing.T) {
	srv, _ := countingHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id": 5, "seasons": [
		  {"season_number": 1, "episode_count": 6, "air_date": "2020-01-01"},
		  {"season_number": 2, "episode_count": 6, "air_date": "2099-01-01"}]}`))
	})
	defer srv.Close()
	list, err := testClient(t, srv).GetTVSeasons(context.Background(), 5)
	if err != nil {
		t.Fatalf("GetTVSeasons: %v", err)
	}
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	if list.AiredEpisodes(1, now) != 6 || list.AiredEpisodes(2, now) != 0 {
		t.Errorf("aired = %d/%d, want 6/0", list.AiredEpisodes(1, now), list.AiredEpisodes(2, now))
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
