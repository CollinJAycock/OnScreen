package scrobble

import (
	"context"
	"crypto/md5" //nolint:gosec // Last.fm's request signature is defined as MD5; not used for security here.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	lastFMAPIURL      = "https://ws.audioscrobbler.com/2.0/"
	lastFMAuthPageURL = "https://www.last.fm/api/auth/"

	// lastFMTokenTTL is how long a request token from auth.getToken can be
	// approved and exchanged.
	lastFMTokenTTL = 60 * time.Minute

	// lastFMMinTrackMS: Last.fm doesn't accept scrobbles of tracks of 30
	// seconds or less.
	lastFMMinTrackMS = 30_000
)

// LastFMApp is the operator's Last.fm API account (Settings, Scrobbling). The
// shared secret signs every call and never leaves the server.
type LastFMApp struct {
	APIKey       string
	SharedSecret string
}

func (a LastFMApp) configured() bool { return a.APIKey != "" && a.SharedSecret != "" }

// lastFMError is a Last.fm API error body: {"error": 14, "message": "..."}.
type lastFMError struct {
	Code    int    `json:"error"`
	Message string `json:"message"`
}

func (e *lastFMError) Error() string { return fmt.Sprintf("last.fm error %d: %s", e.Code, e.Message) }

// Last.fm error codes the link flow tells apart.
const (
	lastFMErrInvalidToken   = 4  // unknown or malformed token
	lastFMErrTokenNotAuthed = 14 // the user hasn't approved it yet
	lastFMErrTokenExpired   = 15
)

// lastFMSign computes api_sig: the MD5 of every parameter except format and
// callback, sorted by name and concatenated as name+value, followed by the
// shared secret.
func lastFMSign(params url.Values, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "format" || k == "callback" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(params.Get(k))
	}
	b.WriteString(secret)
	sum := md5.Sum([]byte(b.String())) //nolint:gosec // see import
	return hex.EncodeToString(sum[:])
}

