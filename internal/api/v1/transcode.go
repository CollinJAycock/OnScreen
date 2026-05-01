package v1

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

// workerClient is shared across all segment/playlist proxy requests to the
// worker's local segment HTTP server. Connection pooling avoids per-request
// TCP handshakes on the internal network.
var workerClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	},
}

// NativeTranscodeMediaService defines the media operations needed by the native transcode handler.
type NativeTranscodeMediaService interface {
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
	GetFile(ctx context.Context, id uuid.UUID) (*media.File, error)
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
}

// SessionKiller can kill an in-progress FFmpeg process for a session.
type SessionKiller interface {
	KillSession(sessionID string)
}

// NativeTranscodeHandler handles HLS transcoding for the native web player.
type NativeTranscodeHandler struct {
	sessions *transcode.SessionStore
	segToken *transcode.SegmentTokenManager
	media    NativeTranscodeMediaService
	access   LibraryAccessChecker
	audit    *audit.Logger
	cfg      *config.Config
	logger   *slog.Logger
	killer   SessionKiller // optional — set for embedded worker deployments
}

// NewNativeTranscodeHandler creates a NativeTranscodeHandler.
func NewNativeTranscodeHandler(
	sessions *transcode.SessionStore,
	segToken *transcode.SegmentTokenManager,
	media NativeTranscodeMediaService,
	cfg *config.Config,
	logger *slog.Logger,
) *NativeTranscodeHandler {
	return &NativeTranscodeHandler{
		sessions: sessions,
		segToken: segToken,
		media:    media,
		cfg:      cfg,
		logger:   logger,
	}
}

// WithLibraryAccess attaches per-library ACL enforcement to Start.
func (h *NativeTranscodeHandler) WithLibraryAccess(a LibraryAccessChecker) *NativeTranscodeHandler {
	h.access = a
	return h
}

// WithAudit attaches the audit logger so transcode session creation is recorded.
func (h *NativeTranscodeHandler) WithAudit(a *audit.Logger) *NativeTranscodeHandler {
	h.audit = a
	return h
}

// SetSessionKiller wires the embedded worker so Stop can kill FFmpeg immediately.
func (h *NativeTranscodeHandler) SetSessionKiller(k SessionKiller) {
	h.killer = k
}

type transcodeStartRequest struct {
	FileID           *string `json:"file_id,omitempty"`
	Height           int     `json:"height"`             // 0 = no constraint (use source height)
	PositionMS       int64   `json:"position_ms"`        // start offset in ms
	VideoCopy        bool    `json:"video_copy"`         // true = copy video stream, only transcode audio
	AudioStreamIndex *int    `json:"audio_stream_index"` // nil = default (first) audio stream
	SupportsHEVC     bool    `json:"supports_hevc"`      // client can decode HEVC (H.265) output
	SupportsAV1      bool    `json:"supports_av1"`       // client can decode AV1 output — used to auto-prefer AV1 re-encode for AV1 source files
}

type transcodeStartResponse struct {
	SessionID   string `json:"session_id"`
	PlaylistURL string `json:"playlist_url"`
	Token       string `json:"token"`
	// StartOffsetSec is the content position the stream content begins
	// at (keyframe-aligned). May be earlier than the requested
	// position_ms when video is being stream-copied — input-side -ss
	// snaps back to the previous keyframe. The client uses this for
	// its scrubber-time mapping so the UI matches what's on screen
	// instead of advertising an exact resume that the codec can't
	// honor.
	StartOffsetSec float64 `json:"start_offset_sec"`
	// Seg0AudioGapSec is how far into the stream (in seconds) the
	// player should seek on startup to skip silent video at the head
	// of segment 0. With AC3 → AAC re-encode after a mid-stream -ss,
	// the AAC encoder's first valid frame lands a few seconds after
	// video's first packet — starting playback at this offset gets
	// A/V synced from the first audible frame instead of showing
	// silent video while the audio pipeline warms up. Zero when no
	// gap was measurable (seg 0 still being written, probe failed,
	// or gap below the resolution threshold).
	Seg0AudioGapSec float64 `json:"seg0_audio_gap_sec"`
}

