// Package audiodb implements the metadata.MusicAgent interface using
// TheAudioDB free API (https://www.theaudiodb.com). No API key is required
// for the free tier — requests use the public key "2".
package audiodb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/safehttp"
)

const baseURL = "https://www.theaudiodb.com/api/v1/json/2"

// Client queries TheAudioDB's free public API.
type Client struct {
	http *http.Client
}

// New returns a TheAudioDB client. The underlying HTTP client is
// wrapped in safehttp so dial-time connections to private / loopback /
// link-local addresses are refused, the same posture every other
// public-API client uses.
func New() *Client {
	return &Client{
		http: safehttp.NewClient(safehttp.DialPolicy{}, 10*time.Second),
	}
}

// SearchArtist implements metadata.MusicAgent.
func (c *Client) SearchArtist(ctx context.Context, name string) (*metadata.ArtistResult, error) {
	var resp struct {
		Artists []struct {
			Name      string `json:"strArtist"`
			Thumb     string `json:"strArtistThumb"`
			Fanart    string `json:"strArtistFanart"`
			Biography string `json:"strBiographyEN"`
		} `json:"artists"`
	}
	if err := c.get(ctx, "/search.php?s="+url.QueryEscape(name), &resp); err != nil {
		return nil, err
	}
	if len(resp.Artists) == 0 {
		return nil, nil
	}
	a := resp.Artists[0]
	return &metadata.ArtistResult{
		Name:      a.Name,
		ThumbURL:  a.Thumb,
		FanartURL: a.Fanart,
		Biography: strings.TrimSpace(a.Biography),
	}, nil
}

// SearchAlbum implements metadata.MusicAgent.
func (c *Client) SearchAlbum(ctx context.Context, artistName, albumName string) (*metadata.AlbumResult, error) {
	q := "/searchalbum.php?s=" + url.QueryEscape(artistName) + "&a=" + url.QueryEscape(albumName)
	var resp struct {
		Albums []struct {
			Name        string `json:"strAlbum"`
			Thumb       string `json:"strAlbumThumb"`
			Description string `json:"strDescriptionEN"`
			Year        string `json:"intYearReleased"`
			Genre       string `json:"strGenre"`
		} `json:"album"`
	}
	if err := c.get(ctx, q, &resp); err != nil {
		return nil, err
	}
	if len(resp.Albums) == 0 {
		return nil, nil
	}
	a := resp.Albums[0]
	year, _ := strconv.Atoi(strings.TrimSpace(a.Year))
	var genres []string
	if g := strings.TrimSpace(a.Genre); g != "" {
		genres = []string{g}
	}
	return &metadata.AlbumResult{
		Name:        a.Name,
		ThumbURL:    a.Thumb,
		Description: strings.TrimSpace(a.Description),
		Year:        year,
		Genres:      genres,
	}, nil
}

func (c *Client) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("audiodb: HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}
