// Package schedulesdirect is a slim client for the schedulesdirect.org
// JSON API (the "SD" service used by Plex, Emby, Jellyfin, MythTV).
// It covers the subset OnScreen needs to refresh EPG data: token auth,
// lineup discovery, station→schedule mapping, and program metadata.
//
// SD is a paid service ($35/yr) so this package never tries to be free
// — operators bring their own credentials. The client is per-process
// stateless aside from a cached token; production reuses the same
// *Client across calls.
//
// API reference: https://docs.schedulesdirect.org/
package schedulesdirect

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onscreen/onscreen/internal/safehttp"
)

const defaultBaseURL = "https://json.schedulesdirect.org/20141201"
const userAgent = "OnScreen/1.0 ( https://github.com/CollinJAycock/OnScreen )"

// Client talks to schedulesdirect.org. Token is lazily fetched on the
// first authenticated call and refreshed when the API returns 401.
type Client struct {
	http       *http.Client
	baseURL    string
	username   string
	passSHA1   string
	mu         sync.Mutex
	token      string
	tokenExpAt time.Time
}

// New constructs a client with a shared HTTP client (10 s per request).
// passwordSHA1 is the lowercased hex sha1 of the SD account password —
// SD's auth contract takes the hash, not the raw password.
//
// The HTTP client is wrapped in safehttp so dial-time connections to
// private / loopback / link-local addresses are refused — defense in
// depth against DNS rebinding for json.schedulesdirect.org.
func New(username, passwordSHA1 string) *Client {
	return &Client{
		http:     safehttp.NewClient(safehttp.DialPolicy{}, 10*time.Second),
		baseURL:  defaultBaseURL,
		username: username,
		passSHA1: strings.ToLower(passwordSHA1),
	}
}

// NewWithHTTPClient lets tests inject an httptest.Server-backed client.
func NewWithHTTPClient(username, passwordSHA1 string, hc *http.Client) *Client {
	return &Client{
		http:     hc,
		baseURL:  defaultBaseURL,
		username: username,
		passSHA1: strings.ToLower(passwordSHA1),
	}
}

// WithBaseURL overrides the API base — used by tests.
func (c *Client) WithBaseURL(u string) *Client {
	c.baseURL = strings.TrimRight(u, "/")
	return c
}

