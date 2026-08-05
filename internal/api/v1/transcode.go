package v1

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/watchlimit"
	"github.com/onscreen/onscreen/internal/mediastore"
	"github.com/onscreen/onscreen/internal/scanner"
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

// StreamCaps holds a user's admin-set streaming ceilings. Zero means "no cap"
// (fall back to the server-wide default).
type StreamCaps struct {
	ConcurrentStreams int
	BitrateKbps       int
}

// UserStreamCapsReader reads a user's admin-set streaming caps. Optional on the
// handler (WithUserCaps); nil = no per-user caps, so the server-wide defaults
// apply. Read at transcode-start (not from the token) so an admin change takes
// effect on the user's next stream.
type UserStreamCapsReader interface {
	GetStreamCaps(ctx context.Context, userID uuid.UUID) (StreamCaps, error)
}

// NativeTranscodeHandler handles HLS transcoding for the native web player.
type NativeTranscodeHandler struct {
	sessions   *transcode.SessionStore
	segToken   *transcode.SegmentTokenManager
	media      NativeTranscodeMediaService
	access     LibraryAccessChecker
	watchLimit ItemWatchLimit // optional; when set, Start blocks playback that's outside allowed hours / past the daily cap
	audit      *audit.Logger
	cfg        *config.Config
	logger     *slog.Logger
	killer     SessionKiller        // optional — set for embedded worker deployments
	userCaps   UserStreamCapsReader // optional — per-user admin concurrent/bitrate caps
	// tokens mints the per-file stream token embedded in a job's SourceURL
	// so a remote worker without shared storage can pull the source over
	// HTTP. Optional — when nil, jobs carry no SourceURL and workers must
	// reach the file on a local/shared path.
	tokens *auth.TokenMaker
	// store is the media-byte backend. Optional — when nil, mediaStore()
	// defaults to mediastore.Local, whose SignedURL returns "" (can't offload),
	// so buildSourceURL falls through to the LAN stream-token URL exactly as
	// before. An object-storage backend returns a presigned bucket/CDN URL the
	// worker reads source from directly, off the app tier (HA roadmap §3).
	store mediastore.Store

	// verifySource is the pre-flight source-readability check called from
	// Start before the session is dispatched. Defaults to
	// scanner.VerifySource; tests inject a no-op so they can exercise the
	// session/dispatch path without a real on-disk file.
	verifySource func(ctx context.Context, path string) (scanner.SourceStatus, error)

	// probe overrides scanner.ProbeFile for lazyReprobe. Tests inject canned
	// results; nil uses the real ffprobe.
	probe func(ctx context.Context, path string) (*scanner.ProbeResult, error)

	// staticEnabled + staticRoot serve pre-encoded ("static") ABR ladders from
	// the media store when one exists, instead of starting a live session
	// (HA roadmap §5). staticRoot is the key prefix (STATIC_ABR_ROOT) and must
	// match the encoder's. Off unless WithStaticABR is called.
	staticEnabled bool
	staticRoot    string
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

// LibraryAccessWired reports whether the library-ACL checker is wired.
// A nil checker fails OPEN (serves every library), so api.Handlers.
// ValidateLibraryAccess asserts this at startup and refuses to boot
// without it.
func (h *NativeTranscodeHandler) LibraryAccessWired() bool { return h.access != nil }

// WithUserCaps attaches the per-user streaming-cap reader. When nil, Start uses
// only the server-wide concurrent + bitrate limits.
func (h *NativeTranscodeHandler) WithUserCaps(c UserStreamCapsReader) *NativeTranscodeHandler {
	h.userCaps = c
	return h
}

// WithAudit attaches the audit logger so transcode session creation is recorded.
func (h *NativeTranscodeHandler) WithAudit(a *audit.Logger) *NativeTranscodeHandler {
	h.audit = a
	return h
}

// WithWatchLimit attaches the parental watch-limit store so Start refuses to
// spin up a session when the user is outside their allowed hours or past their
// daily cap. nil = no enforcement.
// Memo-wrapped for the same reason as ItemHandler.WithWatchLimit: the static-ABR
// gate now runs on rung playlists and segments, which are hot.
func (h *NativeTranscodeHandler) WithWatchLimit(wl ItemWatchLimit) *NativeTranscodeHandler {
	h.watchLimit = newWatchLimitMemo(wl)
	return h
}

// SetSessionKiller wires the embedded worker so Stop can kill FFmpeg immediately.
func (h *NativeTranscodeHandler) SetSessionKiller(k SessionKiller) {
	h.killer = k
}

// WithStreamTokenMaker wires the token maker used to mint a job's HTTP
// source URL (a stream token bound to the source file). Without it, jobs
// carry no SourceURL and remote workers must reach the file on a shared
// path. Returns the handler for chaining.
func (h *NativeTranscodeHandler) WithStreamTokenMaker(m *auth.TokenMaker) *NativeTranscodeHandler {
	h.tokens = m
	return h
}

// WithMediaStore attaches the media-byte backend buildSourceURL consults to
// offload source reads to object storage / a CDN. When nil, mediaStore() falls
// back to mediastore.Local (returns "" from SignedURL), so single-node and
// shared-storage deployments behave exactly as before — opt-in and non-breaking.
func (h *NativeTranscodeHandler) WithMediaStore(s mediastore.Store) *NativeTranscodeHandler {
	h.store = s
	return h
}

// mediaStore returns the configured backend, defaulting to local-filesystem
// serving when none is wired.
func (h *NativeTranscodeHandler) mediaStore() mediastore.Store {
	if h.store == nil {
		return mediastore.Local{}
	}
	return h.store
}

// WithStaticABR enables serving pre-encoded ABR ladders from the media store.
// root is the static key prefix (STATIC_ABR_ROOT), matching the encoder's. When
// unset, the handler always serves live ABR. Returns the handler for chaining.
func (h *NativeTranscodeHandler) WithStaticABR(root string) *NativeTranscodeHandler {
	h.staticEnabled = true
	h.staticRoot = root
	return h
}

// WithVerifySource overrides the pre-flight source-readability check called
// from Start. Tests use this to skip the real ffprobe (their file paths
// don't exist on disk); production wiring leaves it nil and the handler
// uses scanner.VerifySource.
func (h *NativeTranscodeHandler) WithVerifySource(fn func(ctx context.Context, path string) (scanner.SourceStatus, error)) *NativeTranscodeHandler {
	h.verifySource = fn
	return h
}

// buildSourceURL returns an HTTP URL a worker can read the source from
// when it can't reach the file on its own filesystem: this server's
// /media/stream/{file_id} with a 24 h stream token bound to that file.
// One token per playback session — startABR builds it once on the parent
// and every rung child reuses it (all rungs read the same source file).
// Returns "" when no token maker is wired or no LAN IP is detectable, in
// which case the worker falls back to the local FilePath.
//
// srcKey is the media store key for the source (today the absolute FilePath).
// When the store can offload (object storage / CDN), its presigned URL is
// preferred over the LAN stream-token URL — the worker then reads source
// straight from the bucket/CDN, off the app tier. The Local backend returns ""
// (can't offload), so single-node and shared-storage deployments fall through to
// the existing LAN-token path unchanged.
func (h *NativeTranscodeHandler) buildSourceURL(ctx context.Context, claims *auth.Claims, fileID uuid.UUID, srcKey string) string {
	if signed, err := h.mediaStore().SignedURL(ctx, srcKey, auth.StreamTokenTTL); err == nil && signed != "" {
		return signed
	}

	if h.tokens == nil || claims == nil {
		return ""
	}
	lan := detectLANIP()
	if lan == "" {
		return ""
	}
	tok, err := h.tokens.IssueStreamToken(*claims, fileID)
	if err != nil {
		h.logger.Warn("build source url: issue stream token", "err", err)
		return ""
	}
	port := "7070"
	if _, p, err := net.SplitHostPort(h.cfg.ListenAddr); err == nil && p != "" {
		port = p
	}
	return fmt.Sprintf("http://%s:%s/media/stream/%s?token=%s",
		lan, port, fileID, url.QueryEscape(tok))
}

type transcodeStartRequest struct {
	FileID           *string `json:"file_id,omitempty"`
	Height           int     `json:"height"`             // 0 = no constraint (use source height)
	PositionMS       int64   `json:"position_ms"`        // start offset in ms
	VideoCopy        bool    `json:"video_copy"`         // true = copy video stream, only transcode audio
	AudioStreamIndex *int    `json:"audio_stream_index"` // nil = default (first) audio stream
	// SupportsHEVC/SupportsAV1 are TRI-STATE: absent (nil) defers to the
	// X-Client-Capabilities header; an explicit value overrides the header in
	// BOTH directions. Explicit false matters — it's how a client reports a
	// codec its own capability probe claimed but a real decode disproved
	// (Windows Chrome answers isTypeSupported=true for HEVC without the HEVC
	// Video Extensions installed, then rejects the append). See clientCaps.
	SupportsHEVC *bool `json:"supports_hevc"` // client can decode HEVC (H.265) output
	SupportsAV1  *bool `json:"supports_av1"`  // client can decode AV1 output — used to auto-prefer AV1 re-encode for AV1 source files
	// MaxAudioChannels caps the transcoded AAC channel count. nil/0 preserves
	// the source layout (5.1/7.1 stays multichannel); a stereo-only client can
	// send 2 to force a downmix.
	MaxAudioChannels *int `json:"max_audio_channels,omitempty"`
}

// clientCaps resolves the effective client playback capabilities for a transcode
// request. The source of truth is the X-Client-Capabilities header (parsed into a
// transcode.ClientCapabilities — codecs, channels, resolution, bit-depth); the
// per-request booleans (supports_hevc/av1, max_audio_channels) are tri-state
// refinements: absent defers to the header (so header-only clients and clients
// that haven't migrated off the booleans both keep today's behavior), present
// wins in both directions. See docs/capability-profiles.md.
func clientCaps(r *http.Request, body transcodeStartRequest) (caps transcode.ClientCapabilities, hasProfile bool) {
	raw := r.Header.Get("X-Client-Capabilities")
	caps = transcode.ParseCapabilities(raw)
	// An explicit body boolean overrides the header — INCLUDING downgrades.
	// The header is memoized from the client's capability probe, and probes
	// lie: Windows Chrome enumerates a platform HEVC decoder and reports
	// isTypeSupported=true with the HEVC Video Extensions missing, then MSE
	// rejects the actual append. When the player proves a codec undecodable it
	// re-requests with supports_hevc=false — under the old OR-in ("never
	// downgrade a header claim") that demotion was discarded, preferHEVC stayed
	// on, and the fallback transcode came back as the very codec that had just
	// failed. Absent (nil) still means "header stands", so no-opinion clients
	// lose nothing.
	if body.SupportsHEVC != nil {
		caps.SupportsHEVC = *body.SupportsHEVC
	}
	if body.SupportsAV1 != nil {
		caps.SupportsAV1 = *body.SupportsAV1
	}
	// An explicit per-request channel cap (legacy clients, test injection) wins.
	if body.MaxAudioChannels != nil && *body.MaxAudioChannels > 0 {
		caps.MaxAudioChannels = *body.MaxAudioChannels
	}
	return caps, raw != ""
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

	// Parental watch-limit gate — refuse to start a session outside the
	// user's allowed hours or past their daily cap, before spending any
	// worker resources. Fail-open on a store error (best-effort cap, not a
	// paywall); the progress heartbeat re-checks during playback anyway.
	if h.watchLimit != nil {
		now := time.Now()
		if policy, perr := h.watchLimit.GetPolicy(ctx, claims.UserID); perr != nil {
			h.logger.WarnContext(ctx, "transcode: watch-limit get policy", "err", perr)
		} else if policy.Restricted() {
			used, _ := h.watchLimit.TodayUsageSeconds(ctx, claims.UserID, watchlimit.LocalDay(now))
			if allowed, reason := watchlimit.Evaluate(policy, used, now); !allowed {
				respond.Error(w, r, http.StatusForbidden, "PARENTAL_LIMIT", reason)
				return
			}
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

	// Pre-flight source verification — bounds the "spinner forever" case
	// where ffmpeg would otherwise hang trying to demux a corrupt or
	// missing file. Catches the bad input in ~1 s with a structured error
	// code the player surfaces as "This file can't be played" instead of
	// the playlist endpoint's 60 s deadline → infinite-retry spinner.
	// Cheap on healthy files (~200-500 ms; see scanner.VerifySource).
	verify := h.verifySource
	if verify == nil {
		verify = scanner.VerifySource
	}
	if status, verr := verify(ctx, file.FilePath); status != scanner.SourceOK {
		switch status {
		case scanner.SourceMissing:
			h.logger.WarnContext(ctx, "transcode: source file missing",
				"file_id", file.ID, "path", file.FilePath, "err", verr)
			respond.Error(w, r, http.StatusUnprocessableEntity, "SOURCE_MISSING",
				"The file for this title is missing on disk. Re-scan the library to refresh.")
			return
		case scanner.SourceUnreadable:
			h.logger.WarnContext(ctx, "transcode: source file unreadable",
				"file_id", file.ID, "path", file.FilePath, "err", verr)
			respond.Error(w, r, http.StatusUnprocessableEntity, "SOURCE_UNREADABLE",
				"This file appears to be corrupt — the server couldn't read its container. Re-encode or replace the file.")
			return
		case scanner.SourceOK:
			// Caught by the outer `!= SourceOK` guard already; the case
			// label keeps exhaustive lint happy.
		}
	}

	// Last-writer-wins: if this user already has sessions running for this
	// item (typical when a phone takes over from a TV), kill them first so
	// we don't pile up GPU slots and orphan playlists.
	h.supersedeUserItem(ctx, claims.UserID, itemID)

	// Per-user concurrent-session cap. supersedeUserItem only kills
	// sessions for the SAME (user, item); a script that POSTs Start
	// repeatedly with different item IDs sails past it and accumulates
	// up to AuthLimit's 10/min × sessionTTL's 4 h = 2400 simultaneous
	// ffmpeg jobs per account. Each one pins a GPU slot and writes
	// segments to disk at real-time. Cap at 5 concurrent sessions per
	// user — 4 devices in a household plus a small slack — and reject
	// further Start requests with 429 until the user tears one down.
	// Per-user admin caps (concurrent streams + bitrate). Read from the DB so an
	// admin change applies on the next stream; zero / error = fall back to the
	// server-wide defaults (5 sessions, server bitrate ceiling).
	var streamCaps StreamCaps
	if h.userCaps != nil {
		streamCaps, _ = h.userCaps.GetStreamCaps(ctx, claims.UserID)
	}
	maxSessionsPerUser := 5
	if streamCaps.ConcurrentStreams > 0 {
		maxSessionsPerUser = streamCaps.ConcurrentStreams
	}
	if active, err := h.sessions.CountByUser(ctx, claims.UserID); err == nil && active >= maxSessionsPerUser {
		h.logger.WarnContext(ctx, "per-user transcode session cap reached",
			"user_id", claims.UserID, "active", active, "cap", maxSessionsPerUser)
		respond.Error(w, r, http.StatusTooManyRequests, "TOO_MANY_SESSIONS",
			fmt.Sprintf("you already have %d active streams (cap %d); stop one before starting another",
				active, maxSessionsPerUser))
		return
	}

	// Lazy re-probe: scan-time probesize/analyzeduration limits or a
	// transient I/O blip during the initial scan can leave a file row
	// with nil VideoCodec / ResolutionW / ResolutionH / HDRType. Without
	// source dimensions and codec the planner can't pick HW decode or
	// emit the correct tonemap chain — the resulting transcode either
	// stalls (mis-sized canvas, no scale) or produces HDR-tagged SDR
	// pixels. Re-probe synchronously on the transcode-start path; cheap
	// for healthy files (probe completes in <500 ms) and only fires when
	// the row is actually missing data.
	h.lazyReprobe(ctx, file)

	// Dolby Vision gate: refuse rather than serve a broken stream. The only
	// correct DV tonemapper (libplacebo) can't init on the deployment host and
	// tonemap_cuda wrecks the colors (docs/dolby-vision.md), so a DV title can't
	// be transcoded correctly here. Return a clear error the client shows as
	// "Dolby Vision is not supported". Honors a DV-capable client (dovi=1), which
	// would direct-play and not hit transcode-start anyway. This mirrors the
	// transcode.Decide DecisionUnsupported verdict the playback-decision endpoint
	// returns, so the client refusing up-front and a client that skips the
	// decision both end at the same clear message.
	if file.HDRType != nil && strings.EqualFold(*file.HDRType, "dolby_vision") {
		if dvCaps, _ := clientCaps(r, body); !dvCaps.SupportsDV {
			h.logger.InfoContext(ctx, "transcode: refusing Dolby Vision (unsupported)",
				"item_id", itemID, "file_id", file.ID)
			respond.Error(w, r, http.StatusUnsupportedMediaType,
				"DOLBY_VISION_UNSUPPORTED", "Dolby Vision is not supported")
			return
		}
	}

	// Serve a pre-encoded ("static") ABR ladder when one exists for this file —
	// the player gets a master playlist whose segments come from object storage /
	// CDN, so no live session or ffmpeg is spent (HA roadmap §5). Only when ABR is
	// enabled (the static ladder is adaptive) and not a video-copy/remux request.
	if h.staticEnabled && h.cfg.TranscodeABR && !body.VideoCopy &&
		file.FileHash != nil && h.staticAvailable(ctx, file.ID, *file.FileHash) {
		token := ""
		if h.tokens != nil {
			if t, terr := h.tokens.IssueStreamToken(*claims, file.ID); terr == nil {
				token = t
			}
		}
		h.logger.InfoContext(ctx, "transcode: serving static ABR ladder", "file_id", file.ID)
		// respond.Success, NOT respond.JSON: every client parses Start's reply
		// through the standard {"data": ...} envelope, and this path skipped
		// it — so whenever a pre-encoded ladder existed, the client read
		// `undefined` for playlist_url and playback failed on exactly the
		// files that had been optimized ahead of time.
		respond.Success(w, r, transcodeStartResponse{
			PlaylistURL: fmt.Sprintf("/api/v1/transcode/static/%s/master.m3u8%s", file.ID, tokenQuery(token)),
			Token:       token,
		})
		return
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

	// Per-user bitrate cap: a direct-play (copy) whose source exceeds the ceiling
	// is forced into a clamped transcode so a guest can't sail past their cap;
	// transcodes are clamped to it below.
	if streamCaps.BitrateKbps > 0 && body.VideoCopy && file.Bitrate != nil &&
		int(*file.Bitrate/1000) > streamCaps.BitrateKbps {
		body.VideoCopy = false
	}

	// Resolve the client's playback capabilities BEFORE quality selection: the
	// header's maxWidth/maxHeight must constrain the OUTPUT. They used to be
	// parsed after the resolution was already chosen, so a client declaring
	// maxheight=1080 forced the transcode verdict (Decide checks the caps) and
	// then received the same 4K output it declared it couldn't display.
	caps, hasProfile := clientCaps(r, body)
	h.logger.DebugContext(ctx, "resolved client capabilities",
		"profile_header", hasProfile,
		"supports_hevc", caps.SupportsHEVC, "supports_av1", caps.SupportsAV1,
		"max_audio_channels", caps.MaxAudioChannels,
		"max_w", caps.MaxWidth, "max_h", caps.MaxHeight, "max_bit_depth", caps.MaxVideoBitDepth)
	// Echo the resolved profile back so it's debuggable without log access
	// (curl -i / devtools): confirms exactly what the server thinks the client
	// can play. Set before any body write.
	w.Header().Set("X-OnScreen-Client-Caps", fmt.Sprintf(
		"profile=%t;hevc=%t;av1=%t;maxAudioChannels=%d;maxW=%d;maxH=%d;maxBitDepth=%d",
		hasProfile, caps.SupportsHEVC, caps.SupportsAV1, caps.MaxAudioChannels,
		caps.MaxWidth, caps.MaxHeight, caps.MaxVideoBitDepth))

	if body.VideoCopy {
		encoder = "copy"
		// No resolution or bitrate needed — video passes through unchanged.
	} else {
		maxBitrate := h.cfg.TranscodeMaxBitrate
		if streamCaps.BitrateKbps > 0 && streamCaps.BitrateKbps < maxBitrate {
			maxBitrate = streamCaps.BitrateKbps
		}
		serverCaps := transcode.ServerCaps{
			MaxBitrateKbps: maxBitrate,
			MaxWidth:       h.cfg.TranscodeMaxWidth,
			MaxHeight:      h.cfg.TranscodeMaxHeight,
		}
		// The client's requested height (explicit quality pick) and its
		// declared display ceiling both bound the output; SelectQuality mins
		// them against the server caps and the source.
		reqHeight := body.Height
		if caps.MaxHeight > 0 && (reqHeight == 0 || reqHeight > caps.MaxHeight) {
			reqHeight = caps.MaxHeight
		}
		quality := transcode.SelectQuality(0, caps.MaxWidth, reqHeight, sourceW, sourceH, serverCaps)

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

	// HTTP source fallback for remote workers without shared storage (see
	// buildSourceURL). Minted once and reused by both the ABR ladder (every
	// rung child) and the single-rendition job below — one stream token per
	// playback session regardless of ladder size.
	sourceURL := h.buildSourceURL(ctx, claims, file.ID, file.FilePath)

	decision := "transcode"
	if body.VideoCopy {
		decision = "remux"
	}

	audioStreamIdx := -1 // -1 = default (let FFmpeg pick)
	if body.AudioStreamIndex != nil && *body.AudioStreamIndex >= 0 {
		audioStreamIdx = *body.AudioStreamIndex
	}

	// (Client capabilities were resolved above, before quality selection.)

	// AAC output channel count: preserve the source layout (5.1/7.1) instead of
	// always downmixing to stereo, capped by the client's declared maximum (5.1
	// default). Used by both the single-session job and the ABR ladder below.
	audioChannels := transcode.TargetAudioChannels(
		transcode.SourceAudioChannels(file.AudioStreams, audioStreamIdx), caps.MaxAudioChannels)

	isSourceHEVC := file.VideoCodec != nil && (strings.EqualFold(*file.VideoCodec, "hevc") || strings.EqualFold(*file.VideoCodec, "h265"))
	isSourceAV1 := file.VideoCodec != nil && strings.EqualFold(*file.VideoCodec, "av1")
	isSourceH264 := file.VideoCodec != nil && (strings.EqualFold(*file.VideoCodec, "h264") || strings.EqualFold(*file.VideoCodec, "avc") || strings.EqualFold(*file.VideoCodec, "avc1"))
	isSourceHDR := file.HDRType != nil && *file.HDRType != ""

	// HEVC output preference fires in two cases:
	//   - Output is 4K (≥2160 lines): saves ~40 % bitrate for the same
	//     visual quality vs H.264 at the same resolution.
	//   - Source is HEVC and the client supports it: preserves the
	//     format the source already paid for, instead of round-tripping
	//     HEVC → H.264 just because the client also speaks H.264.
	// Mirrors the AV1 source-preservation rule below.
	preferHEVC := caps.SupportsHEVC && !body.VideoCopy && (height >= 2160 || isSourceHEVC)
	// Auto-prefer AV1 output for AV1-source playback when the client supports it.
	// Avoids the AV1 → H.264 round-trip we'd otherwise do (any non-Auto quality
	// click on an AV1 source). The worker confirms an AV1 encoder is actually
	// active before honoring this; if not, it falls back to HEVC then H.264.
	// AV1 takes priority over HEVC at the worker — natural use case is "play
	// the AV1 source," and re-encoding AV1 → HEVC throws away the format
	// efficiency the source already paid for.
	preferAV1 := caps.SupportsAV1 && isSourceAV1 && !body.VideoCopy

	// Stream-shape facts the arg builder needs. A hard -map of a missing
	// stream kills ffmpeg instantly, so both absences must be declared:
	// audio-only sources (music in undeclared containers, .dsf/.ape rips,
	// audiobooks — the decision engine deliberately routes them here) and
	// audio-less videos (screen recordings, timelapses) were both structurally
	// unplayable through this pipeline.
	sourceAudioOnly := file.VideoCodec == nil && file.AudioCodec != nil
	sourceNoAudio := file.AudioCodec == nil && file.VideoCodec != nil

	// Bit-depth ceiling: when the client declared a sub-10-bit decoder, the
	// OUTPUT must be 8-bit. Decide already forces the transcode verdict for
	// deep sources on these clients — but the job then picked an HEVC encoder
	// (source preservation) and emitted Main 10: the exact property that
	// forced the transcode, handed back to the client that can't decode it.
	force8Bit := caps.MaxVideoBitDepth > 0 && caps.MaxVideoBitDepth < 10 && !body.VideoCopy

	// Audio passthrough on remux: when DirectStream was chosen for the
	// CONTAINER (the client decodes the source audio, within its channel cap,
	// and the codec is HLS-carriable), copy the audio instead of degrading it
	// through a forced AAC re-encode.
	audioCopy := false
	if body.VideoCopy && file.AudioCodec != nil {
		alias := transcode.CanonicalAudioCodec(*file.AudioCodec)
		srcCh := transcode.SourceAudioChannels(file.AudioStreams, audioStreamIdx)
		chFit := caps.MaxAudioChannels <= 0 || srcCh <= 0 || srcCh <= caps.MaxAudioChannels
		switch alias {
		case "aac", "ac3", "eac3", "mp3": // valid in both MPEG-TS and fMP4
			audioCopy = caps.SupportsAudioCodec(alias) && chFit
		}
	}

	// The session's HEVCOutput/AV1Output record the OUTPUT codec, which is what
	// the playlist handler derives the segment container from. On a remux that
	// is the SOURCE codec — and preferHEVC/preferAV1 above BOTH carry
	// `!body.VideoCopy`, because they gate re-encode decisions. Reading them
	// here would claim H.264 output for an HEVC remux and send the playlist
	// handler looking for .ts files ffmpeg is writing as .m4s.
	//
	// The worker overwrites these via SetWorkerInfo once it knows the encoder
	// it actually got, but the client can ask for the playlist before that
	// lands, so the initial guess has to be right on its own.
	sessVout := transcode.VideoOutput{HEVC: preferHEVC, AV1: preferAV1}
	if body.VideoCopy {
		sessVout = transcode.ResolveVideoOutput(transcode.EncoderCopy, isSourceHEVC, isSourceAV1, false)
	}

	// For remux, use the source file bitrate (video is copied unchanged).
	// For full transcode, use the target bitrate from quality selection.
	sessionBitrate := bitrateKbps
	if body.VideoCopy && file.Bitrate != nil {
		sessionBitrate = int(*file.Bitrate / 1000)
	}

	// ── Adaptive-bitrate ladder (on-demand) ───────────────────────────────
	// When ABR is enabled and this is a full re-encode of a file with a
	// known duration + resolution that yields more than one rung, serve a
	// multi-rendition ladder instead of a single rendition. The parent
	// session runs no ffmpeg; playlist.m3u8 returns a master and each rung
	// transcodes on demand (transcode_abr.go). H.264 .ts only for now.
	// ABR is for AUTO playback only (body.Height == 0). An explicit quality
	// pick means "give me exactly this" — serving a single rendition makes
	// rung-switching (and therefore thrash) structurally impossible, which is
	// what a user who picked 1080p/4K expects. Auto still gets the adaptive
	// ladder.
	if h.cfg.TranscodeABR && decision == "transcode" && body.Height == 0 &&
		file.DurationMS != nil && *file.DurationMS > 0 && sourceH > 0 {
		srcBitrate := 0
		if file.Bitrate != nil {
			srcBitrate = int(*file.Bitrate / 1000)
		}
		// Pick the ladder codec, mirroring the single-rendition prefer
		// rules. AV1 wins for an AV1 source the client can decode (don't
		// round-trip to H.264/HEVC). Else HEVC when the client can decode it
		// AND it pays off — an HEVC source or 4K. Else H.264 .ts, the
		// universally-decodable default. HEVC/AV1 ladders are fMP4 (.m4s).
		// Client capability is necessary but NOT sufficient: the ladder's codec
		// decides the advertised CODECS and the .m4s/.ts segment names, and a
		// fleet with no HEVC/AV1 encoder silently falls back to H.264 — so
		// advertising it made every segment 503. Confirm a node can deliver
		// before promising it.
		// A sub-10-bit client must not get an HEVC/AV1 ladder off a 10-bit
		// source: the rung children preserve source depth and would emit
		// Main 10 — the property that forced the transcode. H.264 rungs are
		// always 8-bit-stripped, so they're safe for any depth.
		sourceDepth := 0
		if file.VideoBitDepth != nil {
			sourceDepth = *file.VideoBitDepth
		}
		depthOK := !force8Bit || sourceDepth < 10
		abrCodec := transcode.LadderH264
		switch {
		case caps.SupportsAV1 && isSourceAV1 && depthOK && h.sessions.FleetCanEncode(ctx, "av1"):
			abrCodec = transcode.LadderAV1
		case caps.SupportsHEVC && (isSourceHEVC || sourceH >= 2160) && depthOK &&
			h.sessions.FleetCanEncode(ctx, "hevc"):
			abrCodec = transcode.LadderHEVC
		}
		// Resolve the ladder height ceiling. An explicit quality pick wins;
		// Auto (body.Height==0) falls back to the soft TranscodeABRAutoMaxHeight
		// default so it doesn't build a 4K rung the player thrashes on (each
		// rung switch restarts ffmpeg + re-probes the source over HTTP on a
		// fleet worker); the hard TranscodeABRMaxHeight cap applies on top.
		ladderCap := abrLadderCap(body.Height, h.cfg.TranscodeABRAutoMaxHeight, h.cfg.TranscodeABRMaxHeight)
		// The client's declared display ceiling bounds the ladder the same way
		// it bounds the single-rendition output — a maxheight=1080 client must
		// not be offered (and auto-switched onto) a 4K rung.
		if caps.MaxHeight > 0 && (ladderCap == 0 || caps.MaxHeight < ladderCap) {
			ladderCap = caps.MaxHeight
		}
		// Same ceiling the single-rendition path applies below: the per-user
		// stream cap wins over the server-wide default when it is lower. The
		// ladder used to ignore both and take its bitrates straight from the
		// fixed tier table, so an administered 2000 kbps user pressing Play at
		// Auto — which IS the ABR trigger — got a 1080p@8000 rung.
		abrMaxBitrate := h.cfg.TranscodeMaxBitrate
		if streamCaps.BitrateKbps > 0 && (abrMaxBitrate <= 0 || streamCaps.BitrateKbps < abrMaxBitrate) {
			abrMaxBitrate = streamCaps.BitrateKbps
		}
		ladder := transcode.BuildLadder(sourceW, sourceH, srcBitrate, abrCodec, ladderCap, abrMaxBitrate)
		if len(ladder) > 1 {
			h.startABR(w, r, sessionID, segTok, sourceURL, claims.UserID, itemID, file, ladder, audioStreamIdx, audioChannels, isSourceHDR, abrCodec, body.PositionMS, maxSessionsPerUser)
			return
		}
	}

	sess := transcode.Session{
		ID:          sessionID,
		UserID:      claims.UserID,
		MediaItemID: itemID,
		FileID:      file.ID,
		Decision:    decision,
		FilePath:    file.FilePath,
		SourceURL:   sourceURL,
		PositionMS:  body.PositionMS,
		CreatedAt:   time.Now(),
		ClientName:  "OnScreenWeb",
		SegToken:    segTok,
		BitrateKbps: sessionBitrate,
		HEVCOutput:  sessVout.HEVC,
		AV1Output:   sessVout.AV1,
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
	// Authoritative cap enforcement at write time. The pre-check above is a
	// cheap early-out; this closes the check-then-act race where concurrent
	// Starts both pass the pre-check and overshoot the cap.
	if err := h.sessions.CreateWithUserCap(ctx, sess, maxSessionsPerUser); err != nil {
		if errors.Is(err, transcode.ErrUserAtCap) {
			h.logger.WarnContext(ctx, "per-user transcode session cap reached (atomic)",
				"user_id", claims.UserID, "cap", maxSessionsPerUser)
			respond.Error(w, r, http.StatusTooManyRequests, "TOO_MANY_SESSIONS",
				fmt.Sprintf("you already have %d active streams; stop one before starting another",
					maxSessionsPerUser))
			return
		}
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

	// Audio passthrough when the remux was container-driven (see audioCopy
	// above): "copy" reaches BuildHLS as `-c:a copy` with the AAC extras
	// naturally skipped.
	jobAudioCodec := "aac"
	if audioCopy {
		jobAudioCodec = "copy"
	}

	job := transcode.TranscodeJob{
		SessionID:        sessionID,
		FilePath:         file.FilePath,
		SourceURL:        sourceURL,
		SessionDir:       transcode.SessionDir(sessionID),
		StartOffsetSec:   startOffsetSec,
		Decision:         decision,
		Encoder:          encoder,
		Width:            width,
		Height:           height,
		BitrateKbps:      jobBitrate,
		AudioCodec:       jobAudioCodec,
		AudioChannels:    audioChannels,
		AudioStreamIndex: audioStreamIdx,
		IsHEVC:           isSourceHEVC,
		IsAV1:            isSourceAV1,
		IsH264:           isSourceH264,
		AudioOnly:        sourceAudioOnly,
		NoAudio:          sourceNoAudio,
		Force8Bit:        force8Bit,
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
		"supports_hevc", caps.SupportsHEVC,
		"supports_av1", caps.SupportsAV1,
		"audio_channels", audioChannels,
		"client_profile", hasProfile,
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
	//
	// The probe reads segment files off disk, so it needs to know which
	// container they are in — an HEVC/AV1 remux writes fMP4, which it cannot
	// probe and declines immediately rather than blocking for the full cap.
	//
	// LOCAL disk only: on a fleet deployment the job just dispatched to a
	// REMOTE worker (workerAddr non-empty) and its segments never appear in
	// this host's session dir — the probe used to poll the empty local path
	// for the full 5 s cap, adding a flat five seconds to every mid-stream
	// remux start and never measuring anything. The gap feature is simply
	// unavailable off-box; skip it honestly.
	var seg0AudioGap float64
	if body.VideoCopy && startOffsetSec > 0 && workerAddr == "" {
		if gap, ok := transcode.WaitForSeg0Audio(ctx, transcode.SessionDir(sessionID), sessVout, 5*time.Second); ok {
			seg0AudioGap = gap
			h.logger.InfoContext(ctx, "seg 0 audio gap measured",
				"session_id", sessionID,
				"gap_sec", gap,
			)
		}
	}

	playlistURL := fmt.Sprintf("/api/v1/transcode/sessions/%s/playlist.m3u8?token=%s",
		sessionID, segTok)

	// Client vanished while we were creating the session (BACK during the
	// buffering spinner cancels OkHttp's call, which closes the connection):
	// the response below would go nowhere, the client never learns the
	// session id, and its teardown paths have nothing to DELETE — the
	// session runs orphaned until the idle reaper. Reap it now instead.
	if r.Context().Err() != nil {
		bg := context.WithoutCancel(r.Context())
		h.logger.InfoContext(bg,
			"transcode: client disconnected during start — reaping orphan", "session_id", sessionID)
		if sess, gerr := h.sessions.Get(bg, sessionID); gerr == nil && sess != nil {
			h.tearDown(bg, sess, segTok)
		}
		return
	}

	respond.Success(w, r, transcodeStartResponse{
		SessionID:       sessionID,
		PlaylistURL:     playlistURL,
		Token:           segTok,
		StartOffsetSec:  startOffsetSec,
		Seg0AudioGapSec: seg0AudioGap,
	})
}

// lazyReprobe fills in source metadata a scan left NULL (codec, resolution,
// HDR type, bitrate) by re-probing the file synchronously. Cheap for healthy
// files (<500 ms) and only fires when the row is actually missing data.
//
// Shared by transcode Start AND the playback-decision endpoint. Decide used
// to skip it: a video row that lost its VideoCodec during scanning read as
// "audio-only", which skips the video capability check entirely, so the
// endpoint handed out directPlay/directStream verdicts for video the client
// may not decode at all — and the client then trusted the verdict over its
// own local heuristics.
func (h *NativeTranscodeHandler) lazyReprobe(ctx context.Context, file *media.File) {
	if file.VideoCodec != nil && file.ResolutionW != nil && file.ResolutionH != nil {
		return
	}
	probed, perr := h.probeFile(ctx, file.FilePath)
	if perr != nil {
		h.logger.WarnContext(ctx, "transcode: lazy re-probe failed",
			"file_id", file.ID, "path", file.FilePath, "err", perr)
		return
	}
	if probed == nil {
		return
	}
	if probed.VideoCodec != nil && file.VideoCodec == nil {
		file.VideoCodec = probed.VideoCodec
	}
	if probed.ResolutionW != nil && file.ResolutionW == nil {
		file.ResolutionW = probed.ResolutionW
	}
	if probed.ResolutionH != nil && file.ResolutionH == nil {
		file.ResolutionH = probed.ResolutionH
	}
	if probed.HDRType != nil && file.HDRType == nil {
		file.HDRType = probed.HDRType
	}
	if probed.Bitrate != nil && file.Bitrate == nil {
		file.Bitrate = probed.Bitrate
	}
	vc, hdr := "", ""
	if file.VideoCodec != nil {
		vc = *file.VideoCodec
	}
	if file.HDRType != nil {
		hdr = *file.HDRType
	}
	h.logger.InfoContext(ctx, "transcode: lazy re-probe filled missing source metadata",
		"file_id", file.ID,
		"video_codec", vc,
		"hdr", hdr)
}

// probeFile indirects scanner.ProbeFile so tests can stub the ffprobe exec.
func (h *NativeTranscodeHandler) probeFile(ctx context.Context, path string) (*scanner.ProbeResult, error) {
	if h.probe != nil {
		return h.probe(ctx, path)
	}
	return scanner.ProbeFile(ctx, path)
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
		// Owner-only for regular users. An ADMIN may stop any user's
		// session (the Plex/Emby/Jellyfin "stop this stream" moderation
		// affordance) — audit-logged with both parties so a moderation
		// action is always attributable.
		if sess.UserID != claims.UserID {
			if !claims.IsAdmin {
				respond.Forbidden(w, r)
				return
			}
			h.logger.InfoContext(ctx, "transcode: admin stopped another user's session",
				"session_id", sess.ID, "admin_id", claims.UserID,
				"owner_id", sess.UserID, "client_name", sess.ClientName)
			if h.audit != nil {
				actor := claims.UserID
				h.audit.Log(ctx, &actor, audit.ActionTranscodeStop, sess.ID,
					map[string]any{
						"owner_id": sess.UserID.String(),
						"reason":   "admin_terminated",
					}, "")
			}
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
	// ABR parents own per-rung child sessions; reap them too.
	if sess.ABR {
		h.cleanupRungChildren(ctx, sess)
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

	// ABR parent: serve the master playlist (instant — no ffmpeg). The
	// per-rung media playlists + on-demand segments are separate routes.
	if sess, err := h.sessions.Get(ctx, sessionID); err == nil && sess.ABR {
		h.serveABRMaster(w, r, sess, token)
		return
	}

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
	//
	// The extension comes from transcode.VideoOutput, the same type the worker
	// derives the real filenames from. Waiting on the wrong extension here
	// doesn't 404 — it hangs until the readiness timeout and the client never
	// gets a playlist.
	segExt := ".ts"
	if sess, err := h.sessions.Get(ctx, sessionID); err == nil {
		segExt = sess.VideoOutput().SegExt()
	}
	seg0Name := "seg00000" + segExt
	seg1Name := "seg00001" + segExt

	// Wait for seg0 to land — that's the minimum a client needs to start
	// playback. Once it's ready, briefly wait for seg1 so HLS.js has 2
	// entries on its first playlist parse — but cap that wait tightly.
	// seg0-only is already enough for HLS.js to begin fetching, and on a
	// slow first segment (Turing CUDA warmup, large remux, paced readrate)
	// seg1 can lag seg0 by 10 s+; a generous grace adds that lag straight
	// onto every transcode's start latency (measured: a 4K HDR start blocked
	// ~20 s on QA — ~11 s to seg0 plus the full 10 s grace waiting on a
	// lagging seg1). A 2 s cap still covers the fast-GPU case (seg1 lands
	// ~1 s after seg0, as on the local 5080) without penalising slow ones:
	// they return seg0-only and HLS.js picks up seg1 on its next poll.
	//
	// Each poll iteration stamps TouchActivity on the session so the
	// worker's supervisor doesn't reap an actively-waiting client as
	// "idle." Without this stamp the supervisor sees LastActivityAt=0,
	// uses CreatedAt as the anchor, and kills the session at the 60 s
	// mark even though the client (this handler) is blocked in the
	// loop below.
	const seg1ExtraGrace = 2 * time.Second
	haveSeg0 := false
	var seg1Deadline time.Time
	for time.Now().Before(deadline) {
		if !haveSeg0 {
			if workerReady(ctx, workerAddr, sessID, seg0Name, filepath.Join(sessDir, seg0Name)) {
				haveSeg0 = true
				seg1Deadline = time.Now().Add(seg1ExtraGrace)
			}
		} else {
			if !time.Now().Before(seg1Deadline) ||
				workerReady(ctx, workerAddr, sessID, seg1Name, filepath.Join(sessDir, seg1Name)) {
				break
			}
		}
		h.sessions.TouchActivity(ctx, sessionID)
		select {
		case <-ctx.Done():
			http.Error(w, "request cancelled", http.StatusServiceUnavailable)
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !haveSeg0 {
		// Never produced anything in the window. ffmpeg likely died on
		// input open (bad path, corrupt source, encoder init failure).
		// Surface 503 so the player can show a useful error instead of
		// hanging on a stale playlist URL.
		http.Error(w, "playlist not ready", http.StatusServiceUnavailable)
		return
	}

	data, err := fetchFromWorker(ctx, workerAddr, sessID, "index.m3u8", filepath.Join(sessDir, "index.m3u8"))
	if err != nil {
		http.Error(w, "playlist not ready", http.StatusServiceUnavailable)
		return
	}

	rewritten := rewritePlaylist(data, sessionID, token, h.segBase())
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

	// Stamp client-side activity. Lets the worker's idle-kill loop
	// distinguish "client still consuming" from "client crashed." A
	// browser tab closed without firing DELETE leaves the session
	// alive otherwise, and ffmpeg keeps encoding for the full 4 h
	// session TTL — wasting GPU + filling disk with segments nobody
	// will ever fetch.
	h.sessions.TouchActivity(ctx, sessionID)

	if workerAddr != "" {
		proxyWorkerFile(w, r, workerAddr, sessID, segName)
		return
	}
	// Fallback: local disk (worker not yet stamped or same-machine embedded worker).
	http.ServeFile(w, r, localPath)
}

// workerReady returns true if the named file is available — either via a HEAD
// request to the worker's segment server or by stat-ing the local path.
//
// Each call is bounded by a 3 s sub-context so a hung worker can't defeat
// the playlist endpoint's overall 60 s deadline. Without this cap, a
// single workerReady call could block on `workerClient.Timeout=30s` and
// push the handler's return to 60+30 = 90 s — long enough that the
// browser keeps the spinner up and the user perceives "hangs forever"
// rather than "errored quickly." Returning false on a per-call timeout
// is correct because the next iteration of the polling loop will retry,
// up to the outer deadline.
func workerReady(ctx context.Context, workerAddr, sessID, name, localPath string) bool {
	if workerAddr != "" {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		url := fmt.Sprintf("http://%s/segments/%s/%s", workerAddr, sessID, name)
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
		if err != nil {
			return false
		}
		authWorkerReq(req)
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

// workerSegToken authenticates segment/seghead proxy requests to a worker's
// segment HTTP server. Set once at startup from SECRET_KEY (see
// transcode.SegmentProxyToken). Empty = no header (tests / unset key).
var workerSegToken string

// SetWorkerSegToken installs the worker segment-proxy bearer.
func SetWorkerSegToken(t string) { workerSegToken = t }

// authWorkerReq attaches the segment-proxy bearer so an unauthenticated peer
// on the network can't fetch in-progress segments directly from a worker.
func authWorkerReq(req *http.Request) {
	if workerSegToken != "" {
		req.Header.Set("Authorization", "Bearer "+workerSegToken)
	}
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
		authWorkerReq(req)
		resp, err := workerClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		// A non-200 body is an ERROR PAGE, not the file. This used to return
		// it as content, so a worker's "503 segment not ready" (or a 401 from
		// a token mismatch) was rewritten by the playlist pipeline and served
		// to the player as a 200 m3u8 whose body was an error string — the
		// player then failed parsing with no hint of the real cause.
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			return nil, fmt.Errorf("worker %s returned %d for %s/%s: %s",
				workerAddr, resp.StatusCode, sessID, name, strings.TrimSpace(string(body)))
		}
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
	authWorkerReq(req)
	resp, err := workerClient.Do(req)
	if err != nil {
		http.Error(w, "segment unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Pass the worker's status through with an HONEST message. Everything
		// used to be labelled "segment not found" regardless, so a transient
		// "still encoding" was indistinguishable from a genuinely missing
		// segment both to the player and to whoever was reading the logs.
		if resp.StatusCode == http.StatusServiceUnavailable {
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				w.Header().Set("Retry-After", ra)
			}
			http.Error(w, "segment not ready", http.StatusServiceUnavailable)
			return
		}
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

// publicSeg prefixes a relative transcode URL with the configured public segment
// base URL (PUBLIC_SEGMENT_BASE_URL) when set, so the bandwidth-heavy segment /
// rung-playlist traffic can be routed to a direct host that bypasses a CDN/tunnel
// while the API and UI stay on the main host. Empty base → path unchanged
// (same-origin). A trailing slash on base is tolerated.
func publicSeg(base, path string) string {
	if base == "" {
		return path
	}
	return strings.TrimRight(base, "/") + path
}

// segBase returns the configured public segment base URL, or "" when no config
// is wired (e.g. test handlers) or the split is disabled. Keeps the URL builders
// nil-safe — see publicSeg / PUBLIC_SEGMENT_BASE_URL.
func (h *NativeTranscodeHandler) segBase() string {
	if h.cfg == nil {
		return ""
	}
	return h.cfg.PublicSegmentBaseURL
}

// rewritePlaylist rewrites segment URIs in an HLS playlist to API paths with the
// auth token embedded, so HLS.js can request them without extra config. Handles
// both MPEG-TS (.ts) and fMP4 (.m4s / init.mp4) segment references. When baseURL
// is non-empty, segment URLs are made absolute against it (see publicSeg) so the
// heavy segment fetches can ride a direct/CDN host while the manifest — which
// the player re-polls from the main host — stays where it was served.
func rewritePlaylist(data []byte, sessionID, token, baseURL string) []byte {
	var buf bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		// Rewrite segment file references: .ts (MPEG-TS) and .m4s/.mp4 (fMP4).
		if !strings.HasPrefix(line, "#") &&
			(strings.HasSuffix(line, ".ts") || strings.HasSuffix(line, ".m4s") || strings.HasSuffix(line, ".mp4")) {
			name := filepath.Base(line)
			line = publicSeg(baseURL, fmt.Sprintf("/api/v1/transcode/sessions/%s/seg/%s?token=%s",
				sessionID, name, token))
		}
		// Rewrite #EXT-X-MAP URI (fMP4 init segment).
		if strings.HasPrefix(line, "#EXT-X-MAP:URI=") {
			// Format: #EXT-X-MAP:URI="init.mp4"
			name := strings.TrimPrefix(line, "#EXT-X-MAP:URI=")
			name = strings.Trim(name, "\"")
			name = filepath.Base(name)
			line = fmt.Sprintf("#EXT-X-MAP:URI=\"%s\"", publicSeg(baseURL, fmt.Sprintf("/api/v1/transcode/sessions/%s/seg/%s?token=%s",
				sessionID, name, token)))
		}
		buf.WriteString(line + "\n")
	}
	return buf.Bytes()
}
