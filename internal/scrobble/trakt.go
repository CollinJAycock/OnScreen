package scrobble

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/google/uuid"
)

const (
	traktAPIURL = "https://api.trakt.tv"

	// traktRefreshWindow: an access token this close to expiry is refreshed
	// before use rather than left to fail.
	traktRefreshWindow = time.Hour

	// traktDefaultInterval is the device-code poll interval when Trakt
	// doesn't send one.
	traktDefaultInterval = 5
)

// TraktApp is the operator's Trakt API application (Settings, Scrobbling).
// The client secret never leaves the server.
type TraktApp struct {
	ClientID     string
	ClientSecret string
}

func (a TraktApp) configured() bool { return a.ClientID != "" && a.ClientSecret != "" }

// traktDo sends one Trakt API request and returns the status code. out is
// decoded from a 2xx body only; err is a transport or decoding failure, and
// every status (including 4xx) is the caller's to interpret, because the
// device-code flow signals its states through them.
func (s *Service) traktDo(ctx context.Context, app TraktApp, method, path, accessToken string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, fmt.Errorf("marshal: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.traktURL+path, body)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", app.ClientID)
	req.Header.Set("User-Agent", userAgent)
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	if out != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// traktTokens is the token endpoints' response (device token and refresh).
type traktTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	CreatedAt    int64  `json:"created_at"`
}

func (t traktTokens) link(now time.Time, username string) TraktLink {
	issued := now
	if t.CreatedAt > 0 {
		issued = time.Unix(t.CreatedAt, 0)
	}
	return TraktLink{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		ExpiresAt:    issued.Add(time.Duration(t.ExpiresIn) * time.Second),
		Username:     username,
	}
}

// TraktLinkStart is the first step of linking Trakt: the user enters UserCode
// at VerificationURL (on any device), while the client polls complete with
// Pending every Interval seconds for up to ExpiresIn seconds.
type TraktLinkStart struct {
	UserCode        string
	VerificationURL string
	ExpiresIn       int
	Interval        int
	Pending         string
}

