// Package lrclib resolves song lyrics from lrclib.net — a free,
// MIT-licensed, community-curated lyrics database that serves both
// plain and synced (LRC-format) lyrics with no API key required.
//
// The /get endpoint is the primary lookup: four params (track, artist,
// album, duration) that must all match an entry, with duration
// tolerance ±2s per the lrclib API docs. /search is the fuzzy
// fallback when the exact lookup misses — often because the track
// tag title doesn't match the lyrics DB's canonical spelling.
//
// Cache the result server-side; this package exists to fetch on
// demand, not to be called on every playback.
package lrclib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://lrclib.net/api"
const userAgent = "OnScreen/1.0 ( https://github.com/CollinJAycock/OnScreen )"

var testBaseURL string // overridden in tests

// Result carries the lyric data for a single track. Either PlainLyrics
// or SyncedLyrics (or both) will be set on success — callers prefer
// synced when present and fall back to plain for display.
type Result struct {
	TrackName    string
	ArtistName   string
	AlbumName    string
	DurationSec  float64
	PlainLyrics  string // unsynced, newline-separated
	SyncedLyrics string // LRC format: [mm:ss.xx]line
	Instrumental bool   // true when lrclib flags the track as instrumental
}

// Client calls the lrclib.net API.
type Client struct {
	http *http.Client
}

// New returns a Client with a 10 s HTTP timeout.
func New() *Client {
	return &Client{http: &http.Client{Timeout: 10 * time.Second}}
}

// NewWithClient lets tests inject a custom *http.Client.
func NewWithClient(hc *http.Client) *Client { return &Client{http: hc} }

func (c *Client) baseURL() string {
	if testBaseURL != "" {
		return testBaseURL
	}
	return defaultBaseURL
}

// Query is the set of fields used for /get and /search. Empty fields
// are dropped from the request; track + artist are typically the
// minimum a useful match needs.
type Query struct {
	Track    string
	Artist   string
	Album    string
	Duration float64 // seconds; 0 = don't constrain
}

// Get does an exact lookup (/api/get). Returns (nil, nil) when lrclib
// has no entry for these parameters — callers treat that as "miss,
// try /search" rather than an error.
func (c *Client) Get(ctx context.Context, q Query) (*Result, error) {
	if q.Track == "" || q.Artist == "" {
		return nil, errors.New("lrclib: Get requires at least Track + Artist")
	}
	v := url.Values{}
	v.Set("track_name", q.Track)
	v.Set("artist_name", q.Artist)
	if q.Album != "" {
		v.Set("album_name", q.Album)
	}
	if q.Duration > 0 {
		v.Set("duration", fmt.Sprintf("%.0f", q.Duration))
	}

	return c.do(ctx, "/get?"+v.Encode())
}

// Search is the fuzzy fallback (/api/search). Returns the top match
// per lrclib's ranking — the API returns an ordered array and we
// take the first element. (nil, nil) on empty result set.
func (c *Client) Search(ctx context.Context, q Query) (*Result, error) {
	if q.Track == "" && q.Artist == "" {
		return nil, errors.New("lrclib: Search requires Track or Artist")
	}
	v := url.Values{}
	if q.Track != "" {
		v.Set("track_name", q.Track)
	}
	if q.Artist != "" {
		v.Set("artist_name", q.Artist)
	}
	if q.Album != "" {
		v.Set("album_name", q.Album)
	}

	body, err := c.doRaw(ctx, "/search?"+v.Encode())
	if err != nil {
		return nil, err
	}
	if body == nil {
		return nil, nil
	}
	var arr []apiResult
	if err := json.Unmarshal(body, &arr); err != nil {
		return nil, fmt.Errorf("lrclib: decode search: %w", err)
	}
	if len(arr) == 0 {
		return nil, nil
	}
	r := arr[0].toResult()
	return &r, nil
}

// Resolve tries /get first (exact) then /search (fuzzy) so most callers
// can just call this one method. Returns (nil, nil) when neither path
// finds lyrics — MusicBrainz-grade libraries rarely hit that, but
// obscure / recent releases do.
func (c *Client) Resolve(ctx context.Context, q Query) (*Result, error) {
	if r, err := c.Get(ctx, q); err != nil {
		return nil, err
	} else if r != nil {
		return r, nil
	}
	return c.Search(ctx, q)
}

// ---- internals ----

type apiResult struct {
	ID           int64   `json:"id"`
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	AlbumName    string  `json:"albumName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

func (a apiResult) toResult() Result {
	return Result{
		TrackName:    a.TrackName,
		ArtistName:   a.ArtistName,
		AlbumName:    a.AlbumName,
		DurationSec:  a.Duration,
		PlainLyrics:  strings.TrimSpace(a.PlainLyrics),
		SyncedLyrics: strings.TrimSpace(a.SyncedLyrics),
		Instrumental: a.Instrumental,
	}
}

// do GETs path and decodes the JSON body into a single Result.
func (c *Client) do(ctx context.Context, path string) (*Result, error) {
	body, err := c.doRaw(ctx, path)
	if err != nil {
		return nil, err
	}
	if body == nil {
		return nil, nil
	}
	var a apiResult
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("lrclib: decode: %w", err)
	}
	r := a.toResult()
	return &r, nil
}

// doRaw returns the raw body bytes, or (nil, nil) on 404. Split from
// do() because /search returns an array while /get returns an object.
func (c *Client) doRaw(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+path, nil)
	if err != nil {
		return nil, fmt.Errorf("lrclib: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lrclib: GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lrclib: status %d for %s", resp.StatusCode, path)
	}
	// Cap at 1 MB — longest real lyrics are a few KB; anything larger
	// is a misbehaving proxy or a runaway response.
	const maxBody = 1 << 20
	out, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("lrclib: read body: %w", err)
	}
	return out, nil
}
