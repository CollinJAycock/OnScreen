// Package wikipedia resolves portrait/thumbnail URLs from Wikipedia's
// REST API summary endpoint. Used by the audiobook scanner as the
// author-photo source when no embedded m4b/mp3 picture or on-disk
// cover.jpg exists in the author's directory.
//
// Why Wikipedia (vs. Wikidata or OpenLibrary's author photos):
//   - The REST summary endpoint returns ready-to-display thumbnails
//     directly (no second hop to Commons, no SPARQL query); a single
//     GET resolves both "is there an article?" and "what's the lead
//     image?".
//   - Coverage of public-figure authors is excellent — Tolkien,
//     Sanderson, Fitzgerald, Orwell all have articles with portraits.
//     OpenLibrary's author photos are spottier and often outdated.
//   - The summary endpoint follows article redirects, so calling it
//     with "Brandon Sanderson" lands on the canonical title even when
//     the URL slug differs.
//
// Caveats:
//   - Disambiguation pages return type="disambiguation" with no
//     thumbnail. We treat that as "no result" rather than picking a
//     branch — narrower coverage is safer than picking the wrong
//     "John Smith".
//   - The image returned is whatever Wikipedia editors picked as the
//     lead image. For some authors that may not be a portrait
//     (occasionally a book cover or a co-photo); for the audiobook
//     library use case that's still a recognisable tile.
//   - All Wikipedia images are CC / public-domain / fair use under
//     Wikimedia's terms; safe to cache and redistribute internally.
package wikipedia

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onscreen/onscreen/internal/safehttp"
)

const (
	defaultBaseURL   = "https://en.wikipedia.org/api/rest_v1"
	defaultUserAgent = "OnScreen/2.1 (https://github.com/onscreen/onscreen)"
)

// testBaseURL lets tests rewrite the endpoint to point at httptest.Server.
var testBaseURL string

// Client calls the Wikipedia REST API.
type Client struct {
	http      *http.Client
	userAgent string
}

// New returns a Client with a 10 s timeout and the default User-Agent.
// The HTTP client is wrapped in safehttp so the dialer rejects
// post-resolution loopback / RFC1918 / link-local addresses — defense
// in depth against DNS rebinding for every public-API client.
func New() *Client {
	return &Client{
		http:      safehttp.NewClient(safehttp.DialPolicy{}, 10*time.Second),
		userAgent: defaultUserAgent,
	}
}

// NewWithClient lets tests inject a custom *http.Client.
func NewWithClient(hc *http.Client) *Client {
	return &Client{http: hc, userAgent: defaultUserAgent}
}

func (c *Client) baseURL() string {
	if testBaseURL != "" {
		return testBaseURL
	}
	return defaultBaseURL
}

// summaryResponse is the slice of the REST summary endpoint we care
// about. The endpoint returns the page title, type (article /
// disambiguation / etc.), and `originalimage` / `thumbnail` URL pairs
// when the article has a lead image. We always pick `originalimage`
// (full-resolution) over `thumbnail` (~320px) — the artwork pipeline
// resizes on demand; storing the full image avoids re-fetching at
// higher resolution later.
type summaryResponse struct {
	Type          string `json:"type"`
	Title         string `json:"title"`
	OriginalImage struct {
		Source string `json:"source"`
	} `json:"originalimage"`
	Thumbnail struct {
		Source string `json:"source"`
	} `json:"thumbnail"`
}

// GetThumbnailURL returns the full-resolution lead image URL for the
// Wikipedia article matching `name`. Returns "" with no error when:
//   - name is empty (caller should skip)
//   - the page doesn't exist (404)
//   - the page is a disambiguation page
//   - the article has no lead image (rare for biographical entries)
//
// Names with spaces are URL-encoded; the REST endpoint follows article
// redirects so "Brandon Sanderson" lands on the canonical title even
// when the URL slug differs.
func (c *Client) GetThumbnailURL(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}

	// Wikipedia titles use spaces, but the REST API expects underscore-
	// separated paths. PathEscape handles the rest of the URL-unsafe
	// characters (apostrophes, accented characters, etc.).
	slug := url.PathEscape(strings.ReplaceAll(name, " ", "_"))
	u := fmt.Sprintf("%s/page/summary/%s", c.baseURL(), slug)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("build Wikipedia request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("Wikipedia GET: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotFound:
		return "", nil // article doesn't exist; caller falls through
	case http.StatusOK:
	default:
		return "", fmt.Errorf("Wikipedia status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return "", fmt.Errorf("read Wikipedia body: %w", err)
	}
	var sr summaryResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return "", fmt.Errorf("parse Wikipedia JSON: %w", err)
	}
	if sr.Type == "disambiguation" {
		// Refusing to guess between branches; the operator can drop a
		// folder.jpg or set the author poster manually for ambiguous
		// names like "John Smith".
		return "", nil
	}
	if sr.OriginalImage.Source != "" {
		return sr.OriginalImage.Source, nil
	}
	if sr.Thumbnail.Source != "" {
		return sr.Thumbnail.Source, nil
	}
	return "", nil
}