// Start handles POST /api/v1/items/{id}/transcode.
// Creates a transcode session and returns the HLS playlist URL with an auth token.
func (h *NativeTranscodeHandler) Start(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	itemID, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}

	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}

	var body transcodeStartRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}

	// Validate resolution: must be non-negative and within server caps.
	if body.Height < 0 {
		respond.BadRequest(w, r, "height must be non-negative")
		return
	}
	if body.Height > h.cfg.TranscodeMaxHeight {
		respond.BadRequest(w, r, fmt.Sprintf("height exceeds server maximum of %d", h.cfg.TranscodeMaxHeight))
		return
	}

	// Resolve the parent item up-front so we can enforce per-library ACL
	// and content-rating gates before spending any worker resources.
	item, err := h.media.GetItem(ctx, itemID)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	if h.access != nil {
		ok, aerr := h.access.CanAccessLibrary(ctx, claims.UserID, item.LibraryID, claims.IsAdmin)
		if aerr != nil {
			h.logger.ErrorContext(ctx, "transcode: library access check", "library_id", item.LibraryID, "err", aerr)
			respond.InternalError(w, r)
			return
		}
		if !ok {
			respond.NotFound(w, r)
			return
		}
	}
	if claims.MaxContentRating != "" {
		cr := ""
		if item.ContentRating != nil {
			cr = *item.ContentRating
		}
		if !contentrating.IsAllowed(cr, claims.MaxContentRating) {
			respond.Forbidden(w, r)
			return
		}
	}

	// Select the file to transcode.
	var file *media.File
	if body.FileID != nil && *body.FileID != "" {
		fid, err := uuid.Parse(*body.FileID)
		if err != nil {
			respond.BadRequest(w, r, "invalid file_id")
			return
		}
		f, err := h.media.GetFile(ctx, fid)
		if err != nil {
			respond.NotFound(w, r)
			return
		}
		// Reject cross-item file IDs — otherwise a caller can use an
		// allowed item ID to launch a session that streams a different,
		// disallowed file.
		if f.MediaItemID != itemID {
			respond.NotFound(w, r)
			return
		}
		file = f
	} else {
		files, err := h.media.GetFiles(ctx, itemID)
		if err != nil || len(files) == 0 {
			respond.NotFound(w, r)
			return
		}
		file = &files[0] // already sorted best quality first
	}

	// Last-writer-wins: if this user already has sessions running for this
	// item (typical when a phone takes over from a TV), kill them first so
	// we don't pile up GPU slots and orphan playlists.
	h.supersedeUserItem(ctx, claims.UserID, itemID)

	// Video-copy mode: remux video (no re-encode), only transcode audio.
	// Used when the source video is already browser-compatible (H.264) but the
	// audio codec or container is not.
	var encoder string
	var width, height, bitrateKbps int
	sourceW, sourceH := 0, 0
	if file.ResolutionW != nil {
		sourceW = *file.ResolutionW
	}
	if file.ResolutionH != nil {
		sourceH = *file.ResolutionH
	}

	if body.VideoCopy {
		encoder = "copy"
		// No resolution or bitrate needed — video passes through unchanged.
	} else {
		serverCaps := transcode.ServerCaps{
			MaxBitrateKbps: h.cfg.TranscodeMaxBitrate,
			MaxWidth:       h.cfg.TranscodeMaxWidth,
			MaxHeight:      h.cfg.TranscodeMaxHeight,
		}
		quality := transcode.SelectQuality(0, 0, body.Height, sourceW, sourceH, serverCaps)

		// SelectQuality leaves MaxWidth at the server cap when the client only
		// specified height. Recalculate it from the source aspect ratio so FFmpeg
		// produces a correctly-proportioned output instead of a padded ultrawide frame.
		if body.Height > 0 && sourceW > 0 && sourceH > 0 {
			ar := float64(sourceW) / float64(sourceH)
			w := int(math.Round(float64(quality.MaxHeight) * ar))
			if w%2 != 0 {
				w++ // encoders require even dimensions
			}
			if w > quality.MaxWidth {
				// Clamp to server width cap and recalculate height to match.
				w = quality.MaxWidth
				if w%2 != 0 {
					w--
				}
				hh := int(math.Round(float64(w) / ar))
				if hh%2 != 0 {
					hh++
				}
				quality.MaxHeight = hh
			}
			quality.MaxWidth = w
		}
		width = quality.MaxWidth
		height = quality.MaxHeight
		bitrateKbps = quality.Bitrate
	}

	sessionID := transcode.NewSessionID()
	segTok, err := h.segToken.Issue(ctx, sessionID, claims.UserID)
	if err != nil {
		h.logger.WarnContext(ctx, "issue seg token", "err", err)
		respond.InternalError(w, r)
		return
	}

	decision := "transcode"
	if body.VideoCopy {
		decision = "remux"
	}

	audioStreamIdx := -1 // -1 = default (let FFmpeg pick)
	if body.AudioStreamIndex != nil && *body.AudioStreamIndex >= 0 {
		audioStreamIdx = *body.AudioStreamIndex
	}

	isSourceHEVC := file.VideoCodec != nil && (strings.EqualFold(*file.VideoCodec, "hevc") || strings.EqualFold(*file.VideoCodec, "h265"))
	isSourceAV1 := file.VideoCodec != nil && strings.EqualFold(*file.VideoCodec, "av1")
	isSourceHDR := file.HDRType != nil && *file.HDRType != ""

	// Use HEVC output for 4K when client supports it — 40% bitrate savings.
	preferHEVC := body.SupportsHEVC && height >= 2160 && !body.VideoCopy
	// Auto-prefer AV1 output for AV1-source playback when the client supports it.
	// Avoids the AV1 → H.264 round-trip we'd otherwise do (any non-Auto quality
	// click on an AV1 source). The worker confirms an AV1 encoder is actually
	// active before honoring this; if not, it falls back to HEVC then H.264.
	// AV1 takes priority over HEVC at the worker — natural use case is "play
	// the AV1 source," and re-encoding AV1 → HEVC throws away the format
	// efficiency the source already paid for.
	preferAV1 := body.SupportsAV1 && isSourceAV1 && !body.VideoCopy

	// For remux, use the source file bitrate (video is copied unchanged).
	// For full transcode, use the target bitrate from quality selection.
	sessionBitrate := bitrateKbps
	if body.VideoCopy && file.Bitrate != nil {
		sessionBitrate = int(*file.Bitrate / 1000)
	}

	sess := transcode.Session{
		ID:          sessionID,
		UserID:      claims.UserID,
		MediaItemID: itemID,
		FileID:      file.ID,
		Decision:    decision,
		FilePath:    file.FilePath,
		PositionMS:  body.PositionMS,
		CreatedAt:   time.Now(),
		ClientName:  "OnScreenWeb",
		SegToken:    segTok,
		BitrateKbps: sessionBitrate,
		HEVCOutput:  preferHEVC,
		AV1Output:   preferAV1,
	}
	if h.audit != nil {
		actor := claims.UserID
		h.audit.Log(ctx, &actor, audit.ActionTranscodeStart, sessionID,
			map[string]any{
				"item_id":  itemID.String(),
				"file_id":  file.ID.String(),
				"decision": decision,
				"height":   height,
			}, audit.ClientIP(r))
	}
	if err := h.sessions.Create(ctx, sess); err != nil {
		h.logger.WarnContext(ctx, "create transcode session", "err", err)
		respond.InternalError(w, r)
		return
	}

	// Scale bitrate down for HEVC efficiency (same visual quality at lower bitrate).
	jobBitrate := bitrateKbps
	if preferHEVC {
		jobBitrate = transcode.ScaleBitrateForHEVC(bitrateKbps)
	}

	// For video-copy (remux), -ss INPUT lands on the keyframe at-or-before
	// the requested time, which can be 5–10 s earlier on sparse-GOP rips.
	// Probe the source so we know the real start time and can tell the
	// client to set its scrubber to match. Skip when offset is 0 (start
	// of file) or for full re-encode (decoder can land on any frame).
	requestedStartSec := float64(body.PositionMS) / 1000.0
	startOffsetSec := requestedStartSec
	if body.VideoCopy && startOffsetSec > 0 {
		startOffsetSec = transcode.FindPreviousKeyframe(ctx, file.FilePath, requestedStartSec)
	}

	job := transcode.TranscodeJob{
		SessionID:        sessionID,
		FilePath:         file.FilePath,
		SessionDir:       transcode.SessionDir(sessionID),
		StartOffsetSec:   startOffsetSec,
		Decision:         decision,
		Encoder:          encoder,
		Width:            width,
		Height:           height,
		BitrateKbps:      jobBitrate,
		AudioCodec:       "aac",
		AudioChannels:    2,
		AudioStreamIndex: audioStreamIdx,
		IsHEVC:           isSourceHEVC,
		IsAV1:            isSourceAV1,
		NeedsToneMap:     isSourceHDR && !body.VideoCopy,
		PreferHEVC:       preferHEVC,
		PreferAV1:        preferAV1,
		EnqueuedAt:       time.Now(),
	}
	h.logger.InfoContext(ctx, "transcode job created",
		"session_id", sessionID,
		"decision", decision,
		"requested_height", body.Height,
		"source_w", sourceW, "source_h", sourceH,
		"output_w", width, "output_h", height,
		"bitrate_kbps", jobBitrate,
		"prefer_hevc", preferHEVC,
		"prefer_av1", preferAV1,
		"source_codec", func() string {
			if file.VideoCodec != nil {
				return *file.VideoCodec
			}
			return ""
		}(),
		"needs_tonemap", job.NeedsToneMap,
		"supports_hevc", body.SupportsHEVC,
		"supports_av1", body.SupportsAV1,
	)

	workerAddr, err := h.sessions.DispatchJob(ctx, job)
	if err != nil {
		h.logger.ErrorContext(ctx, "dispatch transcode job failed", "session_id", sessionID, "err", err)
		// Clean up the orphaned Valkey session — FFmpeg will never start.
		_ = h.sessions.Delete(ctx, sessionID)
		respond.InternalError(w, r)
		return
	}
	if workerAddr != "" {
		h.logger.InfoContext(ctx, "dispatched to worker", "session_id", sessionID, "worker", workerAddr)
	}

	// For mid-stream video-copy sessions, the AAC encoder needs a few
	// seconds of warmup after the seek before its first valid frame
	// arrives — seg 0 then carries silent video at the head. Wait
	// for seg 0 to be finalized, measure the audio/video PTS gap,
	// and hand it to the client so the player skips the silent head
	// on startup. Capped so a stuck FFmpeg can't stall session
	// creation; on timeout we fall back to the bare keyframe offset
	// and the user sees the old silent-head behavior — the same as
	// before this probe landed, so it's a no-op not a regression.
	var seg0AudioGap float64
	if body.VideoCopy && startOffsetSec > 0 {
		if gap, ok := transcode.WaitForSeg0Audio(ctx, transcode.SessionDir(sessionID), 5*time.Second); ok {
			seg0AudioGap = gap
			h.logger.InfoContext(ctx, "seg 0 audio gap measured",
				"session_id", sessionID,
				"gap_sec", gap,
			)
		}
	}

	playlistURL := fmt.Sprintf("/api/v1/transcode/sessions/%s/playlist.m3u8?token=%s",
		sessionID, segTok)

	respond.Success(w, r, transcodeStartResponse{
		SessionID:       sessionID,
		PlaylistURL:     playlistURL,
		Token:           segTok,
		StartOffsetSec:  startOffsetSec,
		Seg0AudioGapSec: seg0AudioGap,
	})
}

