package v1

import (
	"context"
	"errors"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/livetv"
)

// LiveTVStreamProxy is the slice of HLSProxy the stream handlers use.
// Kept narrow so the handler can be tested with a stub.
type LiveTVStreamProxy interface {
	Acquire(ctx context.Context, channelID uuid.UUID) (*livetv.HLSSession, error)
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
			w.Write(data)
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

	// Acquire+release the session so we don't serve a segment from a
	// directory the proxy is concurrently tearing down. Cheap because the
	// session almost certainly already exists — playlist fetch came first.
	session, err := h.proxy.Acquire(r.Context(), id)
	if err != nil {
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

