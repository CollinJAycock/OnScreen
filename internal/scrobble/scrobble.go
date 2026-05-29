// Package scrobble exports completed music listens to external services
// (ListenBrainz today; Last.fm planned). It is strictly one-way — OnScreen
// submits listens and never reads anything back — and best-effort: a failed
// submission is logged, never surfaced to the player or the watch-event path.
package scrobble

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// listenBrainzSubmitURL is the public submit-listens endpoint. Overridable per
// Service (submitURL) so tests can point at an httptest server.
const listenBrainzSubmitURL = "https://api.listenbrainz.org/1/submit-listens"

// Settings is a user's scrobble configuration. The token is already decrypted
// by the SettingsStore implementation — this package never touches ciphertext.
type Settings struct {
	ListenBrainzToken   string
	ListenBrainzEnabled bool
}

// Track is the metadata needed to scrobble one listen. ArtistName and TrackName
// are required by ListenBrainz; the rest are best-effort enrichers.
type Track struct {
	TrackName     string
	ArtistName    string
	ReleaseName   string // album title; optional
	RecordingMBID string // optional
	ReleaseMBID   string // optional
	ArtistMBID    string // optional
}

// SettingsStore loads a user's (decrypted) scrobble settings.
type SettingsStore interface {
	Get(ctx context.Context, userID uuid.UUID) (Settings, error)
}

// TrackLookup resolves a media id to scrobble-ready track metadata. ok=false
// means the item isn't a music track (movies / episodes / etc.), so the play
// is skipped without an error.
type TrackLookup interface {
	Track(ctx context.Context, mediaID uuid.UUID) (t Track, ok bool, err error)
}

// Service dispatches completed listens to a user's linked services.
type Service struct {
	store     SettingsStore
	tracks    TrackLookup
	client    *http.Client
	submitURL string
	logger    *slog.Logger
}

// NewService builds a Service. client should be SSRF-guarded for production use
// (e.g. safehttp.Default()); ListenBrainz resolves to a public IP so the
// public-only policy is correct.
func NewService(store SettingsStore, tracks TrackLookup, client *http.Client, logger *slog.Logger) *Service {
	return &Service{
		store:     store,
		tracks:    tracks,
		client:    client,
		submitURL: listenBrainzSubmitURL,
		logger:    logger,
	}
}

// OnScrobble submits a completed listen to the user's linked services. It is
// designed to be called asynchronously from the watch-event path: it returns
// nothing and swallows all errors (logged), so a flaky scrobble service can
// never affect playback recording. No-op when the user has nothing enabled or
// the item isn't a music track.
func (s *Service) OnScrobble(ctx context.Context, userID, mediaID uuid.UUID, listenedAt time.Time) {
	st, err := s.store.Get(ctx, userID)
	if err != nil {
		s.warn(ctx, "scrobble: load settings", "user_id", userID, "err", err)
		return
	}
	if !st.ListenBrainzEnabled || st.ListenBrainzToken == "" {
		return // nothing linked
	}

	track, ok, err := s.tracks.Track(ctx, mediaID)
	if err != nil {
		s.warn(ctx, "scrobble: resolve track", "media_id", mediaID, "err", err)
		return
	}
	// Not a music track, or missing the two fields ListenBrainz requires.
	if !ok || track.TrackName == "" || track.ArtistName == "" {
		return
	}

	if err := s.submitListenBrainz(ctx, st.ListenBrainzToken, track, listenedAt); err != nil {
		s.warn(ctx, "scrobble: listenbrainz submit", "media_id", mediaID, "err", err)
	}
}

// submitListenBrainz POSTs a single completed listen. Body shape per the
// ListenBrainz "submit-listens" API: listen_type=single + a one-element
// payload carrying listened_at + track_metadata.
func (s *Service) submitListenBrainz(ctx context.Context, token string, t Track, at time.Time) error {
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
	body, err := json.Marshal(map[string]any{
		"listen_type": "single",
		"payload": []any{map[string]any{
			"listened_at":    at.Unix(),
			"track_metadata": meta,
		}},
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