// Stop handles DELETE /api/v1/transcode/sessions/{sid}.
//
// Auth: Bearer (user/admin), NOT the segment token. Asymmetric on
// purpose — Playlist + Segment use the seg-token because hls.js can't
// attach Authorization headers to fragment fetches, so we issue a
// short-lived per-session token bound to the URL. A leaked seg-token
// must not be enough to kill someone's session, so the control-plane
// (Start/Stop) requires the user's Bearer cookie/header. The `token`
// query param here is consulted only as a *fallback hint* by tearDown
// when revoking the seg-token, not for auth.
func (h *NativeTranscodeHandler) Stop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := chi.URLParam(r, "sid")
	token := r.URL.Query().Get("token")

	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	sess, err := h.sessions.Get(ctx, sessionID)
	if err == nil && sess != nil {
		if sess.UserID != claims.UserID {
			respond.Forbidden(w, r)
			return
		}
		h.tearDown(ctx, sess, token)
	}

	respond.NoContent(w)
}

// tearDown executes the kill-revoke-delete-rmrf cleanup for a single
// session. Called by Stop (explicit shutdown) and by the supersede path
// in Start (last-writer-wins). fallbackToken is only consulted when the
// session row has no SegToken stamped on it — older sessions sometimes
// carry the token only in the URL.
func (h *NativeTranscodeHandler) tearDown(ctx context.Context, sess *transcode.Session, fallbackToken string) {
	// Kill FFmpeg immediately if we have an embedded worker reference.
	// For remote workers, the Delete below triggers the worker's heartbeat
	// loop to notice the missing session and kill its own process.
	if h.killer != nil {
		h.killer.KillSession(sess.ID)
	}
	revokeToken := sess.SegToken
	if revokeToken == "" {
		revokeToken = fallbackToken
	}
	if revokeToken != "" {
		_ = h.segToken.Revoke(ctx, revokeToken)
	}
	_ = h.sessions.Delete(ctx, sess.ID)
	safeID := filepath.Base(sess.ID)
	// Delay the wipe so any in-flight client prefetches against the
	// just-killed session don't race the rm -rf and 404. The session
	// is already gone from Valkey and the seg token is revoked, so
	// new requests fail at auth — only requests already past auth at
	// this instant are at risk, and 30s is well past their lifetime.
	go func() {
		time.Sleep(30 * time.Second)
		_ = os.RemoveAll(transcode.SessionDir(safeID))
	}()
}

