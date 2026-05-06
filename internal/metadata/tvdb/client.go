// Package tvdb implements a TheTVDB v4 API client used as a fallback when TMDB
// lookups fail — both for per-episode metadata (anime with absolute numbering,
// differently grouped seasons) and for show-level metadata/artwork when a show
// isn't in TMDB or TMDB returned no poster.
package tvdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/safehttp"
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
		apiKey: apiKey,
		// safehttp.NewClient blocks dial-time connections to private,
		// loopback, link-local, multicast, and unspecified addresses.
		// api4.thetvdb.com is public; this is defense in depth against
		// DNS rebinding or a hijacked apex record.
		httpClient: safehttp.NewClient(safehttp.DialPolicy{}, 10*time.Second),
	}
}

// SearchSeries looks up a show by title (optionally filtered by first-air year)
// and returns the top match with poster+fanart URLs resolved from the series'
// extended record. Intended as a fallback when the TMDB agent finds no show.
func (c *Client) SearchSeries(ctx context.Context, title string, year int) (*metadata.TVShowResult, error) {
	token, err := c.ensureToken(ctx)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("query", title)
	q.Set("type", "series")
	if year > 0 {
		q.Set("year", fmt.Sprintf("%d", year))
	}
	searchURL := fmt.Sprintf("%s/search?%s", baseURL, q.Encode())

	var searchResp struct {
		Data []tvdbSearchHit `json:"data"`
	}
	if err := c.getJSON(ctx, token, searchURL, &searchResp); err != nil {
		return nil, fmt.Errorf("tvdb search series %q: %w", title, err)
	}
	if len(searchResp.Data) == 0 {
		return nil, fmt.Errorf("tvdb: no series match for %q", title)
	}

	// Pick the first hit whose TVDB id parses — the search API returns the
	// id as a string, not an int.
	var hit *tvdbSearchHit
	var seriesID int
	for i := range searchResp.Data {
		if id, convErr := parseTVDBID(searchResp.Data[i].TVDBID); convErr == nil {
			hit = &searchResp.Data[i]
			seriesID = id
			break
		}
	}
	if hit == nil {
		return nil, fmt.Errorf("tvdb: no usable series id in search results for %q", title)
	}

	poster, fanart := c.fetchSeriesArtwork(ctx, token, seriesID)
	// Fall back to the search hit's image_url if the extended fetch failed to
	// turn up a typed poster.
	if poster == "" {
		poster = hit.ImageURL
	}

	firstAirYear := 0
	if hit.Year != "" {
		fmt.Sscanf(hit.Year, "%d", &firstAirYear)
	}

	return &metadata.TVShowResult{
		TVDBID:       seriesID,
		Title:        hit.Name,
		FirstAirYear: firstAirYear,
		Summary:      hit.Overview,
		PosterURL:    poster,
		FanartURL:    fanart,
	}, nil
}

// fetchSeriesArtwork pulls the series' extended record and picks the highest
// scored poster + fanart. Returns empty strings on any error — the caller
// falls back to the search hit's image_url.
func (c *Client) fetchSeriesArtwork(ctx context.Context, token string, seriesID int) (poster, fanart string) {
	extURL := fmt.Sprintf("%s/series/%d/extended", baseURL, seriesID)
	var extResp struct {
		Data struct {
			Image    string        `json:"image"`
			Artworks []tvdbArtwork `json:"artworks"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, token, extURL, &extResp); err != nil {
		return "", ""
	}

	// TVDB artwork types: 2 = poster, 3 = background/fanart.
	posters := filterArtworks(extResp.Data.Artworks, 2)
	fanarts := filterArtworks(extResp.Data.Artworks, 3)
	sort.SliceStable(posters, func(i, j int) bool { return posters[i].Score > posters[j].Score })
	sort.SliceStable(fanarts, func(i, j int) bool { return fanarts[i].Score > fanarts[j].Score })

	if len(posters) > 0 {
		poster = posters[0].Image
	} else {
		poster = extResp.Data.Image
	}
	if len(fanarts) > 0 {
		fanart = fanarts[0].Image
	}
	return poster, fanart
}

func filterArtworks(all []tvdbArtwork, kind int) []tvdbArtwork {
	out := make([]tvdbArtwork, 0, len(all))
	for _, a := range all {
		if a.Type == kind && a.Image != "" {
			out = append(out, a)
		}
	}
	return out
}

func parseTVDBID(raw any) (int, error) {
	switch v := raw.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case string:
		// TVDB search returns ids prefixed with "series-" sometimes.
		s := strings.TrimPrefix(v, "series-")
		var n int
		if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
			return 0, fmt.Errorf("parse tvdb id %q: %w", v, err)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("unexpected tvdb id type %T", raw)
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

// tvdbSearchHit matches the /search endpoint schema. The TVDB id comes back
// as a string here (unlike the extended endpoint where it's numeric), so we
// decode it through any and parse it in parseTVDBID.
type tvdbSearchHit struct {
	TVDBID   any    `json:"tvdb_id"`
	Name     string `json:"name"`
	Overview string `json:"overview"`
	ImageURL string `json:"image_url"`
	Year     string `json:"year"`
}

// tvdbArtwork matches an entry in the /series/{id}/extended artworks list.
// Type 2 = poster, 3 = background/fanart. Score is TVDB's community rating.
type tvdbArtwork struct {
	Image string  `json:"image"`
	Type  int     `json:"type"`
	Score float64 `json:"score"`
}
