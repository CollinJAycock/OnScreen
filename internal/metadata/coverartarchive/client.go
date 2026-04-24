// Package coverartarchive resolves Cover Art Archive front-cover URLs
// from MusicBrainz release IDs. CAA is the canonical fallback when
// TheAudioDB doesn't have a cover for an album — particularly helpful
// for indie / classical / non-mainstream releases that MusicBrainz
// curates thoroughly but TheAudioDB doesn't track.
//
// No API key is required and there's no rate limit of note (a few
// hundred req/s is fine per their docs). The /front.jpg endpoint
// 307-redirects to a Wikimedia-hosted image — callers only need the
// final URL, not the image bytes, because the enricher's
// ArtworkFetcher handles the actual download.
package coverartarchive

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

const defaultBaseURL = "https://coverartarchive.org"

// testBaseURL lets tests rewrite the endpoint for httptest.Server.
// Nil in production.
var testBaseURL string

// Client calls the Cover Art Archive API.
type Client struct {
	http *http.Client
}

// New returns a Client using a shared HTTP client with a 10 s timeout.
func New() *Client {
	return &Client{
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

// NewWithClient lets tests inject a custom *http.Client (typically
// pointed at an httptest.Server).
func NewWithClient(hc *http.Client) *Client {
	return &Client{http: hc}
}

func (c *Client) baseURL() string {
	if testBaseURL != "" {
		return testBaseURL
	}
	return defaultBaseURL
}

// FrontCoverURL resolves the front cover for a MusicBrainz release.
// Prefers the release-level endpoint (exact cover for the specific
// pressing) and falls back to the release-group endpoint (a
// representative cover across all releases in the group) when the
// release-specific lookup 404s. Returns "" when neither lookup finds
// a cover — callers treat that as "no fallback available."
//
// Only one ID needs to be populated; pass uuid.Nil for whichever
// you don't have.
func (c *Client) FrontCoverURL(ctx context.Context, releaseID, releaseGroupID uuid.UUID) (string, error) {
	if releaseID != uuid.Nil {
		if url, err := c.lookup(ctx, "release", releaseID); err != nil {
			return "", err
		} else if url != "" {
			return url, nil
		}
	}
	if releaseGroupID != uuid.Nil {
		if url, err := c.lookup(ctx, "release-group", releaseGroupID); err != nil {
			return "", err
		} else if url != "" {
			return url, nil
		}
	}
	return "", nil
}

// lookup issues a HEAD against {base}/{kind}/{mbid}/front — CAA
// responds with a 307 redirect whose Location header points at the
// actual image (typically on ia800*.us.archive.org or commons.wikimedia).
// We don't follow the redirect here because the enricher's artwork
// fetcher already handles that, and this keeps lookup() cheap (one
// round-trip, no body transfer).
//
// A 404 means no cover exists for that MBID — returns "" with no
// error so the caller can fall through to the next lookup.
func (c *Client) lookup(ctx context.Context, kind string, mbid uuid.UUID) (string, error) {
	u := fmt.Sprintf("%s/%s/%s/front", c.baseURL(), kind, mbid.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return "", fmt.Errorf("build CAA request: %w", err)
	}

	// Don't auto-follow; we want the 307 so we can read the Location.
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("CAA HEAD %s: %w", u, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotFound:
		return "", nil // no cover for this MBID, try the next
	case http.StatusMovedPermanently, http.StatusFound,
		http.StatusSeeOther, http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		loc := resp.Header.Get("Location")
		if loc == "" {
			return "", fmt.Errorf("CAA redirect missing Location header")
		}
		return loc, nil
	case http.StatusOK:
		// CAA sometimes serves the image directly at the /front
		// endpoint without a redirect — treat the original URL as
		// the image URL in that case.
		return u, nil
	default:
		return "", fmt.Errorf("CAA status %d for %s", resp.StatusCode, u)
	}
}
