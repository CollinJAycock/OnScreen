// Package opensubtitles is an HTTP client for the OpenSubtitles.com REST API.
//
// Auth model: every request needs an Api-Key header. A login token bumps the
// per-day download quota (5/day anonymous, 100/day with a free account); search
// works without login. Tokens expire after 24h, so we re-login on 401.
//
// Rate limits: token bucket at 1 req/s by default. Hard 429/406 from upstream
// trips a circuit breaker for one hour, mirroring the TMDB client — both signal
// "stop calling us until things calm down."
package opensubtitles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/onscreen/onscreen/internal/safehttp"
)

const (
	baseURL         = "https://api.opensubtitles.com/api/v1"
	defaultUA       = "OnScreen v1.1"
	circuitCooldown = time.Hour
)

// ErrCircuitOpen is returned by every method while the breaker is open.
var ErrCircuitOpen = errors.New("opensubtitles circuit open (rate limit or auth failure)")

// ErrNotConfigured is returned when no API key has been supplied.
var ErrNotConfigured = errors.New("opensubtitles client not configured")

// Client is the OpenSubtitles API client.
type Client struct {
	apiKey    string
	username  string
	password  string
	userAgent string
	baseURL   string // overridable for tests; defaults to the public endpoint

	httpClient *http.Client
	limiter    *rate.Limiter

	mu          sync.Mutex
	token       string
	tokenExp    time.Time
	circuitOpen time.Time
}

// New creates a Client. apiKey is required; username/password are optional and
// only needed to lift the per-day download quota above the anonymous tier.
// userAgent is required by OpenSubtitles ToS — falls back to defaultUA if empty.
func New(apiKey, username, password, userAgent string) *Client {
	if userAgent == "" {
		userAgent = defaultUA
	}
	return &Client{
		apiKey:    apiKey,
		username:  username,
		password:  password,
		userAgent: userAgent,
		baseURL:   baseURL,
		// safehttp gates every dial post-resolution against the
		// loopback / RFC1918 / link-local / metadata-service ranges.
		// FetchFile downloads from a URL OpenSubtitles itself returns,
		// so a compromised or malicious upstream could otherwise pivot
		// the fetch into the operator's internal network.
		httpClient: safehttp.NewClient(safehttp.DialPolicy{}, 15*time.Second),
		// Tests can bypass the limiter's 1 req/s default via WithLimiter.
		limiter: rate.NewLimiter(rate.Limit(1), 2),
	}
}

// WithBaseURL overrides the API base URL. Intended for tests; production code
// should use the default which points at api.opensubtitles.com.
func (c *Client) WithBaseURL(u string) *Client {
	c.baseURL = u
	return c
}

// WithHTTPClient overrides the HTTP client. Intended for tests that hit
// httptest.NewServer (loopback, otherwise blocked by safehttp); production
// callers should use the default New constructor.
func (c *Client) WithHTTPClient(h *http.Client) *Client {
	c.httpClient = h
	return c
}

// WithLimiter replaces the rate limiter. Tests pass rate.NewLimiter(rate.Inf, 0)
// so repeated calls don't sleep.
func (c *Client) WithLimiter(l *rate.Limiter) *Client {
	c.limiter = l
	return c
}

// Configured reports whether the client has the minimum config to make calls.
func (c *Client) Configured() bool {
	return c != nil && c.apiKey != ""
}

// SearchOpts narrows a subtitle search. Languages is a comma-separated list of
// ISO-639-1 codes (e.g. "en,es"). For TV episodes, set Season + Episode and
// pass the *show* title in Query.
type SearchOpts struct {
	Query    string // movie or show title
	Year     int
	Season   int
	Episode  int
	IMDBID   string // numeric IMDB id without the "tt" prefix; preferred when known
	TMDBID   int
	Languages string
}

// SearchResult is one subtitle file returned by /subtitles search.
// FileID is the value to pass to Download() — *not* the subtitles row id.
type SearchResult struct {
	FileID        int     `json:"file_id"`
	FileName      string  `json:"file_name"`
	Language      string  `json:"language"`
	Release       string  `json:"release"`
	HearingImpaired bool   `json:"hearing_impaired"`
	HD            bool    `json:"hd"`
	FromTrusted   bool    `json:"from_trusted"`
	Rating        float32 `json:"rating"`
	DownloadCount int32   `json:"download_count"`
	UploaderName  string  `json:"uploader_name"`
}

