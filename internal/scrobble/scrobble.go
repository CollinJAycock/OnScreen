// Package scrobble exports plays to external services: music listens to
// ListenBrainz and Last.fm, movie and episode plays to Trakt. It is strictly
// one-way — OnScreen submits plays and never reads a history back — and
// best-effort: a failed submission is logged, never surfaced to the player or
// the watch-event path.
package scrobble

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// listenBrainzSubmitURL is the public submit-listens endpoint. Overridable per
// Service (submitURL) so tests can point at an httptest server.
const listenBrainzSubmitURL = "https://api.listenbrainz.org/1/submit-listens"

// userAgent identifies OnScreen to Last.fm and Trakt (Trakt rejects requests
// without one).
const userAgent = "OnScreen/1.0 (+https://github.com/CollinJAycock/OnScreen)"

// dispatchTimeout bounds one play's worth of outbound calls. The hook runs
// detached from any request, so without it a hung service would pin the
// goroutine.
const dispatchTimeout = 30 * time.Second

// Settings is a user's scrobble configuration. Credentials are already
// decrypted by the SettingsStore implementation — this package never touches
// ciphertext.
type Settings struct {
	ListenBrainzToken   string
	ListenBrainzEnabled bool

	LastFMSessionKey string
	LastFMUsername   string

	Trakt TraktLink
}

// TraktLink is a user's Trakt OAuth grant. The zero value means not linked.
type TraktLink struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Username     string
}

func (t TraktLink) linked() bool { return t.AccessToken != "" }

func (s Settings) listenBrainzOn() bool { return s.ListenBrainzEnabled && s.ListenBrainzToken != "" }
func (s Settings) musicLinked() bool    { return s.listenBrainzOn() || s.LastFMSessionKey != "" }

// Track is the metadata needed to scrobble one listen. ArtistName and TrackName
// are required by every service; the rest are best-effort enrichers.
type Track struct {
	TrackName     string
	ArtistName    string
	ReleaseName   string // album title; optional
	TrackNumber   int    // 0 = unknown
	DurationMS    int64  // the item's duration; 0 = unknown
	RecordingMBID string // optional
	ReleaseMBID   string // optional
	ArtistMBID    string // optional
}

// Video is a movie or an episode, as much as Trakt needs to match it. An
// episode carries its show's ids and title and its season / episode numbers;
// Trakt matches the ids first, then the title and year.
type Video struct {
	Episode    bool
	Title      string // movie title, or the show's for an episode
	Year       int
	TMDBID     int
	TVDBID     int
	IMDBID     string
	Season     int // episodes only
	Number     int // episodes only
	DurationMS int64
}

// SettingsStore loads and saves a user's (decrypted) scrobble credentials.
type SettingsStore interface {
	Get(ctx context.Context, userID uuid.UUID) (Settings, error)
	SetListenBrainz(ctx context.Context, userID uuid.UUID, token string, enabled bool) error
	// SetLastFM stores a Last.fm session; an empty sessionKey unlinks.
	SetLastFM(ctx context.Context, userID uuid.UUID, sessionKey, username string) error
	// SetTrakt stores a Trakt grant; the zero TraktLink unlinks.
	SetTrakt(ctx context.Context, userID uuid.UUID, link TraktLink) error
}

// TrackLookup resolves a media id to scrobble-ready track metadata. ok=false
// means the item isn't a music track (movies / episodes / etc.), so the play
// is skipped without an error.
type TrackLookup interface {
	Track(ctx context.Context, mediaID uuid.UUID) (t Track, ok bool, err error)
}

// VideoLookup resolves a media id to a movie or episode. ok=false means it is
// neither (or an episode without a season and show above it).
type VideoLookup interface {
	Video(ctx context.Context, mediaID uuid.UUID) (v Video, ok bool, err error)
}

// MediaLookup is what the dispatcher needs to know about a played item.
type MediaLookup interface {
	TrackLookup
	VideoLookup
}

// AppCredentials supplies the operator's API accounts, read per call so an
// admin's change in Settings applies without a restart.
type AppCredentials interface {
	LastFM(ctx context.Context) LastFMApp
	Trakt(ctx context.Context) TraktApp
}

// Sealer encrypts the short-lived handle a link flow hands the browser. It is
// the server Encryptor.
type Sealer interface {
	EncryptContext(plaintext, context string) (string, error)
	DecryptContext(encoded, context string) (string, error)
}

