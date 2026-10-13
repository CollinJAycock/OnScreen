// Package serverimport copies users' watch state, ratings, favorites,
// playlists and play history from a Plex, Jellyfin or Emby server into
// OnScreen, so switching servers doesn't mean starting over.
//
// An import reads the source server into a Snapshot (source-side keys,
// external IDs and file paths), matches the items it refers to against this
// server's library (Index), turns what each mapped account did into a Plan
// of writes, and applies it. A dry run stops after the Plan, so an admin can
// see what would match before anything is written.
package serverimport

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

	"github.com/onscreen/onscreen/internal/safehttp"
)

// Kind is a source server type.
type Kind string

// The supported source servers.
const (
	KindPlex     Kind = "plex"
	KindJellyfin Kind = "jellyfin"
	KindEmby     Kind = "emby"
)

// Valid reports whether k is a supported source.
func (k Kind) Valid() bool {
	return k == KindPlex || k == KindJellyfin || k == KindEmby
}

// Label is the source's product name, for display.
func (k Kind) Label() string {
	switch k {
	case KindPlex:
		return "Plex"
	case KindJellyfin:
		return "Jellyfin"
	case KindEmby:
		return "Emby"
	}
	return string(k)
}

// ItemKind is what a source item is.
type ItemKind string

// Source item kinds.
const (
	ItemMovie   ItemKind = "movie"
	ItemShow    ItemKind = "show"
	ItemEpisode ItemKind = "episode"
	ItemTrack   ItemKind = "track"
	ItemVideo   ItemKind = "video" // music videos, home videos, other clips
)

// ExternalIDs are an item's metadata-provider IDs. Zero / "" when unknown.
type ExternalIDs struct {
	TMDB int
	TVDB int
	IMDB string
}

func (e ExternalIDs) empty() bool { return e.TMDB == 0 && e.TVDB == 0 && e.IMDB == "" }

// Item is one source library item.
type Item struct {
	Key   string // the source's ID for the item
	Kind  ItemKind
	Title string
	Path  string // file path as the source sees it; "" when unknown
	IDs   ExternalIDs

	// Episodes: the show's IDs and the season and episode numbers.
	ShowIDs ExternalIDs
	Season  int
	Episode int
	HasSE   bool

	DurationMS int64 // 0 when unknown
}

// Account is a source user.
type Account struct {
	ID   string
	Name string
	// FullState: the source exposes this account's per-item state (watched,
	// resume point, rating, favorite, playlists). A Plex server shows that
	// only for its owner; its other accounts are known from play history.
	FullState bool
}

// State is one account's state for one item.
type State struct {
	Key          string
	Played       bool
	LastViewedAt time.Time // zero when unknown
	PositionMS   int64     // resume point; 0 = none
	Rating       *float64  // 0-10
	Favorite     bool
}

// Play is one past play from the source's history.
type Play struct {
	Account string
	Key     string
	At      time.Time
}

// Playlist is one account's playlist.
type Playlist struct {
	ID   string
	Name string
	Keys []string
}

// ServerInfo identifies a source server.
type ServerInfo struct {
	ID      string
	Name    string
	Version string
}

// Snapshot is everything an import read from the source.
type Snapshot struct {
	Server    ServerInfo
	Accounts  []Account
	Items     map[string]*Item
	States    map[string][]State    // by account ID
	Plays     []Play                // mapped accounts only
	Playlists map[string][]Playlist // by account ID
}

func newSnapshot(info ServerInfo, accounts []Account) *Snapshot {
	return &Snapshot{
		Server:    info,
		Accounts:  accounts,
		Items:     map[string]*Item{},
		States:    map[string][]State{},
		Playlists: map[string][]Playlist{},
	}
}

// Options says which kinds of data an import copies.
type Options struct {
	Watched   bool `json:"watched"`   // watched marks and resume points
	Ratings   bool `json:"ratings"`   // star ratings
	Favorites bool `json:"favorites"` // favorites (Jellyfin / Emby)
	Playlists bool `json:"playlists"` // playlists
	History   bool `json:"history"`   // every past play, as play events
}

func (o Options) any() bool {
	return o.Watched || o.Ratings || o.Favorites || o.Playlists || o.History
}

// Source reads a server.
type Source interface {
	// Probe checks the URL and token and lists the server's accounts.
	Probe(ctx context.Context) (ServerInfo, []Account, error)
	// Fetch reads what the given accounts did. progress reports what it's
	// reading, for the job's phase.
	Fetch(ctx context.Context, accounts []string, opts Options, progress func(string)) (*Snapshot, error)
}

// Errors the API maps to client responses.
var (
	ErrUnreachable  = errors.New("the server could not be reached")
	ErrUnauthorized = errors.New("the server refused the token")
	ErrInvalidURL   = errors.New("the server URL must be an http or https URL with no user name, query or fragment")
)

// FetchError is a source request that failed after connecting.
type FetchError struct{ Msg string }

func (e *FetchError) Error() string { return e.Msg }

// maxResponseBytes bounds one source response. A page of 500 items with
// full metadata is a few MB.
const maxResponseBytes = 64 << 20

// requestTimeout bounds one source request. A large library page on a busy
// server can take a while.
const requestTimeout = 2 * time.Minute

// newHTTPClient allows the operator's LAN and loopback (the old server
// usually runs beside OnScreen) but not link-local, the cloud metadata
// range. Same policy as the Radarr/Sonarr client: the URL is admin-entered.
func newHTTPClient() *http.Client {
	return safehttp.NewClient(safehttp.DialPolicy{AllowPrivate: true, AllowLoopback: true}, requestTimeout)
}

// NormalizeURL validates a server base URL and returns it as
// scheme://host[:port][/path] with no trailing slash.
func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", ErrInvalidURL
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

// getJSON GETs base+path?query with headers and decodes the body into out.
func getJSON(ctx context.Context, hc *http.Client, rawURL string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s", ErrUnreachable, cleanErr(err, rawURL))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &FetchError{Msg: fmt.Sprintf("%s answered %s", redact(rawURL), resp.Status)}
	}
	body := io.LimitReader(resp.Body, maxResponseBytes+1)
	data, err := io.ReadAll(body)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s", ErrUnreachable, cleanErr(err, rawURL))
	}
	if len(data) > maxResponseBytes {
		return &FetchError{Msg: fmt.Sprintf("%s sent more than %d MB", redact(rawURL), maxResponseBytes>>20)}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &FetchError{Msg: fmt.Sprintf("%s did not answer with the expected JSON (is this the right kind of server?)", redact(rawURL))}
	}
	return nil
}

// redact drops the query string from a URL for error messages.
func redact(rawURL string) string {
	base, _, _ := strings.Cut(rawURL, "?")
	return base
}

// cleanErr is a transport error's message without the request URL's query.
func cleanErr(err error, rawURL string) string {
	return strings.ReplaceAll(err.Error(), rawURL, redact(rawURL))
}