// Search queries the /subtitles endpoint. Caller is responsible for narrowing
// by language; we don't second-guess and we don't paginate (50-result page is
// plenty for a UI picker).
func (c *Client) Search(ctx context.Context, opts SearchOpts) ([]SearchResult, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	if !c.circuitAllows() {
		return nil, ErrCircuitOpen
	}

	params := url.Values{}
	if opts.Query != "" {
		params.Set("query", opts.Query)
	}
	if opts.Year > 0 {
		params.Set("year", strconv.Itoa(opts.Year))
	}
	if opts.Season > 0 {
		params.Set("season_number", strconv.Itoa(opts.Season))
	}
	if opts.Episode > 0 {
		params.Set("episode_number", strconv.Itoa(opts.Episode))
	}
	if opts.IMDBID != "" {
		params.Set("imdb_id", strings.TrimPrefix(opts.IMDBID, "tt"))
	}
	if opts.TMDBID > 0 {
		params.Set("tmdb_id", strconv.Itoa(opts.TMDBID))
	}
	if opts.Languages != "" {
		params.Set("languages", opts.Languages)
	}

	var raw struct {
		Data []struct {
			Attributes struct {
				Language        string  `json:"language"`
				Release         string  `json:"release"`
				HearingImpaired bool    `json:"hearing_impaired"`
				HD              bool    `json:"hd"`
				FromTrusted     bool    `json:"from_trusted"`
				Ratings         float32 `json:"ratings"`
				DownloadCount   int32   `json:"download_count"`
				Uploader        struct {
					Name string `json:"name"`
				} `json:"uploader"`
				Files []struct {
					FileID   int    `json:"file_id"`
					FileName string `json:"file_name"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/subtitles", params, nil, false, &raw); err != nil {
		return nil, fmt.Errorf("opensubtitles search: %w", err)
	}

	results := make([]SearchResult, 0, len(raw.Data))
	for _, d := range raw.Data {
		if len(d.Attributes.Files) == 0 {
			continue
		}
		f := d.Attributes.Files[0]
		results = append(results, SearchResult{
			FileID:          f.FileID,
			FileName:        f.FileName,
			Language:        d.Attributes.Language,
			Release:         d.Attributes.Release,
			HearingImpaired: d.Attributes.HearingImpaired,
			HD:              d.Attributes.HD,
			FromTrusted:     d.Attributes.FromTrusted,
			Rating:          d.Attributes.Ratings,
			DownloadCount:   d.Attributes.DownloadCount,
			UploaderName:    d.Attributes.Uploader.Name,
		})
	}
	return results, nil
}

// DownloadInfo is the payload returned by /download — Link is short-lived
// (~3 hours) so callers should fetch immediately via FetchFile.
type DownloadInfo struct {
	Link      string
	FileName  string
	Remaining int // remaining downloads in current 24h window
}

// Download requests a download URL for the given file id. Counts against the
// account's daily quota even if FetchFile is never called.
func (c *Client) Download(ctx context.Context, fileID int) (*DownloadInfo, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	if !c.circuitAllows() {
		return nil, ErrCircuitOpen
	}

	body := map[string]any{"file_id": fileID}
	var raw struct {
		Link      string `json:"link"`
		FileName  string `json:"file_name"`
		Remaining int    `json:"remaining"`
	}
	if err := c.do(ctx, http.MethodPost, "/download", nil, body, true, &raw); err != nil {
		return nil, fmt.Errorf("opensubtitles download: %w", err)
	}
	return &DownloadInfo{
		Link:      raw.Link,
		FileName:  raw.FileName,
		Remaining: raw.Remaining,
	}, nil
}

// FetchFile pulls bytes from the short-lived URL returned by Download.
// The response is the raw subtitle file (typically SRT). Caller converts to VTT.
func (c *Client) FetchFile(ctx context.Context, link string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch subtitle file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch subtitle file: status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

// ── auth ────────────────────────────────────────────────────────────────────

func (c *Client) ensureLoggedIn(ctx context.Context) error {
	if c.username == "" || c.password == "" {
		return nil
	}
	c.mu.Lock()
	if c.token != "" && time.Now().Before(c.tokenExp) {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	body := map[string]string{"username": c.username, "password": c.password}
	var raw struct {
		Token string `json:"token"`
	}
	if err := c.do(ctx, http.MethodPost, "/login", nil, body, false, &raw); err != nil {
		return fmt.Errorf("opensubtitles login: %w", err)
	}
	c.mu.Lock()
	c.token = raw.Token
	// API doesn't return expiry; assume 23h to leave headroom before the 24h limit.
	c.tokenExp = time.Now().Add(23 * time.Hour)
	c.mu.Unlock()
	return nil
}

// ── circuit ────────────────────────────────────────────────────────────────

func (c *Client) circuitAllows() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.circuitOpen.IsZero() {
		return true
	}
	if time.Since(c.circuitOpen) >= circuitCooldown {
		c.circuitOpen = time.Time{}
		return true
	}
	return false
}

func (c *Client) tripCircuit(status int) {
	c.mu.Lock()
	first := c.circuitOpen.IsZero()
	if first {
		c.circuitOpen = time.Now()
	}
	c.mu.Unlock()
	if first {
		slog.Warn("opensubtitles circuit opened — pausing requests",
			"status", status, "cooldown", circuitCooldown.String())
	}
}

// ── transport ──────────────────────────────────────────────────────────────

func (c *Client) do(ctx context.Context, method, path string, params url.Values, body any, requireAuth bool, dest any) error {
	if requireAuth {
		if err := c.ensureLoggedIn(ctx); err != nil {
			return err
		}
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter: %w", err)
	}

	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	var req *http.Request
	var err error
	if body != nil {
		buf, mErr := json.Marshal(body)
		if mErr != nil {
			return fmt.Errorf("marshal body: %w", mErr)
		}
		req, err = http.NewRequestWithContext(ctx, method, u, strings.NewReader(string(buf)))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, method, u, nil)
	}
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Api-Key", c.apiKey)
	c.mu.Lock()
	tok := c.token
	c.mu.Unlock()
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		// Token may have expired mid-flight — drop it and let the next call re-login.
		c.mu.Lock()
		c.token = ""
		c.tokenExp = time.Time{}
		c.mu.Unlock()
		c.tripCircuit(resp.StatusCode)
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusNotAcceptable {
		c.tripCircuit(resp.StatusCode)
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	if dest == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