// HashPassword returns sha1(password) as lowercase hex — what SD wants
// at /token. Exposed so the API/admin layer can hash a user-entered
// password once at credential-save time and store the hash in the
// EPGSource config blob, never the raw password.
func HashPassword(plaintext string) string {
	sum := sha1.Sum([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ---- public API methods ----

// LineupResponse is the /lineups response shape — just what we use.
type LineupResponse struct {
	Lineups []Lineup `json:"lineups"`
}

// Lineup represents one SD lineup the account is subscribed to. Each
// lineup is a set of stations the user gets from a specific provider
// (e.g. an OTA antenna lineup for a zip code, or a cable subscription).
type Lineup struct {
	Lineup    string `json:"lineup"`    // e.g. "USA-OTA-90210"
	Name      string `json:"name"`      // human label
	Transport string `json:"transport"` // "Antenna", "Cable", "Satellite"
	Modified  string `json:"modified"`  // RFC3339 timestamp
}

// ListLineups returns the lineups subscribed by the account. Used by
// the admin UI to populate a "pick which lineups to import" picker
// after the user enters credentials.
func (c *Client) ListLineups(ctx context.Context) ([]Lineup, error) {
	var resp LineupResponse
	if err := c.do(ctx, http.MethodGet, "/lineups", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Lineups, nil
}

// LineupMap is the /lineups/{id} response: the channel↔station mapping
// the account's lineup uses. We need both the map and stations to
// build OnScreen Channel rows + EPG-program lookups.
type LineupMap struct {
	Map      []ChannelStation `json:"map"`
	Stations []Station        `json:"stations"`
	Metadata struct {
		Lineup     string `json:"lineup"`
		Modified   string `json:"modified"`
		Transport  string `json:"transport"`
	} `json:"metadata"`
}

// ChannelStation maps a tuner channel number to an SD station ID.
type ChannelStation struct {
	StationID string `json:"stationID"`
	Channel   string `json:"channel"`
	UHFVHF    int    `json:"uhfVhf,omitempty"`
}

// Station carries the station's display metadata.
type Station struct {
	StationID  string   `json:"stationID"`
	Name       string   `json:"name"`
	Callsign   string   `json:"callsign"`
	Affiliate  string   `json:"affiliate,omitempty"`
	BroadcastLanguage []string `json:"broadcastLanguage,omitempty"`
	Logo       *struct {
		URL    string `json:"URL"`
		Height int    `json:"height"`
		Width  int    `json:"width"`
	} `json:"logo,omitempty"`
}

// GetLineup fetches the channel/station mapping for one lineup.
func (c *Client) GetLineup(ctx context.Context, lineupID string) (*LineupMap, error) {
	var lm LineupMap
	if err := c.do(ctx, http.MethodGet, "/lineups/"+lineupID, nil, &lm); err != nil {
		return nil, err
	}
	return &lm, nil
}

// ScheduleRequest is one entry in the /schedules POST body. SD asks
// for a list of {stationID, dates[]} so a single request can pull
// schedules for many stations in many days.
type ScheduleRequest struct {
	StationID string   `json:"stationID"`
	Date      []string `json:"date"` // YYYY-MM-DD; empty means "all available"
}

// StationSchedule is the response per station — a list of program
// airings keyed by the station's program ID. Program details (titles,
// descriptions, art) come from the separate /programs endpoint.
type StationSchedule struct {
	StationID string    `json:"stationID"`
	Programs  []Airing  `json:"programs"`
	Metadata  struct {
		Modified string `json:"modified"`
		MD5      string `json:"md5"`
		StartDate string `json:"startDate"`
	} `json:"metadata"`
}

// Airing is one occurrence of a program on a station.
type Airing struct {
	ProgramID  string    `json:"programID"`     // e.g. "EP012345670001"
	AirDateTime time.Time `json:"airDateTime"`  // start, RFC3339
	Duration   int       `json:"duration"`      // seconds
	MD5        string    `json:"md5,omitempty"`
	IsNew      bool      `json:"new,omitempty"`
	LiveTapeDelay string `json:"liveTapeDelay,omitempty"`
	Ratings    []struct {
		Body string `json:"body"`
		Code string `json:"code"`
	} `json:"ratings,omitempty"`
}

// FetchSchedules POSTs the request batch and returns one
// StationSchedule per station in the request.
func (c *Client) FetchSchedules(ctx context.Context, req []ScheduleRequest) ([]StationSchedule, error) {
	var resp []StationSchedule
	if err := c.do(ctx, http.MethodPost, "/schedules", req, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ProgramRequest is the body for /programs — a flat list of program IDs.
type ProgramRequest []string

// Program is the rich metadata for one program ID.
type Program struct {
	ProgramID    string `json:"programID"`
	Titles       []struct {
		Title120 string `json:"title120"`
	} `json:"titles"`
	EpisodeTitle150 string `json:"episodeTitle150,omitempty"`
	Descriptions    struct {
		Description1000 []struct {
			DescriptionLanguage string `json:"descriptionLanguage"`
			Description         string `json:"description"`
		} `json:"description1000,omitempty"`
		Description100 []struct {
			DescriptionLanguage string `json:"descriptionLanguage"`
			Description         string `json:"description"`
		} `json:"description100,omitempty"`
	} `json:"descriptions"`
	OriginalAirDate string   `json:"originalAirDate,omitempty"`
	Genres          []string `json:"genres,omitempty"`
	Metadata        []struct {
		Tribune struct {
			Season  int `json:"season,omitempty"`
			Episode int `json:"episode,omitempty"`
		} `json:"Tribune,omitempty"`
		Gracenote struct {
			Season  int `json:"season,omitempty"`
			Episode int `json:"episode,omitempty"`
		} `json:"Gracenote,omitempty"`
	} `json:"metadata,omitempty"`
}

// FetchPrograms POSTs the program-ID list and returns full metadata.
// SD caps the request at 5000 IDs per call — caller batches.
func (c *Client) FetchPrograms(ctx context.Context, ids []string) ([]Program, error) {
	var resp []Program
	if err := c.do(ctx, http.MethodPost, "/programs", ids, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ---- internals ----

type tokenResponse struct {
	Token   string `json:"token"`
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

// authenticate hits POST /token and caches the result. Tokens are
// valid for 24 h per SD docs; we treat them as 23 h to leave a margin.
func (c *Client) authenticate(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExpAt) {
		return c.token, nil
	}

	body := map[string]string{
		"username": c.username,
		"password": c.passSHA1,
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/token", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("sd token: %w", err)
	}
	defer resp.Body.Close()

	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	var tr tokenResponse
	if err := json.Unmarshal(rawBody, &tr); err != nil {
		return "", fmt.Errorf("sd token decode (status %d): %w", resp.StatusCode, err)
	}
	if tr.Token == "" {
		return "", fmt.Errorf("sd token: %s (code %d)", tr.Message, tr.Code)
	}
	c.token = tr.Token
	c.tokenExpAt = time.Now().Add(23 * time.Hour)
	return c.token, nil
}

// do issues an authenticated request and decodes the JSON response.
// On 401 it transparently re-authenticates once and retries.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	if err := c.doOnce(ctx, method, path, body, out, false); err != nil {
		var ae *authError
		if errors.As(err, &ae) {
			// Token expired — invalidate cache and retry once.
			c.mu.Lock()
			c.token = ""
			c.tokenExpAt = time.Time{}
			c.mu.Unlock()
			return c.doOnce(ctx, method, path, body, out, true)
		}
		return err
	}
	return nil
}

type authError struct{ status int }

func (e *authError) Error() string { return fmt.Sprintf("sd auth: status %d", e.status) }

func (c *Client) doOnce(ctx context.Context, method, path string, body any, out any, retry bool) error {
	tok, err := c.authenticate(ctx)
	if err != nil {
		return err
	}

	var bodyReader io.Reader
	if body != nil {
		b, mErr := json.Marshal(body)
		if mErr != nil {
			return fmt.Errorf("sd marshal %s: %w", path, mErr)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Token", tok)
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sd %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && !retry {
		return &authError{status: resp.StatusCode}
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("sd %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if out == nil {
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)) // 64 MB cap
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("sd decode %s: %w", path, err)
	}
	return nil
}
