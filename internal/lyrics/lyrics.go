// Package lyrics fetches lyrics for music tracks. The primary path is
// tag-based (ID3/Vorbis comments from the file itself, populated by the
// scanner); when tags are empty the Fetcher falls back to LRCLIB — the
// largest free synced-lyrics service.
//
// No Musixmatch, Genius, etc. — those require paid API keys. LRCLIB is
// community-sourced and covers enough of the long tail to be credible
// as an "audiophile-grade" lyrics story without asking users to wire up
// a third-party key.
package lyrics

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

// Result holds lyrics for a single track in both plain and synced forms.
// Either (or both) may be empty. Synced format is LRC (time-tagged
// lines).
type Result struct {
	Plain  string
	Synced string
}

// IsEmpty returns true when neither form is populated.
func (r Result) IsEmpty() bool { return r.Plain == "" && r.Synced == "" }

// LookupParams identifies a track for LRCLIB's /get endpoint.
type LookupParams struct {
	Artist    string
	Track     string
	Album     string
	DurationS int // track duration in seconds; LRCLIB uses ±2s match window
}

// ErrNotFound is returned by the LRCLIB client when the track has no
// entry in the community database.
var ErrNotFound = errors.New("lyrics: not found")

// Fetcher is the external lyrics lookup interface. Split from any
// caching or persistence layer so the cache adapter can wrap it.
type Fetcher interface {
	Lookup(ctx context.Context, p LookupParams) (Result, error)
}

// LRCLIBClient is the default Fetcher. No auth needed; the service is
// free + anonymous. Rate limit per their docs is "reasonable," which we
// respect by not hammering on startup.
type LRCLIBClient struct {
	baseURL string
	http    *http.Client
}

// NewLRCLIBClient returns a fetcher pointed at lrclib.net. baseURL is
// injected so tests can substitute an httptest server.
func NewLRCLIBClient() *LRCLIBClient {
	return &LRCLIBClient{
		baseURL: "https://lrclib.net",
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// WithBaseURL overrides the base URL — for tests.
func (c *LRCLIBClient) WithBaseURL(u string) *LRCLIBClient {
	c.baseURL = strings.TrimRight(u, "/")
	return c
}

// lrclibResponse mirrors the subset of LRCLIB's /api/get JSON the
// fetcher consumes. Other fields (trackName, artistName echoed back,
// etc.) are ignored — we already have those on the client side.
type lrclibResponse struct {
	ID           int    `json:"id"`
	Instrumental bool   `json:"instrumental"`
	PlainLyrics  string `json:"plainLyrics"`
	SyncedLyrics string `json:"syncedLyrics"`
}

// Lookup queries LRCLIB for the given track. Missing entries return
// ErrNotFound (not a zero-value Result, so callers can distinguish
// "known-absent" from "haven't tried yet").
func (c *LRCLIBClient) Lookup(ctx context.Context, p LookupParams) (Result, error) {
	if p.Artist == "" || p.Track == "" {
		return Result{}, fmt.Errorf("lyrics: artist + track required")
	}
	q := url.Values{}
	q.Set("artist_name", p.Artist)
	q.Set("track_name", p.Track)
	if p.Album != "" {
		q.Set("album_name", p.Album)
	}
	if p.DurationS > 0 {
		q.Set("duration", fmt.Sprintf("%d", p.DurationS))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/get?"+q.Encode(), nil)
	if err != nil {
		return Result{}, err
	}
	// LRCLIB asks users to identify themselves so abuse can be traced.
	// Include a UA with a link the maintainers can hit back on.
	req.Header.Set("User-Agent", "OnScreen/1.0 (https://github.com/CollinJAycock/OnScreen)")
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Result{}, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Result{}, fmt.Errorf("lyrics lookup: status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var r lrclibResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return Result{}, fmt.Errorf("lyrics decode: %w", err)
	}
	if r.Instrumental {
		// Don't return an empty result as success; callers should treat
		// instrumental tracks as "known-absent" via ErrNotFound so they
		// don't keep retrying the network hit.
		return Result{}, ErrNotFound
	}
	return Result{Plain: r.PlainLyrics, Synced: r.SyncedLyrics}, nil
}