// supersedeUserItem kills any sessions the same user already has running
// for mediaItemID. Matches Plex/Jellyfin: starting playback on a new
// device implicitly stops the old one. Logged so operators can see why a
// player suddenly went dark when a phone took over.
func (h *NativeTranscodeHandler) supersedeUserItem(ctx context.Context, userID, mediaItemID uuid.UUID) {
	prior, err := h.sessions.ListByUserItem(ctx, userID, mediaItemID)
	if err != nil {
		// Best-effort — if the lookup fails the user just ends up with two
		// concurrent sessions, the same as the pre-supersede behavior. Log
		// and let Start proceed.
		h.logger.WarnContext(ctx, "supersede: list prior sessions",
			"user_id", userID, "item_id", mediaItemID, "err", err)
		return
	}
	for i := range prior {
		p := &prior[i]
		h.logger.InfoContext(ctx, "transcode: superseding prior session",
			"superseded_session", p.ID,
			"user_id", userID, "item_id", mediaItemID,
			"client_name", p.ClientName)
		if h.audit != nil {
			actor := userID
			h.audit.Log(ctx, &actor, audit.ActionTranscodeStop, p.ID,
				map[string]any{
					"item_id": mediaItemID.String(),
					"reason":  "superseded",
				}, "")
		}
		h.tearDown(ctx, p, "")
	}
}