// StartTraktLink begins Trakt's device-code flow.
func (s *Service) StartTraktLink(ctx context.Context, userID uuid.UUID) (TraktLinkStart, error) {
	app := s.apps.Trakt(ctx)
	if !app.configured() {
		return TraktLinkStart{}, ErrNotConfigured
	}
	var dc struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURL string `json:"verification_url"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	status, err := s.traktDo(ctx, app, http.MethodPost, "/oauth/device/code", "",
		map[string]string{"client_id": app.ClientID}, &dc)
	if err != nil {
		return TraktLinkStart{}, fmt.Errorf("trakt device code: %w", err)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return TraktLinkStart{}, fmt.Errorf("trakt device code: %w (status %d)", ErrAppRejected, status)
	}
	if status != http.StatusOK || dc.DeviceCode == "" {
		return TraktLinkStart{}, fmt.Errorf("trakt device code: status %d", status)
	}
	pending, err := s.sealPending(serviceTrakt, userID, dc.DeviceCode, time.Duration(dc.ExpiresIn)*time.Second)
	if err != nil {
		return TraktLinkStart{}, err
	}
	interval := dc.Interval
	if interval <= 0 {
		interval = traktDefaultInterval
	}
	return TraktLinkStart{
		UserCode:        dc.UserCode,
		VerificationURL: dc.VerificationURL,
		ExpiresIn:       dc.ExpiresIn,
		Interval:        interval,
		Pending:         pending,
	}, nil
}

// CompleteTraktLink polls Trakt for the device code's tokens and stores them
// once the user has approved.
func (s *Service) CompleteTraktLink(ctx context.Context, userID uuid.UUID, pending string) (LinkResult, error) {
	app := s.apps.Trakt(ctx)
	if !app.configured() {
		return LinkResult{}, ErrNotConfigured
	}
	code, err := s.openPending(serviceTrakt, userID, pending)
	if err != nil {
		if errors.Is(err, errPendingExpired) {
			return LinkResult{Status: LinkExpired}, nil
		}
		return LinkResult{}, err
	}
	var tok traktTokens
	status, err := s.traktDo(ctx, app, http.MethodPost, "/oauth/device/token", "", map[string]string{
		"code":          code,
		"client_id":     app.ClientID,
		"client_secret": app.ClientSecret,
	}, &tok)
	if err != nil {
		return LinkResult{}, fmt.Errorf("trakt device token: %w", err)
	}
	// Trakt reports the device code's state through the status code.
	switch status {
	case http.StatusOK:
	case http.StatusBadRequest:
		return LinkResult{Status: LinkPending}, nil
	case http.StatusTooManyRequests:
		return LinkResult{Status: LinkSlowDown}, nil
	case http.StatusNotFound, http.StatusConflict, http.StatusGone: // invalid, already used, expired
		return LinkResult{Status: LinkExpired}, nil
	case http.StatusTeapot: // 418: the user denied the code
		return LinkResult{Status: LinkDenied}, nil
	case http.StatusUnauthorized, http.StatusForbidden: // the client id / secret
		return LinkResult{}, fmt.Errorf("trakt device token: %w (status %d)", ErrAppRejected, status)
	default:
		return LinkResult{}, fmt.Errorf("trakt device token: status %d", status)
	}
	if tok.AccessToken == "" {
		return LinkResult{}, errors.New("trakt device token: empty access token")
	}
	link := tok.link(s.now(), s.traktUsername(ctx, app, tok.AccessToken))
	if err := s.store.SetTrakt(ctx, userID, link); err != nil {
		return LinkResult{}, err
	}
	return LinkResult{Status: LinkLinked, Username: link.Username}, nil
}

// traktUsername is best-effort: the link works without it, it only labels
// the account in Settings.
func (s *Service) traktUsername(ctx context.Context, app TraktApp, accessToken string) string {
	var out struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	status, err := s.traktDo(ctx, app, http.MethodGet, "/users/settings", accessToken, nil, &out)
	if err != nil || status != http.StatusOK {
		return ""
	}
	return out.User.Username
}

// UnlinkTrakt revokes the user's token with Trakt (best-effort) and forgets
// the grant.
func (s *Service) UnlinkTrakt(ctx context.Context, userID uuid.UUID) error {
	st, err := s.store.Get(ctx, userID)
	if err != nil {
		return err
	}
	if app := s.apps.Trakt(ctx); st.Trakt.linked() && app.configured() {
		status, err := s.traktDo(ctx, app, http.MethodPost, "/oauth/revoke", "", map[string]string{
			"token":         st.Trakt.AccessToken,
			"client_id":     app.ClientID,
			"client_secret": app.ClientSecret,
		}, nil)
		if err != nil || status != http.StatusOK {
			s.warn(ctx, "scrobble: trakt revoke", "user_id", userID, "status", status, "err", err)
		}
	}
	return s.store.SetTrakt(ctx, userID, TraktLink{})
}

// traktAccessToken returns a usable access token, refreshing first when the
// current one is about to expire.
func (s *Service) traktAccessToken(ctx context.Context, app TraktApp, userID uuid.UUID, link TraktLink) (string, error) {
	if link.ExpiresAt.IsZero() || link.ExpiresAt.Sub(s.now()) > traktRefreshWindow {
		return link.AccessToken, nil
	}
	return s.refreshTrakt(ctx, app, userID, link)
}

// refreshTrakt trades the refresh token for a new pair and stores it. held is
// the grant the caller was using: if the stored one has moved on, another
// dispatch already refreshed (spending the old refresh token), so its token
// is used as-is.
func (s *Service) refreshTrakt(ctx context.Context, app TraktApp, userID uuid.UUID, held TraktLink) (string, error) {
	s.traktRefresh.Lock()
	defer s.traktRefresh.Unlock()

	cur, err := s.store.Get(ctx, userID)
	if err != nil {
		return "", err
	}
	if !cur.Trakt.linked() {
		return "", errors.New("trakt: unlinked")
	}
	if cur.Trakt.AccessToken != held.AccessToken {
		return cur.Trakt.AccessToken, nil
	}
	var tok traktTokens
	status, err := s.traktDo(ctx, app, http.MethodPost, "/oauth/token", "", map[string]string{
		"refresh_token": cur.Trakt.RefreshToken,
		"client_id":     app.ClientID,
		"client_secret": app.ClientSecret,
		"redirect_uri":  "urn:ietf:wg:oauth:2.0:oob",
		"grant_type":    "refresh_token",
	}, &tok)
	if err != nil {
		return "", fmt.Errorf("trakt refresh: %w", err)
	}
	if status == http.StatusUnauthorized {
		// The grant is gone: revoked on trakt.tv, or the operator swapped the
		// Trakt app it belonged to. Unlink, so Settings shows it rather than
		// every play failing quietly from now on.
		if err := s.store.SetTrakt(ctx, userID, TraktLink{}); err != nil {
			return "", err
		}
		return "", errors.New("trakt: refresh token rejected; unlinked")
	}
	if status != http.StatusOK || tok.AccessToken == "" {
		return "", fmt.Errorf("trakt refresh: status %d", status)
	}
	next := tok.link(s.now(), cur.Trakt.Username)
	if err := s.store.SetTrakt(ctx, userID, next); err != nil {
		return "", err
	}
	return next.AccessToken, nil
}

// traktIDs are the external ids Trakt matches on; zero values are omitted.
type traktIDs struct {
	TMDB int    `json:"tmdb,omitempty"`
	TVDB int    `json:"tvdb,omitempty"`
	IMDB string `json:"imdb,omitempty"`
}

type traktMedia struct {
	Title string   `json:"title,omitempty"`
	Year  int      `json:"year,omitempty"`
	IDs   traktIDs `json:"ids"`
}

type traktEpisodeRef struct {
	Season int `json:"season"`
	Number int `json:"number"`
}

// traktScrobble is the body of /scrobble/{start,pause,stop}. An episode is
// sent as its show plus season and number, which Trakt resolves even when
// the episode's own ids come from a different source than the show's.
type traktScrobble struct {
	Movie    *traktMedia      `json:"movie,omitempty"`
	Show     *traktMedia      `json:"show,omitempty"`
	Episode  *traktEpisodeRef `json:"episode,omitempty"`
	Progress float64          `json:"progress"`
}

func (v Video) traktBody(progress float64) traktScrobble {
	media := &traktMedia{Title: v.Title, Year: v.Year, IDs: traktIDs{TMDB: v.TMDBID, IMDB: v.IMDBID}}
	if !v.Episode {
		return traktScrobble{Movie: media, Progress: progress}
	}
	media.IDs.TVDB = v.TVDBID
	return traktScrobble{Show: media, Episode: &traktEpisodeRef{Season: v.Season, Number: v.Number}, Progress: progress}
}

var traktScrobblePath = map[playKind]string{
	playStart: "/scrobble/start",
	playPause: "/scrobble/pause",
	playStop:  "/scrobble/stop",
}

func (s *Service) dispatchTrakt(ctx context.Context, kind playKind, userID uuid.UUID, link TraktLink, mediaID uuid.UUID, positionMS int64, durationMS *int64) {
	v, ok, err := s.media.Video(ctx, mediaID)
	if err != nil {
		s.warn(ctx, "scrobble: resolve video", "media_id", mediaID, "err", err)
		return
	}
	if !ok {
		return
	}
	dur := v.DurationMS
	if durationMS != nil && *durationMS > 0 {
		dur = *durationMS
	}
	if dur <= 0 {
		return // Trakt needs a progress percentage
	}
	app := s.apps.Trakt(ctx)
	if !app.configured() {
		return // the operator removed the Trakt app; nothing can be sent
	}
	progress := math.Round(math.Min(math.Max(float64(positionMS)/float64(dur)*100, 0), 100)*100) / 100
	body := v.traktBody(progress)
	path := traktScrobblePath[kind]

	token, err := s.traktAccessToken(ctx, app, userID, link)
	if err != nil {
		s.warn(ctx, "scrobble: trakt token", "user_id", userID, "err", err)
		return
	}
	status, err := s.traktDo(ctx, app, http.MethodPost, path, token, body, nil)
	if err == nil && status == http.StatusUnauthorized {
		// Expired or revoked ahead of the stored expiry: refresh once.
		if token, err = s.refreshTrakt(ctx, app, userID, TraktLink{AccessToken: token}); err == nil {
			status, err = s.traktDo(ctx, app, http.MethodPost, path, token, body, nil)
		}
	}
	switch {
	case err != nil:
		s.warn(ctx, "scrobble: trakt "+path, "media_id", mediaID, "err", err)
	case status == http.StatusConflict:
		// Trakt already recorded this play (a repeat stop soon after one).
	case status == http.StatusNotFound:
		s.warn(ctx, "scrobble: trakt has no match", "media_id", mediaID, "title", v.Title, "year", v.Year)
	case status < 200 || status >= 300:
		s.warn(ctx, "scrobble: trakt "+path, "media_id", mediaID, "status", status)
	}
}
