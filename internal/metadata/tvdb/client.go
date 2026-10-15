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
	"strconv"
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
// and returns the match with poster+fanart URLs resolved from the series'
// extended record. Intended as a fallback when the TMDB agent finds no show.
// Like TMDB's SearchTV it is an unattended auto-match, so it takes only a
// series whose name, alias or translation is the title's once folded, first
// aired in the year or a year either side (metadata.MatchTitle), and errors
// otherwise. TVDB's own disambiguators ("The Office (US)", "Doctor Who
// (2005)") are compared without the qualifier, which picks between series
// sharing the name when the title carries one too.
func (c *Client) SearchSeries(ctx context.Context, title string, year int) (*metadata.TVShowResult, error) {
	token, err := c.ensureToken(ctx)
	if err != nil {
		return nil, err
	}
	name, country, qualifierYear := metadata.SplitShowQualifier(title)
	if year == 0 {
		year = qualifierYear
	}

	hits, err := c.searchSeries(ctx, token, title, year)
	if err != nil {
		return nil, err
	}
	matches := metadata.MatchTitle(name, year, seriesCandidates(hits))
	if len(matches) == 0 && year > 0 {
		// The year filter misses a series first aired a year off the
		// title's; the unfiltered search still holds it.
		if hits, err = c.searchSeries(ctx, token, title, 0); err != nil {
			return nil, err
		}
		matches = metadata.MatchTitle(name, year, seriesCandidates(hits))
	}
	if len(matches) == 0 {
		if len(hits) == 0 {
			return nil, fmt.Errorf("tvdb: no series match for %q", title)
		}
		return nil, fmt.Errorf("tvdb: no confident series match for %q (%d); top hit %q (%s)",
			title, year, hits[0].Name, hits[0].Year)
	}
	hit := &hits[matches[0]]
	if country != "" {
		for _, i := range matches {
			if _, hitCountry, _ := metadata.SplitShowQualifier(hits[i].Name); hitCountry == country {
				hit = &hits[i]
				break
			}
		}
	}
	seriesID, _ := parseTVDBID(hit.TVDBID)

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

// searchSeries runs one /search query for series, narrowed to shows first
// aired in year when year > 0.
func (c *Client) searchSeries(ctx context.Context, token, title string, year int) ([]tvdbSearchHit, error) {
	q := url.Values{}
	q.Set("query", title)
	q.Set("type", "series")
	if year > 0 {
		q.Set("year", fmt.Sprintf("%d", year))
	}
	var resp struct {
		Data []tvdbSearchHit `json:"data"`
	}
	if err := c.getJSON(ctx, token, fmt.Sprintf("%s/search?%s", baseURL, q.Encode()), &resp); err != nil {
		return nil, fmt.Errorf("tvdb search series %q: %w", title, err)
	}
	return resp.Data, nil
}

// seriesCandidates is hits as metadata.MatchTitle sees them: each one's name
// and aliases with any qualifier split off, plus its translations. A hit
// whose id doesn't parse (the search API returns it as a string) gets no
// titles, so it never matches.
func seriesCandidates(hits []tvdbSearchHit) []metadata.Candidate {
	out := make([]metadata.Candidate, len(hits))
	for i, h := range hits {
		if _, err := parseTVDBID(h.TVDBID); err != nil {
			continue
		}
		_, _, year := metadata.SplitShowQualifier(h.Name)
		if y, err := strconv.Atoi(h.Year); err == nil {
			year = y
		}
		var titles []string
		for _, t := range append([]string{h.Name}, h.Aliases...) {
			bare, _, _ := metadata.SplitShowQualifier(t)
			titles = append(titles, bare)
		}
		for _, t := range h.Translations {
			titles = append(titles, t)
		}
		out[i] = metadata.Candidate{Titles: titles, Year: year}
	}
	return out
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

// maxOrderingPages bounds GetEpisodeOrdering's paging: TVDB pages hold 500
// episodes, so 40 pages covers any real series and a misbehaving "next"
// link can't loop forever.
const maxOrderingPages = 40

// GetEpisodeOrdering implements metadata.EpisodeOrderProvider through TVDB
// season types: GET /series/{id}/episodes/{dvd|absolute}, every page. In
// the "dvd" type an episode's seasonNumber / number are its DVD season and
// episode; in "absolute" the number is its absolute number (season 0 still
// holds specials). ErrEpisodeOrderUnavailable (wrapped) when the series
// lists no episodes in that type.
func (c *Client) GetEpisodeOrdering(ctx context.Context, tvdbSeriesID int, order string) (*metadata.EpisodeOrdering, error) {
	if order != metadata.EpisodeOrderDVD && order != metadata.EpisodeOrderAbsolute {
		return nil, fmt.Errorf("tvdb episode ordering %q: %w", order, metadata.ErrEpisodeOrderUnavailable)
	}
	token, err := c.ensureToken(ctx)
	if err != nil {
		return nil, err
	}

	out := &metadata.EpisodeOrdering{Order: order}
	for page := 0; page < maxOrderingPages; page++ {
		pageURL := fmt.Sprintf("%s/series/%d/episodes/%s?page=%d", baseURL, tvdbSeriesID, order, page)
		var resp struct {
			Data struct {
				Episodes []tvdbEpisode `json:"episodes"`
			} `json:"data"`
			Links struct {
				Next *string `json:"next"`
			} `json:"links"`
		}
		if err := c.getJSON(ctx, token, pageURL, &resp); err != nil {
			if page > 0 {
				break // keep what the earlier pages gave
			}
			return nil, fmt.Errorf("tvdb %s episodes %d: %w", order, tvdbSeriesID, err)
		}
		for _, ep := range resp.Data.Episodes {
			air, _ := time.Parse("2006-01-02", ep.Aired)
			oe := metadata.OrderedEpisode{
				Season: ep.SeasonNumber,
				Number: ep.Number,
				Episode: metadata.EpisodeResult{
					SeasonNum:  ep.SeasonNumber,
					EpisodeNum: ep.Number,
					Title:      ep.Name,
					Summary:    ep.Overview,
					AirDate:    air,
					ThumbURL:   ep.Image,
				},
			}
			if order == metadata.EpisodeOrderAbsolute && ep.SeasonNumber != 0 {
				oe.Absolute = ep.Number
			}
			out.Episodes = append(out.Episodes, oe)
		}
		if resp.Links.Next == nil || *resp.Links.Next == "" || len(resp.Data.Episodes) == 0 {
			break
		}
	}
	if len(out.Episodes) == 0 {
		return nil, fmt.Errorf("tvdb series %d lists no %s episodes: %w", tvdbSeriesID, order, metadata.ErrEpisodeOrderUnavailable)
	}
	return out, nil
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
	TVDBID       any          `json:"tvdb_id"`
	Name         string       `json:"name"`
	Aliases      lenientNames `json:"aliases"`
	Translations lenientNames `json:"translations"` // language code → name
	Overview     string       `json:"overview"`
	ImageURL     string       `json:"image_url"`
	Year         string       `json:"year"`
}

// lenientNames is a list of names TVDB gives as strings ("aliases") or as a
// language → name object ("translations"); a {"name": ...} entry counts
// too. Anything else decodes to nothing, so a shape TVDB changes can't fail
// the search it rides on.
type lenientNames []string

func (n *lenientNames) UnmarshalJSON(b []byte) error {
	var list []any
	var byLanguage map[string]any
	switch {
	case json.Unmarshal(b, &list) == nil:
		for _, v := range list {
			switch v := v.(type) {
			case string:
				*n = append(*n, v)
			case map[string]any:
				if name, ok := v["name"].(string); ok {
					*n = append(*n, name)
				}
			}
		}
	case json.Unmarshal(b, &byLanguage) == nil:
		for _, v := range byLanguage {
			if name, ok := v.(string); ok {
				*n = append(*n, name)
			}
		}
	}
	return nil
}

// tvdbArtwork matches an entry in the /series/{id}/extended artworks list.
// Type 2 = poster, 3 = background/fanart. Score is TVDB's community rating.
type tvdbArtwork struct {
	Image string  `json:"image"`
	Type  int     `json:"type"`
	Score float64 `json:"score"`
}
