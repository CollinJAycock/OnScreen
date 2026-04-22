package arr

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// SeriesLookup is the subset of Sonarr's /api/v3/series/lookup result we need
// to round-trip into an AddSeriesRequest. Sonarr is TVDB-native; we accept
// either a TVDB id (best) or a TMDB id (Sonarr v4+) for lookup.
type SeriesLookup struct {
	Title          string        `json:"title"`
	SortTitle      string        `json:"sortTitle,omitempty"`
	TVDBID         int           `json:"tvdbId"`
	TMDBID         int           `json:"tmdbId,omitempty"`
	Year           int           `json:"year"`
	TitleSlug      string        `json:"titleSlug"`
	Overview       string        `json:"overview,omitempty"`
	SeriesType     string        `json:"seriesType,omitempty"`
	Images         []MovieImage  `json:"images,omitempty"`
	Seasons        []SeriesSeason `json:"seasons,omitempty"`
	Status         string        `json:"status,omitempty"`
}

// SeriesSeason mirrors Sonarr's per-season block. We forward this back on add
// so the user's season selection ("only S03+") translates to the right
// monitored=true/false flags.
type SeriesSeason struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

// LanguageProfile mirrors /api/v3/languageprofile (Sonarr v3 only — v4
// removed the concept).
type LanguageProfile struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
}

// LanguageProfiles fetches Sonarr language profiles. Returns an empty slice
// (not an error) on Sonarr v4 where the endpoint is gone.
func (c *Client) LanguageProfiles(ctx context.Context) ([]LanguageProfile, error) {
	var out []LanguageProfile
	if err := c.do(ctx, http.MethodGet, "/api/v3/languageprofile", nil, nil, &out); err != nil {
		if err == ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return out, nil
}

// LookupSeriesByTVDB calls /api/v3/series/lookup with `tvdb:<id>`.
func (c *Client) LookupSeriesByTVDB(ctx context.Context, tvdbID int) (*SeriesLookup, error) {
	return c.lookupSeries(ctx, fmt.Sprintf("tvdb:%d", tvdbID), func(s SeriesLookup) bool {
		return s.TVDBID == tvdbID
	})
}

// LookupSeriesByTMDB calls /api/v3/series/lookup with `tmdb:<id>`. Requires
// Sonarr v4+; older Sonarr instances will return an empty result and the
// caller should fall back to title-based lookup.
func (c *Client) LookupSeriesByTMDB(ctx context.Context, tmdbID int) (*SeriesLookup, error) {
	return c.lookupSeries(ctx, fmt.Sprintf("tmdb:%d", tmdbID), func(s SeriesLookup) bool {
		return s.TMDBID == tmdbID
	})
}

// LookupSeriesByTitle calls /api/v3/series/lookup with the raw title. Used as
// a last-resort fallback when ID-based lookup misses.
func (c *Client) LookupSeriesByTitle(ctx context.Context, title string) (*SeriesLookup, error) {
	return c.lookupSeries(ctx, title, func(SeriesLookup) bool { return true })
}

func (c *Client) lookupSeries(ctx context.Context, term string, match func(SeriesLookup) bool) (*SeriesLookup, error) {
	q := url.Values{"term": {term}}
	var results []SeriesLookup
	if err := c.do(ctx, http.MethodGet, "/api/v3/series/lookup", q, nil, &results); err != nil {
		return nil, err
	}
	for i := range results {
		if match(results[i]) {
			return &results[i], nil
		}
	}
	if len(results) > 0 {
		return &results[0], nil
	}
	return nil, ErrNotFound
}

// AddSeriesOptions controls Sonarr's onAdd behavior — same shape as Radarr's
// AddOptions but with show-aware fields.
type AddSeriesOptions struct {
	SearchForMissingEpisodes     bool   `json:"searchForMissingEpisodes"`
	SearchForCutoffUnmetEpisodes bool   `json:"searchForCutoffUnmetEpisodes,omitempty"`
	Monitor                      string `json:"monitor,omitempty"`
}

// AddSeriesRequest is the body for POST /api/v3/series. Like Radarr, Sonarr
// wants the lookup payload echoed back (title, tvdbId, year, titleSlug,
// seasons, images) plus the user's profile/folder/tag selections.
type AddSeriesRequest struct {
	Title             string           `json:"title"`
	TVDBID            int              `json:"tvdbId"`
	Year              int              `json:"year"`
	TitleSlug         string           `json:"titleSlug"`
	Images            []MovieImage     `json:"images,omitempty"`
	Seasons           []SeriesSeason   `json:"seasons"`
	QualityProfileID  int32            `json:"qualityProfileId"`
	LanguageProfileID int32            `json:"languageProfileId,omitempty"`
	RootFolderPath    string           `json:"rootFolderPath"`
	Monitored         bool             `json:"monitored"`
	SeasonFolder      bool             `json:"seasonFolder"`
	SeriesType        string           `json:"seriesType,omitempty"`
	Tags              []int32          `json:"tags,omitempty"`
	AddOptions        AddSeriesOptions `json:"addOptions"`
}

// AddSeriesResponse mirrors the relevant fields of the POST response.
type AddSeriesResponse struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

// AddSeries posts to /api/v3/series.
func (c *Client) AddSeries(ctx context.Context, req AddSeriesRequest) (*AddSeriesResponse, error) {
	var out AddSeriesResponse
	if err := c.do(ctx, http.MethodPost, "/api/v3/series", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