// lastFMCall signs and POSTs one API method, decoding the JSON response into
// out (nil to discard it). An API error comes back as *lastFMError.
func (s *Service) lastFMCall(ctx context.Context, app LastFMApp, params url.Values, out any) error {
	params.Set("api_key", app.APIKey)
	params.Set("api_sig", lastFMSign(params, app.SharedSecret))
	params.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.lastFMURL, strings.NewReader(params.Encode()))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	// Failures carry {"error": N, "message": ...}, usually but not always
	// with a 4xx status, so the body decides.
	var apiErr lastFMError
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Code != 0 {
		return &apiErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("last.fm returned %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// lastFMParams is the metadata both updateNowPlaying and scrobble take.
func (t Track) lastFMParams(sessionKey string) url.Values {
	p := url.Values{}
	p.Set("sk", sessionKey)
	p.Set("artist", t.ArtistName)
	p.Set("track", t.TrackName)
	if t.ReleaseName != "" {
		p.Set("album", t.ReleaseName)
		// The artist is the album's (the track sits under artist, album), so
		// it's the album artist too; saying so keeps compilations grouped.
		p.Set("albumArtist", t.ArtistName)
	}
	if t.TrackNumber > 0 {
		p.Set("trackNumber", strconv.Itoa(t.TrackNumber))
	}
	if t.DurationMS > 0 {
		p.Set("duration", strconv.FormatInt(t.DurationMS/1000, 10))
	}
	if t.RecordingMBID != "" {
		p.Set("mbid", t.RecordingMBID)
	}
	return p
}

func (s *Service) lastFMNowPlaying(ctx context.Context, app LastFMApp, sessionKey string, t Track) error {
	p := t.lastFMParams(sessionKey)
	p.Set("method", "track.updateNowPlaying")
	return s.lastFMCall(ctx, app, p, nil)
}

// lastFMScrobble submits a completed listen that started at started.
func (s *Service) lastFMScrobble(ctx context.Context, app LastFMApp, sessionKey string, t Track, started time.Time) error {
	if t.DurationMS > 0 && t.DurationMS <= lastFMMinTrackMS {
		return nil // too short for Last.fm to count
	}
	p := t.lastFMParams(sessionKey)
	p.Set("method", "track.scrobble")
	p.Set("timestamp", strconv.FormatInt(started.Unix(), 10))
	var out struct {
		Scrobbles struct {
			Attr struct {
				Accepted json.RawMessage `json:"accepted"`
			} `json:"@attr"`
		} `json:"scrobbles"`
	}
	if err := s.lastFMCall(ctx, app, p, &out); err != nil {
		return err
	}
	// A 200 can still say the scrobble was ignored (e.g. a timestamp too far
	// in the past). The count arrives as a number or a string.
	if n := strings.Trim(string(out.Scrobbles.Attr.Accepted), `"`); n == "0" {
		return errors.New("last.fm ignored the scrobble")
	}
	return nil
}

// LastFMLinkStart is the first step of linking Last.fm: the user approves
// OnScreen at AuthURL, then the client finishes with Pending.
type LastFMLinkStart struct {
	AuthURL string
	Pending string
}

// StartLastFMLink begins Last.fm's desktop-style handshake: fetch a request
// token and send the user to approve it. No callback URL is involved, so it
// works for a server that isn't reachable from the internet.
func (s *Service) StartLastFMLink(ctx context.Context, userID uuid.UUID) (LastFMLinkStart, error) {
	app := s.apps.LastFM(ctx)
	if !app.configured() {
		return LastFMLinkStart{}, ErrNotConfigured
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := s.lastFMCall(ctx, app, url.Values{"method": {"auth.getToken"}}, &out); err != nil {
		return LastFMLinkStart{}, fmt.Errorf("last.fm auth.getToken: %w", err)
	}
	if out.Token == "" {
		return LastFMLinkStart{}, errors.New("last.fm auth.getToken: empty token")
	}
	pending, err := s.sealPending(serviceLastFM, userID, out.Token, lastFMTokenTTL)
	if err != nil {
		return LastFMLinkStart{}, err
	}
	authURL := s.lastFMAuthURL + "?" + url.Values{"api_key": {app.APIKey}, "token": {out.Token}}.Encode()
	return LastFMLinkStart{AuthURL: authURL, Pending: pending}, nil
}

// CompleteLastFMLink exchanges an approved request token for a session and
// stores it. Until the user approves, it reports LinkPending; the client
// polls.
func (s *Service) CompleteLastFMLink(ctx context.Context, userID uuid.UUID, pending string) (LinkResult, error) {
	app := s.apps.LastFM(ctx)
	if !app.configured() {
		return LinkResult{}, ErrNotConfigured
	}
	token, err := s.openPending(serviceLastFM, userID, pending)
	if err != nil {
		if errors.Is(err, errPendingExpired) {
			return LinkResult{Status: LinkExpired}, nil
		}
		return LinkResult{}, err
	}
	var out struct {
		Session struct {
			Name string `json:"name"`
			Key  string `json:"key"`
		} `json:"session"`
	}
	err = s.lastFMCall(ctx, app, url.Values{"method": {"auth.getSession"}, "token": {token}}, &out)
	var apiErr *lastFMError
	switch {
	case errors.As(err, &apiErr) && apiErr.Code == lastFMErrTokenNotAuthed:
		return LinkResult{Status: LinkPending}, nil
	case errors.As(err, &apiErr) && (apiErr.Code == lastFMErrTokenExpired || apiErr.Code == lastFMErrInvalidToken):
		return LinkResult{Status: LinkExpired}, nil
	case err != nil:
		return LinkResult{}, fmt.Errorf("last.fm auth.getSession: %w", err)
	}
	if out.Session.Key == "" {
		return LinkResult{}, errors.New("last.fm auth.getSession: empty session key")
	}
	if err := s.store.SetLastFM(ctx, userID, out.Session.Key, out.Session.Name); err != nil {
		return LinkResult{}, err
	}
	return LinkResult{Status: LinkLinked, Username: out.Session.Name}, nil
}

// UnlinkLastFM forgets the user's session. Last.fm has no revoke call; the
// user can remove OnScreen from their Last.fm settings.
func (s *Service) UnlinkLastFM(ctx context.Context, userID uuid.UUID) error {
	return s.store.SetLastFM(ctx, userID, "", "")
}