// Service dispatches plays to a user's linked services and runs the Last.fm
// and Trakt account-link flows.
type Service struct {
	store  SettingsStore
	media  MediaLookup
	apps   AppCredentials
	sealer Sealer
	client *http.Client
	logger *slog.Logger

	submitURL     string // ListenBrainz
	lastFMURL     string
	lastFMAuthURL string
	traktURL      string

	now        func() time.Time
	nowPlaying *nowPlayingGate
	// traktRefresh serializes token refreshes: Trakt rotates the refresh
	// token on use, so two concurrent refreshes would race to spend it.
	traktRefresh sync.Mutex
}

// NewService builds a Service. client should be SSRF-guarded for production use
// (e.g. safehttp.Default()); every service resolves to a public IP so the
// public-only policy is correct.
func NewService(store SettingsStore, media MediaLookup, apps AppCredentials, sealer Sealer, client *http.Client, logger *slog.Logger) *Service {
	return &Service{
		store:         store,
		media:         media,
		apps:          apps,
		sealer:        sealer,
		client:        client,
		logger:        logger,
		submitURL:     listenBrainzSubmitURL,
		lastFMURL:     lastFMAPIURL,
		lastFMAuthURL: lastFMAuthPageURL,
		traktURL:      traktAPIURL,
		now:           time.Now,
		nowPlaying:    newNowPlayingGate(nowPlayingWindow),
	}
}

// Status is the client-safe view of a user's scrobble config. It never
// carries a credential — only what's linked, and whether the operator has set
// up the service at all (Available), which decides whether a user can link.
type Status struct {
	ListenBrainzLinked  bool
	ListenBrainzEnabled bool

	LastFMAvailable bool
	LastFMLinked    bool
	LastFMUsername  string

	TraktAvailable bool
	TraktLinked    bool
	TraktUsername  string
}

// Status reports what the user has linked.
func (s *Service) Status(ctx context.Context, userID uuid.UUID) (Status, error) {
	st, err := s.store.Get(ctx, userID)
	if err != nil {
		return Status{}, err
	}
	return Status{
		ListenBrainzLinked:  st.ListenBrainzToken != "",
		ListenBrainzEnabled: st.ListenBrainzEnabled,
		LastFMAvailable:     s.apps.LastFM(ctx).configured(),
		LastFMLinked:        st.LastFMSessionKey != "",
		LastFMUsername:      st.LastFMUsername,
		TraktAvailable:      s.apps.Trakt(ctx).configured(),
		TraktLinked:         st.Trakt.linked(),
		TraktUsername:       st.Trakt.Username,
	}, nil
}

// SetListenBrainz links (or, with an empty token, unlinks) ListenBrainz.
func (s *Service) SetListenBrainz(ctx context.Context, userID uuid.UUID, token string, enabled bool) error {
	return s.store.SetListenBrainz(ctx, userID, token, enabled)
}

// OnPlayback is the watch-event hook, called asynchronously for every play,
// pause and stop a client reports. It returns nothing and swallows all errors
// (logged), so a flaky service can never affect playback recording.
//
//   - play / resume: "now playing" — ListenBrainz playing_now, Last.fm
//     updateNowPlaying, Trakt scrobble/start. Clients report "playing" on
//     every progress heartbeat, so this passes once per item per
//     nowPlayingWindow, checked before any database work.
//   - pause: Trakt scrobble/pause, which keeps the resume point there.
//   - stop: the completed play. A listen reaching the threshold (half the
//     track, or 4 minutes) goes to ListenBrainz and Last.fm; Trakt gets
//     scrobble/stop, which it records as watched at 80% and otherwise keeps as
//     paused progress.
func (s *Service) OnPlayback(ctx context.Context, event string, userID, mediaID uuid.UUID, positionMS int64, durationMS *int64, at time.Time) {
	var kind playKind
	switch event {
	case "play", "resume":
		if !s.nowPlaying.claim(userID, mediaID, s.now()) {
			return
		}
		kind = playStart
	case "pause":
		s.nowPlaying.release(userID, mediaID)
		kind = playPause
	case "stop":
		s.nowPlaying.release(userID, mediaID)
		kind = playStop
	default:
		return
	}

	ctx, cancel := context.WithTimeout(ctx, dispatchTimeout)
	defer cancel()

	st, err := s.store.Get(ctx, userID)
	if err != nil {
		s.warn(ctx, "scrobble: load settings", "user_id", userID, "err", err)
		return
	}
	// Music has nothing to say about a pause.
	if st.musicLinked() && kind != playPause {
		s.dispatchMusic(ctx, kind, st, mediaID, positionMS, durationMS, at)
	}
	if st.Trakt.linked() {
		s.dispatchTrakt(ctx, kind, userID, st.Trakt, mediaID, positionMS, durationMS)
	}
}

