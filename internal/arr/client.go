// Package arr provides outbound HTTP clients for Radarr and Sonarr — the two
// "*arr" applications OnScreen brokers download requests to. Both expose the
// same v3 REST contract (auth via X-Api-Key header, JSON bodies), so a small
// shared client handles transport while the kind-specific files (radarr.go,
// sonarr.go) implement the operations OnScreen actually invokes: lookup,
// add, list quality profiles / root folders / tags, and a system-status
// "ping" used by the admin "test connection" button.
package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onscreen/onscreen/internal/safehttp"
)

// Errors returned across the package.
var (
	// ErrNotFound is returned when an upstream lookup yields no matches.
	ErrNotFound = errors.New("arr: not found")
	// ErrUnauthorized signals a bad API key or disabled instance.
	ErrUnauthorized = errors.New("arr: unauthorized")
	// ErrConflict signals the title is already managed by the instance.
	// Callers should treat this as success — the title was already added.
	ErrConflict = errors.New("arr: already exists")
)

// Client is a transport wrapper used by both Radarr and Sonarr clients. It is
// safe for concurrent use; the underlying http.Client provides the connection
// pool. BaseURL must not contain a trailing slash.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// New constructs a Client with a sensible default timeout. baseURL is trimmed
// of trailing slashes so callers can pass either form.
//
// The HTTP client uses the safehttp LocalDevice policy: it allows RFC1918,
// loopback, and link-local addresses because Radarr/Sonarr typically run on
// the operator's LAN or in a sibling Docker container. Multicast / unspecified
// addresses are still refused — those aren't valid arr targets and would
// otherwise be free SSRF surface for an admin-level config bug.
func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTPClient: safehttp.NewClient(safehttp.DialPolicy{
			AllowPrivate:   true,
			AllowLoopback:  true,
			AllowLinkLocal: true,
		}, 15*time.Second),
	}
}

// do issues a request against the arr instance, applying X-Api-Key auth and
// decoding the response body into out (if non-nil). Non-2xx responses are
// translated to typed errors so handlers can branch on ErrUnauthorized / etc.
// without sniffing strings.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	full := c.BaseURL + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("arr: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, full, bodyReader)
	if err != nil {
		return fmt.Errorf("arr: new request: %w", err)
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("arr: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusConflict:
		return ErrConflict
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode >= 400:
		// Drain a bounded snippet of the body so the caller can log what arr
		// actually said (validation errors are returned as JSON arrays here).
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("arr: %s %s: %d %s: %s",
			method, path, resp.StatusCode, http.StatusText(resp.StatusCode),
			strings.TrimSpace(string(snippet)))
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// SystemStatus mirrors the subset of /api/v3/system/status that OnScreen uses
// to verify connectivity and surface the upstream version in the admin UI.
type SystemStatus struct {
	AppName    string `json:"appName"`
	Version    string `json:"version"`
	InstanceID string `json:"instanceName"`
}

// Ping calls /api/v3/system/status. A successful response is the canonical
// "the instance is reachable and the API key works" check.
func (c *Client) Ping(ctx context.Context) (*SystemStatus, error) {
	var s SystemStatus
	if err := c.do(ctx, http.MethodGet, "/api/v3/system/status", nil, nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// QualityProfile is the shared shape returned by /api/v3/qualityprofile on
// both Radarr and Sonarr.
type QualityProfile struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
}

// QualityProfiles fetches the available quality profiles. Used by the admin
// UI to populate the "default profile" dropdown when configuring an arr
// instance.
func (c *Client) QualityProfiles(ctx context.Context) ([]QualityProfile, error) {
	var out []QualityProfile
	if err := c.do(ctx, http.MethodGet, "/api/v3/qualityprofile", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RootFolder is the shared shape returned by /api/v3/rootfolder.
type RootFolder struct {
	ID        int32  `json:"id"`
	Path      string `json:"path"`
	Freespace int64  `json:"freeSpace"`
}

// RootFolders fetches configured download targets.
func (c *Client) RootFolders(ctx context.Context) ([]RootFolder, error) {
	var out []RootFolder
	if err := c.do(ctx, http.MethodGet, "/api/v3/rootfolder", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Tag mirrors /api/v3/tag.
type Tag struct {
	ID    int32  `json:"id"`
	Label string `json:"label"`
}

// Tags fetches the tag list so admins can pre-select tags to apply to
// requested items (e.g. "from-onscreen").
func (c *Client) Tags(ctx context.Context) ([]Tag, error) {
	var out []Tag
	if err := c.do(ctx, http.MethodGet, "/api/v3/tag", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
