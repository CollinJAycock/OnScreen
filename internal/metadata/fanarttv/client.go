// Package fanarttv reads clear logos, banners and disc art from fanart.tv
// (API v3): movies by TMDB or IMDb id, TV shows by TheTVDB id, music artists
// by MusicBrainz artist id and albums by MusicBrainz release-group id. It
// needs a personal API key (free, from fanart.tv).
//
// Every image list is ranked the same way: the library's language first,
// then English, then language-neutral art ("00" or none), then anything;
// within a language, the most liked image wins. A title fanart.tv doesn't
// know (404) is an empty result, not an error, and both answers are cached
// in-process for a while so a season cascade or a repeated refresh doesn't
// ask again.
package fanarttv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/safehttp"
)

// DefaultBaseURL is fanart.tv's API v3 endpoint.
const DefaultBaseURL = "https://webservice.fanart.tv/v3/"

// maxBody bounds one response; a busy title's image list is tens of KB.
const maxBody = 2 << 20

// cacheTTL is how long an answer (images or "none") is reused. Art on
// fanart.tv changes slowly; the TTL bounds staleness on a long-running server.
const cacheTTL = 6 * time.Hour

// ErrInvalidKey means fanart.tv refused the API key.
var ErrInvalidKey = errors.New("fanarttv: invalid API key")

// Client reads fanart.tv.
type Client struct {
	apiKey  string
	baseURL string
	hc      *http.Client
	now     func() time.Time

	sf    singleflight.Group
	mu    sync.Mutex
	cache map[string]cacheEntry // key = request path
}

// cacheEntry is a decoded answer: just the chosen URLs (empty = none), so a
// large library's worth of lookups stays small.
type cacheEntry struct {
	art     metadata.ExtraArt
	fetched time.Time
}

// maxCacheEntries bounds the cache; past it, expired entries are dropped.
const maxCacheEntries = 20000

// New returns a client for apiKey. fanart.tv is a public host, so the default
// SSRF policy (public addresses only) applies.
func New(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: DefaultBaseURL,
		hc:      safehttp.NewClient(safehttp.DialPolicy{}, 15*time.Second),
		now:     time.Now,
		cache:   map[string]cacheEntry{},
	}
}

// WithBaseURL points the client elsewhere (tests).
func (c *Client) WithBaseURL(u string, hc *http.Client) *Client {
	c.baseURL, c.hc = u, hc
	return c
}

// image is one entry of a fanart.tv image list. likes is a string in
// fanart.tv's JSON.
type image struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Lang  string `json:"lang"`
	Likes string `json:"likes"`
}

type movieResponse struct {
	HDMovieLogo []image `json:"hdmovielogo"`
	MovieLogo   []image `json:"movielogo"`
	MovieBanner []image `json:"moviebanner"`
	MovieDisc   []image `json:"moviedisc"`
}

type tvResponse struct {
	HDTVLogo  []image `json:"hdtvlogo"`
	ClearLogo []image `json:"clearlogo"`
	TVBanner  []image `json:"tvbanner"`
}

type artistResponse struct {
	HDMusicLogo []image `json:"hdmusiclogo"`
	MusicLogo   []image `json:"musiclogo"`
	MusicBanner []image `json:"musicbanner"`
}

type albumResponse struct {
	Albums map[string]struct {
		CDArt []image `json:"cdart"`
	} `json:"albums"`
}

// MovieArt returns a movie's clear logo (HD first, then the older SD logo),
// banner and disc art. tmdbID is used when set, else imdbID; neither = none.
func (c *Client) MovieArt(ctx context.Context, tmdbID int, imdbID, lang string) (metadata.ExtraArt, error) {
	id := ""
	switch {
	case tmdbID > 0:
		id = strconv.Itoa(tmdbID)
	case strings.HasPrefix(imdbID, "tt"):
		id = imdbID
	default:
		return metadata.ExtraArt{}, nil
	}
	return c.lookup(ctx, "movies/"+url.PathEscape(id), lang, func(body []byte) (metadata.ExtraArt, error) {
		var r movieResponse
		if err := json.Unmarshal(body, &r); err != nil {
			return metadata.ExtraArt{}, err
		}
		return metadata.ExtraArt{
			LogoURL:   firstNonEmpty(pick(r.HDMovieLogo, lang), pick(r.MovieLogo, lang)),
			BannerURL: pick(r.MovieBanner, lang),
			DiscURL:   pick(r.MovieDisc, lang),
		}, nil
	})
}

// ShowArt returns a TV show's clear logo (HD first, then SD) and banner.
// fanart.tv keys shows by TheTVDB id; 0 = none.
func (c *Client) ShowArt(ctx context.Context, tvdbID int, lang string) (metadata.ExtraArt, error) {
	if tvdbID <= 0 {
		return metadata.ExtraArt{}, nil
	}
	return c.lookup(ctx, "tv/"+strconv.Itoa(tvdbID), lang, func(body []byte) (metadata.ExtraArt, error) {
		var r tvResponse
		if err := json.Unmarshal(body, &r); err != nil {
			return metadata.ExtraArt{}, err
		}
		return metadata.ExtraArt{
			LogoURL:   firstNonEmpty(pick(r.HDTVLogo, lang), pick(r.ClearLogo, lang)),
			BannerURL: pick(r.TVBanner, lang),
		}, nil
	})
}

