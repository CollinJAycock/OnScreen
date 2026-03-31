// Package tmdb implements the metadata.Agent interface using The Movie Database API.
// Rate limiting: token bucket at configurable req/s (default 20) per ADR.
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/onscreen/onscreen/internal/metadata"
)

const baseURL = "https://api.themoviedb.org/3"
const imageBaseURL = "https://image.tmdb.org/t/p/original"

// Client is the TMDB metadata agent.
type Client struct {
	apiKey     string
	httpClient *http.Client
	limiter    *rate.Limiter
	language   string
}

// New creates a TMDB client.
// rateLimit is requests per second (default 20; TMDB allows ~50/s).
func New(apiKey string, rateLimit int, language string) *Client {
	if rateLimit <= 0 {
		rateLimit = 20
	}
	if language == "" {
		language = "en-US"
	}
	return &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		limiter:    rate.NewLimiter(rate.Limit(rateLimit), rateLimit),
		language:   language,
	}
}

// SearchMovie implements metadata.Agent.
func (c *Client) SearchMovie(ctx context.Context, title string, year int) (*metadata.MovieResult, error) {
	params := url.Values{}
	params.Set("query", title)
	params.Set("language", c.language)
	if year > 0 {
		params.Set("year", strconv.Itoa(year))
	}

	var resp struct {
		Results []tmdbMovie `json:"results"`
	}
	if err := c.get(ctx, "/search/movie", params, &resp); err != nil {
		return nil, fmt.Errorf("tmdb search movie %q: %w", title, err)
	}
	if len(resp.Results) == 0 {
		return nil, fmt.Errorf("tmdb: no results for %q (%d)", title, year)
	}

	return c.movieToResult(ctx, resp.Results[0])
}

// SearchTV implements metadata.Agent.
// It passes year to TMDB when > 0 and picks the result whose title is the
// closest match instead of blindly returning the first hit.
func (c *Client) SearchTV(ctx context.Context, title string, year int) (*metadata.TVShowResult, error) {
	params := url.Values{}
	params.Set("query", title)
	params.Set("language", c.language)
	if year > 0 {
		params.Set("first_air_date_year", strconv.Itoa(year))
	}

	var resp struct {
		Results []tmdbTV `json:"results"`
	}
	if err := c.get(ctx, "/search/tv", params, &resp); err != nil {
		return nil, fmt.Errorf("tmdb search tv %q: %w", title, err)
	}
	if len(resp.Results) == 0 {
		// If year was specified and got no results, retry without year filter.
		if year > 0 {
			return c.SearchTV(ctx, title, 0)
		}
		return nil, fmt.Errorf("tmdb: no results for TV %q", title)
	}

	best := pickBestTVMatch(resp.Results, title)
	result := c.tvToResult(ctx, best)

	// Fetch external IDs (TVDB) for the show — best-effort, don't fail the search.
	if tvdbID, imdbID, err := c.GetTVExternalIDs(ctx, result.TMDBID); err == nil {
		result.TVDBID = tvdbID
		if result.IMDBID == "" {
			result.IMDBID = imdbID
		}
	}

	return result, nil
}

// SearchTVCandidates implements metadata.Agent.
// Returns up to 10 TV show results for manual match selection.
func (c *Client) SearchTVCandidates(ctx context.Context, query string) ([]metadata.TVShowResult, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("language", c.language)

	var resp struct {
		Results []tmdbTV `json:"results"`
	}
	if err := c.get(ctx, "/search/tv", params, &resp); err != nil {
		return nil, fmt.Errorf("tmdb search tv candidates %q: %w", query, err)
	}

	limit := len(resp.Results)
	if limit > 10 {
		limit = 10
	}
	out := make([]metadata.TVShowResult, limit)
	for i := 0; i < limit; i++ {
		out[i] = *c.tvToResult(ctx, resp.Results[i])
	}
	return out, nil
}

// pickBestTVMatch selects the TMDB result whose title is the closest match to
// the search query. Exact (case-insensitive) matches win, then prefix matches,
// then the first result from TMDB (which is ranked by popularity).
func pickBestTVMatch(results []tmdbTV, query string) tmdbTV {
	normQ := normTitle(query)

	// Pass 1: exact match on Name or OriginalName.
	for _, r := range results {
		if normTitle(r.Name) == normQ || normTitle(r.OriginalName) == normQ {
			return r
		}
	}

	// Pass 2: query is a prefix of name or vice versa (handles "Good Eats" vs "Good Eats: Reloaded").
	for _, r := range results {
		rn := normTitle(r.Name)
		if strings.HasPrefix(rn, normQ) || strings.HasPrefix(normQ, rn) {
			return r
		}
		on := normTitle(r.OriginalName)
		if strings.HasPrefix(on, normQ) || strings.HasPrefix(normQ, on) {
			return r
		}
	}

	// Pass 3: default to TMDB's top result.
	return results[0]
}

