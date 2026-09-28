package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Movie is the subset of a Radarr /api/v3/movie resource the download-status
// sync reads: whether Radarr is still looking for it and whether a file has
// landed.
type Movie struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	TMDBID    int    `json:"tmdbId"`
	Monitored bool   `json:"monitored"`
	HasFile   bool   `json:"hasFile"`
	// Status is Radarr's release status: tba, announced, inCinemas, released.
	Status      string `json:"status"`
	IsAvailable bool   `json:"isAvailable"`
}

// MovieByID fetches one movie from Radarr's library. ErrNotFound means it was
// removed from Radarr.
func (c *Client) MovieByID(ctx context.Context, id int) (*Movie, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v3/movie/"+strconv.Itoa(id), nil, nil, &raw); err != nil {
		return nil, err
	}
	var m Movie
	if err := decodeObject(raw, &m); err != nil {
		return nil, fmt.Errorf("arr: decode movie: %w", err)
	}
	if m.ID == 0 {
		return nil, ErrNotFound
	}
	return &m, nil
}

// MovieByTMDB finds the movie with this TMDB id in Radarr's library
// (GET /api/v3/movie?tmdbId=). The result is re-checked so a build that
// ignores the filter can't return the wrong movie. ErrNotFound when Radarr
// doesn't manage it.
func (c *Client) MovieByTMDB(ctx context.Context, tmdbID int) (*Movie, error) {
	var raw json.RawMessage
	q := url.Values{"tmdbId": {strconv.Itoa(tmdbID)}}
	if err := c.do(ctx, http.MethodGet, "/api/v3/movie", q, nil, &raw); err != nil {
		return nil, err
	}
	records, _, _, err := decodeRecordList(raw)
	if err != nil {
		return nil, fmt.Errorf("arr: decode movies: %w", err)
	}
	for _, rec := range records {
		var m Movie
		if decodeObject(rec, &m) != nil {
			continue
		}
		if m.ID > 0 && m.TMDBID == tmdbID {
			return &m, nil
		}
	}
	return nil, ErrNotFound
}

// Series is the subset of a Sonarr /api/v3/series resource the sync reads.
type Series struct {
	ID         int               `json:"id"`
	Title      string            `json:"title"`
	TVDBID     int               `json:"tvdbId"`
	TMDBID     int               `json:"tmdbId"`
	Monitored  bool              `json:"monitored"`
	Statistics *SeriesStatistics `json:"statistics"`
	// Seasons carries each season's monitoring and file tally (see
	// sonarr_seasons.go); season-level requests are judged per season.
	Seasons []SeriesSeasonState `json:"seasons"`
}

// SeriesStatistics is Sonarr's per-series file tally. EpisodeCount counts
// monitored episodes that have aired (or have a file); EpisodeFileCount the
// ones with a file.
type SeriesStatistics struct {
	EpisodeFileCount  int       `json:"episodeFileCount"`
	EpisodeCount      int       `json:"episodeCount"`
	TotalEpisodeCount int       `json:"totalEpisodeCount"`
	SizeOnDisk        FlexFloat `json:"sizeOnDisk"`
	PercentOfEpisodes FlexFloat `json:"percentOfEpisodes"`
}

// SeriesByID fetches one series from Sonarr's library. ErrNotFound means it
// was removed from Sonarr.
func (c *Client) SeriesByID(ctx context.Context, id int) (*Series, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v3/series/"+strconv.Itoa(id), nil, nil, &raw); err != nil {
		return nil, err
	}
	var s Series
	if err := decodeObject(raw, &s); err != nil {
		return nil, fmt.Errorf("arr: decode series: %w", err)
	}
	if s.ID == 0 {
		return nil, ErrNotFound
	}
	return &s, nil
}

// SeriesByTVDB finds the series with this TVDB id in Sonarr's library
// (GET /api/v3/series?tvdbId=), re-checking the id like MovieByTMDB.
// ErrNotFound when Sonarr doesn't manage it.
func (c *Client) SeriesByTVDB(ctx context.Context, tvdbID int) (*Series, error) {
	var raw json.RawMessage
	q := url.Values{"tvdbId": {strconv.Itoa(tvdbID)}}
	if err := c.do(ctx, http.MethodGet, "/api/v3/series", q, nil, &raw); err != nil {
		return nil, err
	}
	records, _, _, err := decodeRecordList(raw)
	if err != nil {
		return nil, fmt.Errorf("arr: decode series list: %w", err)
	}
	for _, rec := range records {
		var s Series
		if decodeObject(rec, &s) != nil {
			continue
		}
		if s.ID > 0 && s.TVDBID == tvdbID {
			return &s, nil
		}
	}
	return nil, ErrNotFound
}
