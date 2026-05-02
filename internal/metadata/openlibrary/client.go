// Package openlibrary resolves book cover URLs from OpenLibrary's free
// search + covers API. Used as the audiobook-side fallback when neither
// embedded m4b/mp3 art nor an on-disk cover.jpg exists in the book's
// directory.
//
// No API key, no rate-card. OpenLibrary asks consumers to set a
// descriptive User-Agent so they can contact us if a script
// misbehaves; the New() constructor wires one.
//
// Coverage is excellent for English-language and classic books;
// thinner for niche audiobook releases where the audio publisher
// repackaged a book under a slightly different title from the print
// edition. Caller is expected to fall through to a different source
// (or just leave the row poster-less) when this returns "".
package openlibrary

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultSearchURL = "https://openlibrary.org/search.json"
	coverBaseURL     = "https://covers.openlibrary.org/b/id"
	defaultUserAgent = "OnScreen/2.1 (https://github.com/onscreen/onscreen)"
)

// testSearchURL lets tests rewrite the endpoint to point at httptest.Server.
var testSearchURL string

// Client calls openlibrary.org/search.json.
type Client struct {
	http      *http.Client
	userAgent string
}

// New returns a Client with a 10 s timeout and the default User-Agent.
func New() *Client {
	return &Client{
		http:      &http.Client{Timeout: 10 * time.Second},
		userAgent: defaultUserAgent,
	}
}

// NewWithClient lets tests inject a custom *http.Client.
func NewWithClient(hc *http.Client) *Client {
	return &Client{http: hc, userAgent: defaultUserAgent}
}

func (c *Client) searchURL() string {
	if testSearchURL != "" {
		return testSearchURL
	}
	return defaultSearchURL
}

// searchResponse is the shape we care about from search.json. OpenLibrary
// returns many more fields per doc — we only need cover_i (the integer
// cover ID) which feeds covers.openlibrary.org/b/id/{N}-L.jpg.
type searchResponse struct {
	Docs []struct {
		CoverI       int      `json:"cover_i"`
		Title        string   `json:"title"`
		AuthorName   []string `json:"author_name"`
		FirstPublish int      `json:"first_publish_year"`
	} `json:"docs"`
}

// SearchBookCoverURL searches OpenLibrary by book title (and optional
// author name) and returns the L-size cover URL of the first result
// that has a cover_i set. Returns "" with no error when:
//   - the search has no docs
//   - none of the matched docs have a cover_i (uncovered book)
//   - title is empty
//
// The author parameter is best-effort: when set, it's added as an `author=`
// query param that OpenLibrary uses as an additional rank signal. Title
// alone usually suffices for famous books; authors disambiguate
// "The Hobbit" (Tolkien vs. some other Hobbit-titled work).
func (c *Client) SearchBookCoverURL(ctx context.Context, title, author string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", nil
	}

	q := url.Values{}
	q.Set("title", title)
	if a := strings.TrimSpace(author); a != "" {
		q.Set("author", a)
	}
	q.Set("limit", "5") // first hit-with-cover wins; 5 is enough headroom

	u := c.searchURL() + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("build OpenLibrary request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("OpenLibrary GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenLibrary status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return "", fmt.Errorf("read OpenLibrary body: %w", err)
	}

	var sr searchResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return "", fmt.Errorf("parse OpenLibrary JSON: %w", err)
	}
	for _, d := range sr.Docs {
		if d.CoverI > 0 {
			return fmt.Sprintf("%s/%d-L.jpg", coverBaseURL, d.CoverI), nil
		}
	}
	return "", nil
}