// ArtistArt returns a music artist's logo (HD first, then SD) and banner.
func (c *Client) ArtistArt(ctx context.Context, artistMBID uuid.UUID, lang string) (metadata.ExtraArt, error) {
	if artistMBID == uuid.Nil {
		return metadata.ExtraArt{}, nil
	}
	return c.lookup(ctx, "music/"+artistMBID.String(), lang, func(body []byte) (metadata.ExtraArt, error) {
		var r artistResponse
		if err := json.Unmarshal(body, &r); err != nil {
			return metadata.ExtraArt{}, err
		}
		return metadata.ExtraArt{
			LogoURL:   firstNonEmpty(pick(r.HDMusicLogo, lang), pick(r.MusicLogo, lang)),
			BannerURL: pick(r.MusicBanner, lang),
		}, nil
	})
}

// AlbumArt returns an album's CD art, by MusicBrainz release-group id.
func (c *Client) AlbumArt(ctx context.Context, releaseGroupID uuid.UUID, lang string) (metadata.ExtraArt, error) {
	if releaseGroupID == uuid.Nil {
		return metadata.ExtraArt{}, nil
	}
	rg := releaseGroupID.String()
	return c.lookup(ctx, "music/albums/"+rg, lang, func(body []byte) (metadata.ExtraArt, error) {
		var r albumResponse
		if err := json.Unmarshal(body, &r); err != nil {
			return metadata.ExtraArt{}, err
		}
		// The reply is keyed by release group; take ours, else the only one.
		var cd []image
		if a, found := r.Albums[rg]; found {
			cd = a.CDArt
		} else if len(r.Albums) == 1 {
			for _, a := range r.Albums {
				cd = a.CDArt
			}
		}
		return metadata.ExtraArt{DiscURL: pick(cd, lang)}, nil
	})
}

// lookup returns the decoded answer for path in lang, from the cache when
// fresh. Nothing there (404) is an empty ExtraArt. Concurrent callers for the
// same path and language share one request.
func (c *Client) lookup(ctx context.Context, path, lang string, decode func([]byte) (metadata.ExtraArt, error)) (metadata.ExtraArt, error) {
	key := path + "|" + lang
	c.mu.Lock()
	if e, ok := c.cache[key]; ok && c.now().Sub(e.fetched) < cacheTTL {
		c.mu.Unlock()
		return e.art, nil
	}
	c.mu.Unlock()

	v, err, _ := c.sf.Do(key, func() (any, error) {
		body, err := c.request(ctx, path)
		if err != nil {
			return metadata.ExtraArt{}, err
		}
		var art metadata.ExtraArt
		if body != nil {
			if art, err = decode(body); err != nil {
				return metadata.ExtraArt{}, fmt.Errorf("fanarttv: decode %s: %w", path, err)
			}
		}
		c.store(key, art)
		return art, nil
	})
	if err != nil {
		return metadata.ExtraArt{}, err
	}
	art, _ := v.(metadata.ExtraArt)
	return art, nil
}

// store caches an answer, first dropping expired entries when the cache is
// full (and everything, if they were all fresh — a cache, not a store).
func (c *Client) store(key string, art metadata.ExtraArt) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.cache) >= maxCacheEntries {
		for k, e := range c.cache {
			if now.Sub(e.fetched) >= cacheTTL {
				delete(c.cache, k)
			}
		}
		if len(c.cache) >= maxCacheEntries {
			clear(c.cache)
		}
	}
	c.cache[key] = cacheEntry{art: art, fetched: now}
}

func (c *Client) request(ctx context.Context, path string) ([]byte, error) {
	q := url.Values{"api_key": {c.apiKey}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "OnScreen/1.0 (+https://github.com/CollinJAycock/OnScreen)")
	resp, err := c.hc.Do(req)
	if err != nil {
		// The URL carries the key: report the error without it.
		if uerr, ok := errors.AsType[*url.Error](err); ok {
			err = uerr.Err
		}
		return nil, fmt.Errorf("fanarttv: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		return nil, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ErrInvalidKey
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("fanarttv: %s: %s", path, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("fanarttv: read: %w", err)
	}
	return body, nil
}

// pick returns the URL of the best image for lang: lang first, then "en",
// then language-neutral ("00" or none), then any other language; the most
// liked within a tier, the earlier listed on a tie. "" when the list has none.
func pick(images []image, lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		lang = lang[:i] // "en-US" → "en"
	}
	tier := func(l string) int {
		l = strings.ToLower(strings.TrimSpace(l))
		switch {
		case lang != "" && l == lang:
			return 0
		case l == "en":
			return 1
		case l == "" || l == "00":
			return 2
		}
		return 3
	}
	cands := make([]image, 0, len(images))
	for _, im := range images {
		if strings.HasPrefix(im.URL, "https://") || strings.HasPrefix(im.URL, "http://") {
			cands = append(cands, im)
		}
	}
	sort.SliceStable(cands, func(a, b int) bool {
		ta, tb := tier(cands[a].Lang), tier(cands[b].Lang)
		if ta != tb {
			return ta < tb
		}
		return likes(cands[a]) > likes(cands[b])
	})
	if len(cands) == 0 {
		return ""
	}
	return cands[0].URL
}

func likes(im image) int {
	n, _ := strconv.Atoi(strings.TrimSpace(im.Likes))
	return n
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
