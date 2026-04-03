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
	"github.com/onscreen/onscreen/internal/config"
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

// SetSessionKiller wires the embedded worker so Stop can kill FFmpeg immediately.
func (h *NativeTranscodeHandler) SetSessionKiller(k SessionKiller) {
	h.killer = k
}

type transcodeStartRequest struct {
	FileID           *string `json:"file_id,omitempty"`
	Height           int     `json:"height"`              // 0 = no constraint (use source height)
	PositionMS       int64   `json:"position_ms"`         // start offset in ms
	VideoCopy        bool    `json:"video_copy"`          // true = copy video stream, only transcode audio
	AudioStreamIndex *int    `json:"audio_stream_index"`  // nil = default (first) audio stream
	SupportsHEVC     bool    `json:"supports_hevc"`       // client can decode HEVC (H.265) output
}

type transcodeStartResponse struct {
	SessionID   string `json:"session_id"`
	PlaylistURL string `json:"playlist_url"`
	Token       string `json:"token"`
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
		file = f
	} else {
		files, err := h.media.GetFiles(ctx, itemID)
		if err != nil || len(files) == 0 {
			respond.NotFound(w, r)
			return
		}
		file = &files[0] // already sorted best quality first
	}

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
	}
	if err := h.sessions.Create(ctx, sess); err != nil {
		h.logger.WarnContext(ctx, "create transcode session", "err", err)
		respond.InternalError(w, r)
		return
	}

	audioStreamIdx := -1 // -1 = default (let FFmpeg pick)
	if body.AudioStreamIndex != nil && *body.AudioStreamIndex >= 0 {
		audioStreamIdx = *body.AudioStreamIndex
	}

	isSourceHEVC := file.VideoCodec != nil && (strings.EqualFold(*file.VideoCodec, "hevc") || strings.EqualFold(*file.VideoCodec, "h265"))
	isSourceHDR := file.HDRType != nil && *file.HDRType != ""

	// Use HEVC output for 4K when client supports it — 40% bitrate savings.
	preferHEVC := body.SupportsHEVC && height >= 2160 && !body.VideoCopy

	// Scale bitrate down for HEVC efficiency (same visual quality at lower bitrate).
	jobBitrate := bitrateKbps
	if preferHEVC {
		jobBitrate = transcode.ScaleBitrateForHEVC(bitrateKbps)
	}

	job := transcode.TranscodeJob{
		SessionID:        sessionID,
		FilePath:         file.FilePath,
		SessionDir:       transcode.SessionDir(sessionID),
		StartOffsetSec:   float64(body.PositionMS) / 1000.0,
		Decision:         decision,
		Encoder:          encoder,
		Width:            width,
		Height:           height,
		BitrateKbps:      jobBitrate,
		AudioCodec:       "aac",
		AudioChannels:    2,
		AudioStreamIndex: audioStreamIdx,
		IsHEVC:           isSourceHEVC,
		NeedsToneMap:     isSourceHDR && !body.VideoCopy,
		PreferHEVC:       preferHEVC,
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
		"needs_tonemap", job.NeedsToneMap,
		"supports_hevc", body.SupportsHEVC,
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

	playlistURL := fmt.Sprintf("/api/v1/transcode/sessions/%s/playlist.m3u8?token=%s",
		sessionID, segTok)

	respond.Success(w, r, transcodeStartResponse{
		SessionID:   sessionID,
		PlaylistURL: playlistURL,
		Token:       segTok,
	})
}

// Stop handles DELETE /api/v1/transcode/sessions/{sid}.
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
		// Kill FFmpeg immediately if we have an embedded worker reference.
		// This prevents the process from writing to a directory we're about to remove.
		if h.killer != nil {
			h.killer.KillSession(sessionID)
		}
		// Revoke the segment token — prefer session-stored token, fall back to query param.
		revokeToken := sess.SegToken
		if revokeToken == "" {
			revokeToken = token
		}
		if revokeToken != "" {
			_ = h.segToken.Revoke(ctx, revokeToken)
		}
		_ = h.sessions.Delete(ctx, sessionID)
		// Sanitize sessionID before using in filesystem path.
		safeID := filepath.Base(sessionID)
		go func() { _ = os.RemoveAll(transcode.SessionDir(safeID)) }()
	}

	respond.NoContent(w)
}

// Playlist handles GET /api/v1/transcode/sessions/{sid}/playlist.m3u8.
// Validates the segment token, waits for FFmpeg, and serves the rewritten playlist.
func (h *NativeTranscodeHandler) Playlist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := chi.URLParam(r, "sid")
	token := r.URL.Query().Get("token")

	if _, _, err := h.segToken.Validate(ctx, token); err != nil {
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
	deadline := time.Now().Add(30 * time.Second)
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

	// Wait for at least 2 segments before serving the initial playlist so
	// HLS.js has enough buffer to survive its first playlist-poll interval (4 s).
	const seg1Name = "seg00001.ts"
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

	if _, _, err := h.segToken.Validate(ctx, token); err != nil {
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
		return io.ReadAll(resp.Body)
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
	w.Header().Set("Content-Type", "video/MP2T")
	_, _ = io.Copy(w, resp.Body)
}

// sanitizePathComponent strips directory traversal from a path component.
// Used on session IDs and segment names before joining into filesystem paths.
func sanitizePathComponent(s string) string {
	return filepath.Base(s)
}

// rewritePlaylist rewrites segment URIs in an HLS playlist to absolute API paths
// with the auth token embedded, so HLS.js can request them without extra config.
func rewritePlaylist(data []byte, sessionID, token string) []byte {
	var buf bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasSuffix(line, ".ts") && !strings.HasPrefix(line, "#") {
			name := filepath.Base(line)
			line = fmt.Sprintf("/api/v1/transcode/sessions/%s/seg/%s?token=%s",
				sessionID, name, token)
		}
		buf.WriteString(line + "\n")
	}
	return buf.Bytes()
}