// normTitle lowercases and collapses non-alphanumeric runs for fuzzy comparison.
func normTitle(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// GetSeason implements metadata.Agent.
func (c *Client) GetSeason(ctx context.Context, showTMDBID, seasonNum int) (*metadata.SeasonResult, error) {
	var s tmdbSeason
	path := fmt.Sprintf("/tv/%d/season/%d", showTMDBID, seasonNum)
	params := url.Values{}
	params.Set("language", c.language)
	if err := c.get(ctx, path, params, &s); err != nil {
		return nil, fmt.Errorf("tmdb get season %d/%d: %w", showTMDBID, seasonNum, err)
	}
	air, _ := time.Parse("2006-01-02", s.AirDate)
	return &metadata.SeasonResult{
		Number:    s.SeasonNumber,
		Name:      s.Name,
		Summary:   s.Overview,
		AirDate:   air,
		PosterURL: imageURL(s.PosterPath),
	}, nil
}

// GetEpisode implements metadata.Agent.
func (c *Client) GetEpisode(ctx context.Context, showTMDBID, seasonNum, episodeNum int) (*metadata.EpisodeResult, error) {
	var e tmdbEpisode
	path := fmt.Sprintf("/tv/%d/season/%d/episode/%d", showTMDBID, seasonNum, episodeNum)
	params := url.Values{}
	params.Set("language", c.language)
	if err := c.get(ctx, path, params, &e); err != nil {
		return nil, fmt.Errorf("tmdb get episode %d/%d/%d: %w", showTMDBID, seasonNum, episodeNum, err)
	}
	air, _ := time.Parse("2006-01-02", e.AirDate)
	return &metadata.EpisodeResult{
		ShowTMDBID: showTMDBID,
		SeasonNum:  seasonNum,
		EpisodeNum: e.EpisodeNumber,
		Title:      e.Name,
		Summary:    e.Overview,
		AirDate:    air,
		Rating:     e.VoteAverage,
		ThumbURL:   imageURL(e.StillPath),
	}, nil
}

// RefreshMovie implements metadata.Agent.
func (c *Client) RefreshMovie(ctx context.Context, tmdbID int) (*metadata.MovieResult, error) {
	var movie tmdbMovie
	path := fmt.Sprintf("/movie/%d", tmdbID)
	params := url.Values{}
	params.Set("language", c.language)
	if err := c.get(ctx, path, params, &movie); err != nil {
		return nil, fmt.Errorf("tmdb refresh movie %d: %w", tmdbID, err)
	}
	return c.movieToResult(ctx, movie)
}

// RefreshTV implements metadata.Agent.
func (c *Client) RefreshTV(ctx context.Context, tmdbID int) (*metadata.TVShowResult, error) {
	var tv tmdbTV
	path := fmt.Sprintf("/tv/%d", tmdbID)
	params := url.Values{}
	params.Set("language", c.language)
	if err := c.get(ctx, path, params, &tv); err != nil {
		return nil, fmt.Errorf("tmdb refresh tv %d: %w", tmdbID, err)
	}
	result := c.tvToResult(ctx, tv)

	// Fetch external IDs (TVDB) — best-effort.
	if tvdbID, imdbID, err := c.GetTVExternalIDs(ctx, tmdbID); err == nil {
		result.TVDBID = tvdbID
		if result.IMDBID == "" {
			result.IMDBID = imdbID
		}
	}

	return result, nil
}

// GetTVExternalIDs fetches the external IDs (TVDB, IMDB) for a TV show.
// Returns 0 for TVDB ID if not available.
func (c *Client) GetTVExternalIDs(ctx context.Context, tmdbID int) (tvdbID int, imdbID string, err error) {
	var resp struct {
		TVDBID int    `json:"tvdb_id"`
		IMDBID string `json:"imdb_id"`
	}
	path := fmt.Sprintf("/tv/%d/external_ids", tmdbID)
	if err := c.get(ctx, path, nil, &resp); err != nil {
		return 0, "", fmt.Errorf("tmdb get external ids %d: %w", tmdbID, err)
	}
	return resp.TVDBID, resp.IMDBID, nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func (c *Client) get(ctx context.Context, path string, params url.Values, dest any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter: %w", err)
	}

	if params == nil {
		params = url.Values{}
	}
	params.Set("api_key", c.apiKey)

	u := baseURL + path + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("not found")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(dest)
}

func (c *Client) movieToResult(ctx context.Context, m tmdbMovie) (*metadata.MovieResult, error) {
	release, _ := time.Parse("2006-01-02", m.ReleaseDate)
	genres := make([]string, len(m.Genres))
	for i, g := range m.Genres {
		genres[i] = g.Name
	}
	cert := c.getMovieCertification(ctx, m.ID)
	return &metadata.MovieResult{
		TMDBID:        m.ID,
		IMDBID:        m.IMDBID,
		Title:         m.Title,
		OriginalTitle: m.OriginalTitle,
		Year:          release.Year(),
		Summary:       m.Overview,
		Tagline:       m.Tagline,
		Rating:        m.VoteAverage,
		ContentRating: cert,
		DurationMS:    int64(m.Runtime) * 60 * 1000,
		Genres:        genres,
		ReleaseDate:   release,
		PosterURL:     imageURL(m.PosterPath),
		FanartURL:     imageURL(m.BackdropPath),
	}, nil
}

func (c *Client) tvToResult(ctx context.Context, t tmdbTV) *metadata.TVShowResult {
	genres := make([]string, len(t.Genres))
	for i, g := range t.Genres {
		genres[i] = g.Name
	}
	var year int
	if t.FirstAirDate != "" {
		if d, err := time.Parse("2006-01-02", t.FirstAirDate); err == nil {
			year = d.Year()
		}
	}
	cert := c.getTVContentRating(ctx, t.ID)
	return &metadata.TVShowResult{
		TMDBID:        t.ID,
		Title:         t.Name,
		OriginalTitle: t.OriginalName,
		FirstAirYear:  year,
		Summary:       t.Overview,
		Rating:        t.VoteAverage,
		ContentRating: cert,
		Genres:        genres,
		PosterURL:     imageURL(t.PosterPath),
		FanartURL:     imageURL(t.BackdropPath),
	}
}

// getMovieCertification fetches the US certification (e.g. "PG-13") for a movie.
// Best-effort: returns "" on any error.
func (c *Client) getMovieCertification(ctx context.Context, tmdbID int) string {
	var resp struct {
		Results []struct {
			Country      string `json:"iso_3166_1"`
			ReleaseDates []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	}
	path := fmt.Sprintf("/movie/%d/release_dates", tmdbID)
	if err := c.get(ctx, path, nil, &resp); err != nil {
		return ""
	}
	for _, r := range resp.Results {
		if r.Country == "US" {
			for _, rd := range r.ReleaseDates {
				if rd.Certification != "" {
					return rd.Certification
				}
			}
		}
	}
	return ""
}

// getTVContentRating fetches the US content rating (e.g. "TV-14") for a TV show.
// Best-effort: returns "" on any error.
func (c *Client) getTVContentRating(ctx context.Context, tmdbID int) string {
	var resp struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		} `json:"results"`
	}
	path := fmt.Sprintf("/tv/%d/content_ratings", tmdbID)
	if err := c.get(ctx, path, nil, &resp); err != nil {
		return ""
	}
	for _, r := range resp.Results {
		if r.Country == "US" && r.Rating != "" {
			return r.Rating
		}
	}
	return ""
}