type playKind int

const (
	playStart playKind = iota
	playPause
	playStop
)

func (s *Service) dispatchMusic(ctx context.Context, kind playKind, st Settings, mediaID uuid.UUID, positionMS int64, durationMS *int64, at time.Time) {
	if kind == playStop && !listenedEnough(positionMS, durationMS) {
		return // a skip or a brief preview
	}
	track, ok, err := s.media.Track(ctx, mediaID)
	if err != nil {
		s.warn(ctx, "scrobble: resolve track", "media_id", mediaID, "err", err)
		return
	}
	// Not a music track, or missing the two fields every service requires.
	if !ok || track.TrackName == "" || track.ArtistName == "" {
		return
	}
	if durationMS != nil && *durationMS > 0 {
		track.DurationMS = *durationMS
	}

	if st.listenBrainzOn() {
		listenType := "single"
		if kind == playStart {
			listenType = "playing_now"
		}
		if err := s.submitListenBrainz(ctx, st.ListenBrainzToken, listenType, track, at); err != nil {
			s.warn(ctx, "scrobble: listenbrainz submit", "media_id", mediaID, "err", err)
		}
	}
	if st.LastFMSessionKey != "" {
		app := s.apps.LastFM(ctx)
		if !app.configured() {
			return // the operator removed the API account; nothing can be signed
		}
		if kind == playStart {
			err = s.lastFMNowPlaying(ctx, app, st.LastFMSessionKey, track)
		} else {
			// Last.fm wants the time the track started, not when it stopped.
			started := at.Add(-time.Duration(positionMS) * time.Millisecond)
			err = s.lastFMScrobble(ctx, app, st.LastFMSessionKey, track, started)
		}
		if err != nil {
			s.warn(ctx, "scrobble: last.fm submit", "media_id", mediaID, "err", err)
		}
	}
}

// submitListenBrainz POSTs one listen. Body shape per the ListenBrainz
// "submit-listens" API: listen_type single (a completed listen, with
// listened_at) or playing_now (without), plus a one-element payload carrying
// the track_metadata.
func (s *Service) submitListenBrainz(ctx context.Context, token, listenType string, t Track, at time.Time) error {
	info := map[string]any{"submission_client": "OnScreen"}
	if t.RecordingMBID != "" {
		info["recording_mbid"] = t.RecordingMBID
	}
	if t.ReleaseMBID != "" {
		info["release_mbid"] = t.ReleaseMBID
	}
	if t.ArtistMBID != "" {
		info["artist_mbids"] = []string{t.ArtistMBID}
	}
	meta := map[string]any{
		"artist_name":     t.ArtistName,
		"track_name":      t.TrackName,
		"additional_info": info,
	}
	if t.ReleaseName != "" {
		meta["release_name"] = t.ReleaseName
	}
	listen := map[string]any{"track_metadata": meta}
	if listenType == "single" {
		listen["listened_at"] = at.Unix()
	}
	body, err := json.Marshal(map[string]any{
		"listen_type": listenType,
		"payload":     []any{listen},
	})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.submitURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	// ListenBrainz uses a per-user token, not Bearer: "Authorization: Token <t>".
	req.Header.Set("Authorization", "Token "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("listenbrainz returned %d", resp.StatusCode)
	}
	return nil
}

func (s *Service) warn(ctx context.Context, msg string, args ...any) {
	if s.logger != nil {
		s.logger.WarnContext(ctx, msg, args...)
	}
}

// listenedEnough reports whether a play crossed the scrobble threshold: at
// least half the track's duration, or at least 4 minutes. With an unknown
// duration it falls back to the 4-minute rule alone — a short play of an
// unknown-length track can't be confirmed as a real listen, so it's skipped.
func listenedEnough(positionMS int64, durationMS *int64) bool {
	const fourMinutesMS = 4 * 60 * 1000
	if positionMS >= fourMinutesMS {
		return true
	}
	if durationMS != nil && *durationMS > 0 {
		return float64(positionMS)/float64(*durationMS) >= 0.5
	}
	return false
}
