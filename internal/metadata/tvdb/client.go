// Package tvdb implements a TheTVDB v4 API client used as a fallback when TMDB
// episode lookups fail (e.g. anime with absolute numbering, differently grouped
// seasons). Only episode-level metadata is fetched from TVDB — show search and
// artwork remain on TMDB.
package tvdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onscreen/onscreen/internal/metadata"
)

const baseURL = "https://api4.thetvdb.com/v4"

// Client is the TheTVDB v4 metadata client.
type Client struct {
	apiKey     string
	httpClient *http.Client

	mu    sync.Mutex
	token string
	exp   time.Time
}

// New creates a TVDB client. apiKey is the project API key from thetvdb.com.
func New(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// GetEpisode fetches episode metadata by TVDB series ID, season number, and
// episode number. This is the main entry point used as a TMDB fallback.
func (c *Client) GetEpisode(ctx context.Context, tvdbSeriesID, seasonNum, episodeNum int) (*metadata.EpisodeResult, error) {
	token, err := c.ensureToken(ctx)
	if err != nil {
		return nil, err
	}

	// TVDB v4: GET /series/{id}/episodes/default?season={s}&episodeNumber={e}
	// Returns a paginated list; we filter to the exact episode.
	url := fmt.Sprintf("%s/series/%d/episodes/default?season=%d&episodeNumber=%d&page=0",
		baseURL, tvdbSeriesID, seasonNum, episodeNum)

	var resp struct {
		Data struct {
			Episodes []tvdbEpisode `json:"episodes"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, token, url, &resp); err != nil {
		return nil, fmt.Errorf("tvdb get episodes %d/s%de%d: %w", tvdbSeriesID, seasonNum, episodeNum, err)
	}

	// Find the exact episode match.
	for _, ep := range resp.Data.Episodes {
		if ep.SeasonNumber == seasonNum && ep.Number == episodeNum {
			air, _ := time.Parse("2006-01-02", ep.Aired)
			return &metadata.EpisodeResult{
				SeasonNum:  seasonNum,
				EpisodeNum: episodeNum,
				Title:      ep.Name,
				Summary:    ep.Overview,
				AirDate:    air,
				ThumbURL:   ep.Image,
			}, nil
		}
	}

	return nil, fmt.Errorf("tvdb: episode s%02de%02d not found for series %d", seasonNum, episodeNum, tvdbSeriesID)
}

// ensureToken returns a valid JWT, refreshing it if expired or missing.
func (c *Client) ensureToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.exp) {
		return c.token, nil
	}

	body := fmt.Sprintf(`{"apikey":"%s"}`, c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/login",
		strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("tvdb login: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("tvdb login: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tvdb login: status %d", resp.StatusCode)
	}

	var loginResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return "", fmt.Errorf("tvdb login: decode: %w", err)
	}

	c.token = loginResp.Data.Token
	// TVDB tokens expire after 30 days; refresh after 24 hours to be safe.
	c.exp = time.Now().Add(24 * time.Hour)

	return c.token, nil
}

// getJSON performs an authenticated GET request and decodes the response.
func (c *Client) getJSON(ctx context.Context, token, url string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("not found")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// Token may have expired server-side; clear it so next call re-authenticates.
		c.mu.Lock()
		c.token = ""
		c.mu.Unlock()
		return fmt.Errorf("unauthorized (token expired)")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(dest)
}

// ── Wire types ───────────────────────────────────────────────────────────────

type tvdbEpisode struct {
	ID           int    `json:"id"`
	SeriesID     int    `json:"seriesId"`
	Name         string `json:"name"`
	Aired        string `json:"aired"`
	Overview     string `json:"overview"`
	SeasonNumber int    `json:"seasonNumber"`
	Number       int    `json:"number"`
	Image        string `json:"image"`
}