func imageURL(path string) string {
	if path == "" {
		return ""
	}
	return imageBaseURL + path
}

// ── TMDB wire types ───────────────────────────────────────────────────────────

type tmdbGenre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type tmdbMovie struct {
	ID            int         `json:"id"`
	IMDBID        string      `json:"imdb_id"`
	Title         string      `json:"title"`
	OriginalTitle string      `json:"original_title"`
	Overview      string      `json:"overview"`
	Tagline       string      `json:"tagline"`
	ReleaseDate   string      `json:"release_date"`
	Runtime       int         `json:"runtime"`
	VoteAverage   float64     `json:"vote_average"`
	PosterPath    string      `json:"poster_path"`
	BackdropPath  string      `json:"backdrop_path"`
	Genres        []tmdbGenre `json:"genres"`
}

type tmdbTV struct {
	ID           int         `json:"id"`
	Name         string      `json:"name"`
	OriginalName string      `json:"original_name"`
	Overview     string      `json:"overview"`
	FirstAirDate string      `json:"first_air_date"`
	VoteAverage  float64     `json:"vote_average"`
	PosterPath   string      `json:"poster_path"`
	BackdropPath string      `json:"backdrop_path"`
	Genres       []tmdbGenre `json:"genres"`
}

type tmdbSeason struct {
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	AirDate      string `json:"air_date"`
	PosterPath   string `json:"poster_path"`
}

type tmdbEpisode struct {
	EpisodeNumber int     `json:"episode_number"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	AirDate       string  `json:"air_date"`
	Runtime       int     `json:"runtime"`
	VoteAverage   float64 `json:"vote_average"`
	StillPath     string  `json:"still_path"`
}
