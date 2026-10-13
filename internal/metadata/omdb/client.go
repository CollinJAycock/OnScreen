// Package omdb reads a title's IMDb, Rotten Tomatoes (critics) and
// Metacritic scores from the OMDb API (omdbapi.com) by IMDb ID. A free key
// allows 1000 requests a day.
package omdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onscreen/onscreen/internal/safehttp"
)

// DefaultBaseURL is OMDb's API endpoint.
const DefaultBaseURL = "https://www.omdbapi.com/"

// maxBody bounds one response; a title's record is a few KB.
const maxBody = 1 << 20

var (
	// ErrNotFound: OMDb has no title with this IMDb ID.
	ErrNotFound = errors.New("omdb: title not found")
	// ErrInvalidKey: OMDb refused the API key.
	ErrInvalidKey = errors.New("omdb: invalid API key")
	// ErrLimitReached: the key's daily request limit is used up.
	ErrLimitReached = errors.New("omdb: daily request limit reached")
)

// Ratings are a title's scores. nil = OMDb doesn't have that score.
type Ratings struct {
	IMDb           *float64 // 0-10
	IMDbVotes      *int32
	RottenTomatoes *int16 // critics' Tomatometer, 0-100
	Metacritic     *int16 // 0-100
}

// Client reads OMDb.
type Client struct {
	apiKey  string
	baseURL string
	hc      *http.Client
}

// New returns a client for apiKey. OMDb is a public host, so the default
// SSRF policy (public addresses only) applies.
func New(apiKey string) *Client {
	return &Client{apiKey: apiKey, baseURL: DefaultBaseURL, hc: safehttp.NewClient(safehttp.DialPolicy{}, 15*time.Second)}
}

// WithBaseURL points the client elsewhere (tests).
func (c *Client) WithBaseURL(u string, hc *http.Client) *Client {
	c.baseURL, c.hc = u, hc
	return c
}

type response struct {
	Response   string `json:"Response"`
	Error      string `json:"Error"`
	IMDbRating string `json:"imdbRating"`
	IMDbVotes  string `json:"imdbVotes"`
	Metascore  string `json:"Metascore"`
	Ratings    []struct {
		Source string `json:"Source"`
		Value  string `json:"Value"`
	} `json:"Ratings"`
}

// Get returns the scores for an IMDb ID ("tt0111161").
func (c *Client) Get(ctx context.Context, imdbID string) (Ratings, error) {
	q := url.Values{"i": {imdbID}, "apikey": {c.apiKey}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"?"+q.Encode(), nil)
	if err != nil {
		return Ratings{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		// The URL carries the key: report the error without it.
		if uerr, ok := errors.AsType[*url.Error](err); ok {
			err = uerr.Err
		}
		return Ratings{}, fmt.Errorf("omdb: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Ratings{}, fmt.Errorf("omdb: read: %w", err)
	}
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		if resp.StatusCode == http.StatusUnauthorized {
			return Ratings{}, ErrInvalidKey
		}
		return Ratings{}, fmt.Errorf("omdb: %s: unexpected reply", resp.Status)
	}
	if r.Response != "True" {
		return Ratings{}, classify(r.Error, resp.StatusCode)
	}
	return parse(r), nil
}

// classify maps OMDb's error message ("Movie not found!", "Invalid API
// key!", "Request limit reached!") to an error.
func classify(msg string, status int) error {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "not found") || strings.Contains(m, "incorrect imdb id"):
		return ErrNotFound
	case strings.Contains(m, "limit"):
		return ErrLimitReached
	case strings.Contains(m, "api key") || status == http.StatusUnauthorized:
		return ErrInvalidKey
	}
	return fmt.Errorf("omdb: %s", msg)
}

func parse(r response) Ratings {
	var out Ratings
	if f, err := strconv.ParseFloat(r.IMDbRating, 64); err == nil && f >= 0 && f <= 10 {
		out.IMDb = &f
	}
	if n, err := strconv.ParseInt(strings.ReplaceAll(r.IMDbVotes, ",", ""), 10, 32); err == nil && n >= 0 {
		v := int32(n)
		out.IMDbVotes = &v
	}
	for _, rt := range r.Ratings {
		switch rt.Source {
		case "Rotten Tomatoes":
			out.RottenTomatoes = percent(strings.TrimSuffix(rt.Value, "%"))
		case "Metacritic":
			out.Metacritic = percent(strings.TrimSuffix(rt.Value, "/100"))
		}
	}
	if out.Metacritic == nil {
		out.Metacritic = percent(r.Metascore)
	}
	return out
}

// percent parses a 0-100 score; "N/A" and anything else is nil.
func percent(s string) *int16 {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 || n > 100 {
		return nil
	}
	v := int16(n)
	return &v
}