// Playlist handles GET /api/v1/transcode/sessions/{sid}/playlist.m3u8.
// Validates the segment token, waits for FFmpeg, and serves the rewritten playlist.
func (h *NativeTranscodeHandler) Playlist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := chi.URLParam(r, "sid")
	token := r.URL.Query().Get("token")

	tokSession, _, err := h.segToken.Validate(ctx, token)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Bind the token to the requested session — otherwise a token issued for
	// session A would let the holder fetch any other session's playlist.
	if tokSession != sessionID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Sanitize sessionID to prevent path traversal.
	sessID := filepath.Base(sessionID)
	sessDir := transcode.SessionDir(sessID)

	// Resolve the worker address for this session. Once the worker claims the
	// job it stamps WorkerAddr on the session; we use it to proxy requests in
	// multi-instance deployments. In single-instance mode WorkerAddr is still
	// set (to the embedded worker's loopback address), so proxying is used
	// universally and local-disk fallback is only a last resort.
	var workerAddr string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if sess, err := h.sessions.Get(ctx, sessionID); err == nil && sess.WorkerAddr != "" {
			workerAddr = sess.WorkerAddr
			break
		}
		select {
		case <-ctx.Done():
			http.Error(w, "request cancelled", http.StatusServiceUnavailable)
			return
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Determine segment file shape from session metadata. HEVC and AV1
	// output use fMP4 (.m4s); MPEG-TS sessions use .ts. Both layouts
	// are single-rendition muxed (audio + video in one segment file),
	// so the only difference is the file extension.
	seg1Name := "seg00001.ts"
	if sess, err := h.sessions.Get(ctx, sessionID); err == nil && (sess.HEVCOutput || sess.AV1Output) {
		seg1Name = "seg00001.m4s"
	}

	// Wait for at least 2 segments before serving the initial playlist so
	// HLS.js has enough buffer to survive its first playlist-poll interval (4 s).
	for time.Now().Before(deadline) {
		if workerReady(ctx, workerAddr, sessID, seg1Name, filepath.Join(sessDir, seg1Name)) {
			break
		}
		select {
		case <-ctx.Done():
			http.Error(w, "request cancelled", http.StatusServiceUnavailable)
			return
		case <-time.After(100 * time.Millisecond):
		}
	}

	data, err := fetchFromWorker(ctx, workerAddr, sessID, "index.m3u8", filepath.Join(sessDir, "index.m3u8"))
	if err != nil {
		http.Error(w, "playlist not ready", http.StatusServiceUnavailable)
		return
	}

	rewritten := rewritePlaylist(data, sessionID, token)
	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write(rewritten)
}

