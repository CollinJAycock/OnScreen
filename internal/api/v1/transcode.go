package v1

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// NativeTranscodeMediaService defines the media operations needed by the native transcode handler.
type NativeTranscodeMediaService interface {
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
	GetFile(ctx context.Context, id uuid.UUID) (*media.File, error)
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
}

// NativeTranscodeHandler handles HLS transcoding for the native web player.
type NativeTranscodeHandler struct {
	sessions *transcode.SessionStore
	segToken *transcode.SegmentTokenManager
	media    NativeTranscodeMediaService
	cfg      *config.Config
	logger   *slog.Logger
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

type transcodeStartRequest struct {
	FileID     *string `json:"file_id,omitempty"`
	Height     int     `json:"height"`      // 0 = no constraint (use source height)
	PositionMS int64   `json:"position_ms"` // start offset in ms
	VideoCopy  bool    `json:"video_copy"`  // true = copy video stream, only transcode audio
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

	if body.VideoCopy {
		encoder = "copy"
		// No resolution or bitrate needed — video passes through unchanged.
	} else {
		serverCaps := transcode.ServerCaps{
			MaxBitrateKbps: h.cfg.TranscodeMaxBitrate,
			MaxWidth:       h.cfg.TranscodeMaxWidth,
			MaxHeight:      h.cfg.TranscodeMaxHeight,
		}
		sourceW, sourceH := 0, 0
		if file.ResolutionW != nil {
			sourceW = *file.ResolutionW
		}
		if file.ResolutionH != nil {
			sourceH = *file.ResolutionH
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
	}
	if err := h.sessions.Create(ctx, sess); err != nil {
		h.logger.WarnContext(ctx, "create transcode session", "err", err)
		respond.InternalError(w, r)
		return
	}

	job := transcode.TranscodeJob{
		SessionID:      sessionID,
		FilePath:       file.FilePath,
		SessionDir:     transcode.SessionDir(sessionID),
		StartOffsetSec: float64(body.PositionMS) / 1000.0,
		Decision:       decision,
		Encoder:        encoder,
		Width:          width,
		Height:         height,
		BitrateKbps:    bitrateKbps,
		AudioCodec:     "aac",
		AudioChannels:  2,
		EnqueuedAt:     time.Now(),
	}
	if err := h.sessions.EnqueueJob(ctx, job); err != nil {
		h.logger.ErrorContext(ctx, "enqueue transcode job failed", "session_id", sessionID, "err", err)
		// Clean up the orphaned Valkey session — FFmpeg will never start.
		_ = h.sessions.Delete(ctx, sessionID)
		respond.InternalError(w, r)
		return
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
	sessDir := transcode.SessionDir(filepath.Base(sessionID))
	playlistPath := filepath.Join(sessDir, "index.m3u8")

	// Wait up to 10s for FFmpeg to produce at least 2 segments before serving
	// the initial playlist. One segment = 4 s of content; HLS.js polls the playlist
	// every targetDuration (4 s), so a single-segment playlist causes HLS.js to
	// exhaust its buffer exactly at the first poll boundary, producing a visible stall.
	// At remux speed (≥20× real-time) the second segment appears within ~0.4 s, so
	// this adds negligible startup latency. On subsequent polls (playlist already
	// served) we return immediately without waiting.
	seg1Path := filepath.Join(sessDir, "seg00001.ts")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(seg1Path); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			http.Error(w, "request cancelled", http.StatusServiceUnavailable)
			return
		case <-time.After(100 * time.Millisecond):
		}
	}

	data, err := os.ReadFile(playlistPath)
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

	sessDir := transcode.SessionDir(filepath.Base(sessionID))
	segPath := filepath.Join(sessDir, segName)
	http.ServeFile(w, r, segPath)
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
