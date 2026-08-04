package v1

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/livetv"
)

// LiveTVStreamProxy is the slice of HLSProxy the stream handlers use.
// Kept narrow so the handler can be tested with a stub.
type LiveTVStreamProxy interface {
	// Acquire is create-if-missing — it can tune a tuner and start ffmpeg.
	// Only the playlist handler may call it.
	Acquire(ctx context.Context, channelID uuid.UUID) (*livetv.HLSSession, error)
	// Lookup finds an already-running session without ever creating one.
	// Segment handlers use this so a cheap GET can't spawn a tune.
	Lookup(channelID uuid.UUID) (*livetv.HLSSession, bool)
	Release(s *livetv.HLSSession)
}

// WithStreamProxy attaches the HLS proxy. Without it, /stream endpoints
// 503 with LIVE_TV_NOT_CONFIGURED.
func (h *LiveTVHandler) WithStreamProxy(p LiveTVStreamProxy) *LiveTVHandler {
	h.proxy = p
	return h
}

// segmentNameRe limits the segment filename to ffmpeg's actual output
// pattern. Without this a malicious URL could escape the session
// directory via "..".
var segmentNameRe = regexp.MustCompile(`^seg-\d+\.ts$`)

// playlistMaxWait caps how long the playlist endpoint blocks waiting for
// ffmpeg to write the first manifest. Without this an unhealthy upstream
// would hang the request indefinitely. 10s matches the worst-case HDHR
// channel-lock latency.
const playlistMaxWait = 10 * time.Second

// playlistPollInterval is the granularity of the wait loop.
const playlistPollInterval = 200 * time.Millisecond

// rewriteLivePlaylistTokens appends ?token= to every segment URI in a live
// playlist when the caller authenticated by query token.
//
// The playlist's segment lines are RELATIVE ("segments/seg-00000.ts", via
// ffmpeg's -hls_base_url) and relative resolution keeps the PATH, not the
// QUERY — so a native player that loaded the playlist as
// stream.m3u8?token=X fetched every segment with no token at all and got a
// 401 for each. Browsers never noticed (cookies ride along); every non-cookie
// client had a playlist it could read and segments it couldn't.
func rewriteLivePlaylistTokens(playlist []byte, token string) []byte {
	if token == "" {
		return playlist
	}
	lines := strings.Split(string(playlist), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasSuffix(trimmed, ".ts") {
			lines[i] = trimmed + "?token=" + url.QueryEscape(token)
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// StreamPlaylist handles GET /api/v1/tv/channels/{id}/stream.m3u8.
//
// Acquires (or creates+starts) the per-channel HLS session and returns
// the master playlist. The proxy refcounts viewers — the playlist GET
// counts as one viewer, released when the response completes. Segment
// requests don't acquire/release because they're cheap GETs against
// disk; the playlist is the lifecycle anchor.
func (h *LiveTVHandler) StreamPlaylist(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	if h.proxy == nil {
		respond.Error(w, r, http.StatusServiceUnavailable,
			"LIVE_TV_NOT_CONFIGURED", "live TV streaming is not enabled")
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid channel id")
		return
	}

	// Parental watch limits apply to live TV the same as to library playback.
	// Live viewing used to be entirely ungated: a profile blocked by its
	// allowed-hours window or over its daily budget just switched to the Live
	// TV tab. (Channels carry no content rating in the schema, so the rating
	// ceiling has nothing to key on here — the time-based policy is the gate.)
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		if watchLimitBlocks(w, r, h.watchLimit, h.logger, claims.UserID) {
			return
		}
	}

	session, err := h.proxy.Acquire(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, livetv.ErrAllTunersBusy):
			respond.Error(w, r, http.StatusServiceUnavailable,
				"ALL_TUNERS_BUSY", "all tuners are currently in use")
		case errors.Is(err, livetv.ErrNotFound), errors.Is(err, livetv.ErrChannelNotFound):
			respond.NotFound(w, r)
		default:
			h.logger.ErrorContext(r.Context(), "acquire hls session",
				"channel_id", id, "err", err)
			respond.InternalError(w, r)
		}
		return
	}
	defer h.proxy.Release(session)

	// Wait for ffmpeg to write the first playlist. Polling is fine — the
	// file appears within a couple hundred ms once the upstream locks.
	deadline := time.Now().Add(playlistMaxWait)
	for {
		data, err := os.ReadFile(session.PlaylistPath())
		if err == nil {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(rewriteLivePlaylistTokens(data, r.URL.Query().Get("token")))
			return
		}
		if !os.IsNotExist(err) {
			h.logger.ErrorContext(r.Context(), "read hls playlist",
				"channel_id", id, "err", err)
			respond.InternalError(w, r)
			return
		}
		if time.Now().After(deadline) {
			respond.Error(w, r, http.StatusGatewayTimeout,
				"STREAM_NOT_READY", "tuner did not produce a playlist in time")
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(playlistPollInterval):
		}
	}
}

// StreamSegment handles GET /api/v1/tv/channels/{id}/segments/{name}.
//
// Serves a TS segment from the active session's directory. Returns 404
// when the session isn't running (caller hasn't fetched the playlist
// yet) or when the segment has rolled out of the playlist window.
func (h *LiveTVHandler) StreamSegment(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	if h.proxy == nil {
		respond.Error(w, r, http.StatusServiceUnavailable,
			"LIVE_TV_NOT_CONFIGURED", "live TV streaming is not enabled")
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid channel id")
		return
	}
	name := chi.URLParam(r, "name")
	if !segmentNameRe.MatchString(name) {
		// Reject anything that doesn't match the ffmpeg output pattern.
		// In particular this blocks `..` traversal and absolute paths.
		respond.NotFound(w, r)
		return
	}

	// Same parental gate as the playlist — the playlist check alone only
	// covers session START; a viewer already mid-stream when their allowed
	// window closes must stop within the memo TTL, not at the next channel
	// change. Segment fetches are also the accrual signal: live TV counted
	// ZERO usage against the daily budget before this, no matter how long
	// the profile watched.
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		if watchLimitBlocks(w, r, h.watchLimit, h.logger, claims.UserID) {
			return
		}
		h.accrue.Tick(r.Context(), h.logger, claims.UserID)
	}

	// Look up (never create) the session, so we don't serve a segment from a
	// directory the proxy is concurrently tearing down. Lookup, not Acquire:
	// Acquire is create-if-missing, so a segment GET for a channel with no
	// live session used to tune a real tuner and spawn an ffmpeg transcode —
	// held for the full grace period — before 404ing anyway because the
	// segment file doesn't exist. A segment can only be legitimately fetched
	// after a playlist created the session, so a miss here is a genuine 404.
	session, ok := h.proxy.Lookup(id)
	if !ok {
		respond.NotFound(w, r)
		return
	}
	defer h.proxy.Release(session)

	path := session.SegmentPath(name)
	f, err := os.Open(path)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, time.Time{}, f)
}