// Segment handles GET /api/v1/transcode/sessions/{sid}/seg/{name}.
func (h *NativeTranscodeHandler) Segment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := chi.URLParam(r, "sid")
	segName := chi.URLParam(r, "name")
	token := r.URL.Query().Get("token")

	tokSession, _, err := h.segToken.Validate(ctx, token)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if tokSession != sessionID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Prevent path traversal — only allow the bare filename (no slashes or ..).
	segName = filepath.Base(segName)
	if segName == "." || segName == ".." {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	sessID := filepath.Base(sessionID)
	sessDir := transcode.SessionDir(sessID)
	localPath := filepath.Join(sessDir, segName)

	// Look up the worker that owns this session and proxy to its segment server.
	var workerAddr string
	if sess, err := h.sessions.Get(ctx, sessionID); err == nil {
		workerAddr = sess.WorkerAddr
	}

	if workerAddr != "" {
		proxyWorkerFile(w, r, workerAddr, sessID, segName)
		return
	}
	// Fallback: local disk (worker not yet stamped or same-machine embedded worker).
	http.ServeFile(w, r, localPath)
}

// workerReady returns true if the named file is available — either via a HEAD
// request to the worker's segment server or by stat-ing the local path.
func workerReady(ctx context.Context, workerAddr, sessID, name, localPath string) bool {
	if workerAddr != "" {
		url := fmt.Sprintf("http://%s/segments/%s/%s", workerAddr, sessID, name)
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
		if err != nil {
			return false
		}
		resp, err := workerClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	_, err := os.Stat(localPath)
	return err == nil
}

// maxPlaylistBytes caps the playlist/metadata body read from the worker. Real
// HLS playlists are well under 100 KB; this prevents a compromised or buggy
// worker from exhausting memory on the API process.
const maxPlaylistBytes = 5 << 20 // 5 MiB

// fetchFromWorker retrieves file content from the worker's segment server or
// falls back to reading from the local filesystem.
func fetchFromWorker(ctx context.Context, workerAddr, sessID, name, localPath string) ([]byte, error) {
	if workerAddr != "" {
		url := fmt.Sprintf("http://%s/segments/%s/%s", workerAddr, sessID, name)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := workerClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(io.LimitReader(resp.Body, maxPlaylistBytes))
	}
	return os.ReadFile(localPath)
}

// proxyWorkerFile streams a file from the worker's segment HTTP server to the
// client. Used for segment (.ts) requests in multi-instance deployments.
func proxyWorkerFile(w http.ResponseWriter, r *http.Request, workerAddr, sessID, name string) {
	url := fmt.Sprintf("http://%s/segments/%s/%s", workerAddr, sessID, name)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	resp, err := workerClient.Do(req)
	if err != nil {
		http.Error(w, "segment unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "segment not found", resp.StatusCode)
		return
	}
	// Set Content-Type based on segment format.
	ct := "video/MP2T"
	if strings.HasSuffix(name, ".m4s") || strings.HasSuffix(name, ".mp4") {
		ct = "video/mp4"
	}
	w.Header().Set("Content-Type", ct)
	_, _ = io.Copy(w, resp.Body)
}

// sanitizePathComponent strips directory traversal from a path component.
// Used on session IDs and segment names before joining into filesystem paths.
// Normalizes backslashes to forward slashes so Windows-style traversal
// attempts are caught on non-Windows hosts.
func sanitizePathComponent(s string) string {
	return filepath.Base(strings.ReplaceAll(s, `\`, `/`))
}

// rewritePlaylist rewrites segment URIs in an HLS playlist to absolute API paths
// with the auth token embedded, so HLS.js can request them without extra config.
// Handles both MPEG-TS (.ts) and fMP4 (.m4s / init.mp4) segment references.
func rewritePlaylist(data []byte, sessionID, token string) []byte {
	var buf bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		// Rewrite segment file references: .ts (MPEG-TS) and .m4s/.mp4 (fMP4).
		if !strings.HasPrefix(line, "#") &&
			(strings.HasSuffix(line, ".ts") || strings.HasSuffix(line, ".m4s") || strings.HasSuffix(line, ".mp4")) {
			name := filepath.Base(line)
			line = fmt.Sprintf("/api/v1/transcode/sessions/%s/seg/%s?token=%s",
				sessionID, name, token)
		}
		// Rewrite #EXT-X-MAP URI (fMP4 init segment).
		if strings.HasPrefix(line, "#EXT-X-MAP:URI=") {
			// Format: #EXT-X-MAP:URI="init.mp4"
			name := strings.TrimPrefix(line, "#EXT-X-MAP:URI=")
			name = strings.Trim(name, "\"")
			name = filepath.Base(name)
			line = fmt.Sprintf("#EXT-X-MAP:URI=\"/api/v1/transcode/sessions/%s/seg/%s?token=%s\"",
				sessionID, name, token)
		}
		buf.WriteString(line + "\n")
	}
	return buf.Bytes()
}

