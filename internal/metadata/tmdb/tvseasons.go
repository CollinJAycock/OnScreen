package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/onscreen/onscreen/internal/metadata"
)

// ErrNotFound is the error a TMDB 404 wraps (IsNotFound reports it). Exported
// so callers' tests can fake a "no such title" answer.
var ErrNotFound = errNotFound

// GetTVSeasons returns a show's season list and TMDB's airing position
// (last_episode_to_air). It issues exactly the request RefreshTV does (same
// path and parameters), so the two share a disk-cache entry: a request that
// just snapshotted the show answers its season check from cache.
//
// Seasons come back in season-number order with specials (season 0) last,
// the order the request season picker shows them in.
func (c *Client) GetTVSeasons(ctx context.Context, tmdbID int) (*metadata.TVSeasonList, error) {
	var tv tmdbTVSeasons
	params := url.Values{}
	params.Set("language", c.language)
	params.Set("append_to_response", "content_ratings,external_ids")
	if err := c.get(ctx, fmt.Sprintf("/tv/%d", tmdbID), params, &tv); err != nil {
		return nil, fmt.Errorf("tmdb tv seasons %d: %w", tmdbID, err)
	}
	return tv.toSeasonList(), nil
}

// tmdbTVSeasons is the season-related part of a /tv/{id} details payload.
type tmdbTVSeasons struct {
	ID      int `json:"id"`
	Seasons []struct {
		SeasonNumber int    `json:"season_number"`
		Name         string `json:"name"`
		EpisodeCount int    `json:"episode_count"`
		AirDate      string `json:"air_date"`
		PosterPath   string `json:"poster_path"`
	} `json:"seasons"`
	LastEpisodeToAir *struct {
		SeasonNumber  int `json:"season_number"`
		EpisodeNumber int `json:"episode_number"`
	} `json:"last_episode_to_air"`
}

func (t tmdbTVSeasons) toSeasonList() *metadata.TVSeasonList {
	out := &metadata.TVSeasonList{TMDBID: t.ID}
	seen := map[int]bool{}
	for _, s := range t.Seasons {
		if s.SeasonNumber < 0 || seen[s.SeasonNumber] {
			continue
		}
		seen[s.SeasonNumber] = true
		air, _ := time.Parse("2006-01-02", s.AirDate)
		out.Seasons = append(out.Seasons, metadata.TVSeasonSummary{
			Number:       s.SeasonNumber,
			Name:         s.Name,
			EpisodeCount: s.EpisodeCount,
			AirDate:      air,
			PosterURL:    imageURL(s.PosterPath),
		})
	}
	sort.SliceStable(out.Seasons, func(i, j int) bool {
		a, b := out.Seasons[i].Number, out.Seasons[j].Number
		if (a == 0) != (b == 0) {
			return b == 0 // specials last
		}
		return a < b
	})
	if le := t.LastEpisodeToAir; le != nil && le.SeasonNumber >= 0 {
		out.LastAiredSeason = le.SeasonNumber
		out.LastAiredEpisode = le.EpisodeNumber
	}
	return out
}
