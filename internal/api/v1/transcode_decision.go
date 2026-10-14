package v1

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

// playbackDecisionResponse tells the client how to play a file: play it
// directly, remux the container only, or transcode. The client maps the
// decision onto its existing playback paths — directPlay → the direct file
// URL; directStream → a video-copy (remux) transcode; transcode → a full
// transcode. Centralizing the decision here (was: each client deciding
// locally) is Phase 2 of docs/capability-profiles.md.
type playbackDecisionResponse struct {
	// Decision: directPlay | directStream | transcode | unsupported | damaged.
	// unsupported = content the server refuses to transcode for this client
	// (Dolby Vision); damaged = the integrity probe marked the file's
	// bitstream undecodable — no playback path exists on any client.
	Decision string `json:"decision"`
	FileID   string `json:"file_id"` // the file the decision applies to
	// Reason is set when the server overrode a capability verdict:
	// remote_bitrate_limit (or user_bitrate_limit) = the client is outside
	// the home network and the source exceeds its streaming cap, so it must
	// transcode. Additive — clients that ignore it just see "transcode".
	Reason string `json:"reason,omitempty"`
	// MaxBitrateKbps is the cap the transcode will be held to, with Reason.
	MaxBitrateKbps int `json:"max_bitrate_kbps,omitempty"`
}

// Decision handles POST /api/v1/items/{id}/playback-decision. It resolves the
// client's capability profile (X-Client-Capabilities header, legacy booleans
// folded in) and the target file, then returns the server-authoritative play
// decision from transcode.Decide. Read-only and cheap — no session, no worker;
// the subsequent transcode-start re-runs all validation. The optional request
// body mirrors transcode-start (file_id + legacy supports_* flags) so a client
// can select a specific file and stay back-compatible.
func (h *NativeTranscodeHandler) Decision(w http.ResponseWriter, r *http.Request) {
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

	// Body is optional — capabilities normally arrive in the header. Tolerate
	// an empty/missing/garbage body rather than 400ing a header-only request.
	var body transcodeStartRequest
	_ = json.NewDecoder(r.Body).Decode(&body)

	item, err := h.media.GetItem(ctx, itemID)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	if h.access != nil {
		ok, aerr := h.access.CanAccessLibrary(ctx, claims.UserID, item.LibraryID, claims.IsAdmin)
		if aerr != nil {
			h.logger.ErrorContext(ctx, "playback-decision: library access check", "library_id", item.LibraryID, "err", aerr)
			respond.InternalError(w, r)
			return
		}
		if !ok {
			respond.NotFound(w, r)
			return
		}
	}
	if viewerRestricted(claims) {
		cr := ""
		if item.ContentRating != nil {
			cr = *item.ContentRating
		}
		if !itemPermitted(r.Context(), claims, item.ID, cr) {
			respond.Forbidden(w, r)
			return
		}
	}

	// Resolve the file: explicit file_id (with cross-item guard) or the best
	// file. Mirrors transcode-start's selection.
	var file *media.File
	var itemFiles []media.File
	if body.FileID != nil && *body.FileID != "" {
		fid, perr := uuid.Parse(*body.FileID)
		if perr != nil {
			respond.BadRequest(w, r, "invalid file_id")
			return
		}
		f, ferr := h.media.GetFile(ctx, fid)
		if ferr != nil || f.MediaItemID != itemID {
			respond.NotFound(w, r)
			return
		}
		file = f
	} else {
		files, ferr := h.media.GetFiles(ctx, itemID)
		if ferr != nil || len(files) == 0 {
			respond.NotFound(w, r)
			return
		}
		itemFiles = files
		file = &files[0] // sorted best-quality first
	}
	// A part of a stacked movie is judged as the movie: from part 1, which
	// the session will start with whichever part was named.
	parts := h.stackParts(ctx, itemID, file, itemFiles)
	if parts != nil {
		first := parts[0]
		file = &first
	}

	// Fill metadata a scan left NULL before deciding — the same lazy re-probe
	// Start runs. Without it a video row with a lost VideoCodec read as
	// "audio-only", skipped the video capability check entirely, and this
	// endpoint handed out directPlay for video the client can't decode; the
	// client then trusted the verdict over its own heuristics.
	if err := h.lazyReprobe(ctx, file); err != nil {
		respond.Error(w, r, http.StatusUnsupportedMediaType, "UNSUPPORTED_CONTAINER",
			"this file is not a playable media container")
		return
	}
	// The other parts get Start's container gate too; a damaged one damages
	// the movie.
	damagedPart := false
	for _, p := range sessionSources(file, parts) {
		if err := h.refuseReferenceContainer(p); err != nil {
			respond.Error(w, r, http.StatusUnsupportedMediaType, "UNSUPPORTED_CONTAINER",
				"this file is not a playable media container")
			return
		}
		if p.IntegrityStatus == media.IntegrityDamaged {
			damagedPart = true
		}
	}

	caps, hasProfile := clientCaps(r, body)
	serverCaps := transcode.ServerCaps{MaxHeight: h.cfg.TranscodeMaxHeight}
	decision := transcode.Decide(*file, caps, serverCaps)
	if parts != nil {
		// Never directPlay: one file is one part. The session concatenates
		// them (see transcode.ForStack).
		decision = transcode.ForStack(decision, *file)
		if damagedPart {
			decision = transcode.DecisionDamaged
		}
	}

	// Internet streaming limit: a remote client (judged from the trusted
	// client IP) must not direct-play or remux a source above its cap — the
	// verdict becomes transcode and Start clamps the output to the cap. LAN
	// clients are untouched.
	var reason string
	var capKbps int
	if decision == transcode.DecisionDirectPlay || decision == transcode.DecisionDirectStream {
		var userCaps StreamCaps
		if h.userCaps != nil {
			userCaps, _ = h.userCaps.GetStreamCaps(ctx, claims.UserID)
		}
		bc := resolveStreamCap(ctx, userCaps, h.remotePolicy, audit.ClientIP(r), file)
		if why, ok := remoteDecisionOverride(bc, file); ok {
			decision, reason, capKbps = transcode.DecisionTranscode, why, bc.Kbps
		}
	}

	h.logger.DebugContext(ctx, "playback decision",
		"item_id", itemID, "file_id", file.ID, "decision", decision.String(), "reason", reason,
		"profile_header", hasProfile, "max_audio_channels", caps.MaxAudioChannels,
		"supports_hevc", caps.SupportsHEVC, "supports_av1", caps.SupportsAV1)
	// Echo the resolved profile for debugging (same header transcode-start sets).
	w.Header().Set("X-OnScreen-Client-Caps", fmt.Sprintf(
		"profile=%t;hevc=%t;av1=%t;maxAudioChannels=%d;maxW=%d;maxH=%d;maxBitDepth=%d",
		hasProfile, caps.SupportsHEVC, caps.SupportsAV1, caps.MaxAudioChannels,
		caps.MaxWidth, caps.MaxHeight, caps.MaxVideoBitDepth))

	// respond.Success wraps in the standard {"data": ...} envelope that every
	// client's ApiResponse<T> parser expects — using respond.JSON here (raw body)
	// made all clients fail to parse and silently fall back to local decisions.
	respond.Success(w, r, playbackDecisionResponse{
		Decision:       decision.String(),
		FileID:         file.ID.String(),
		Reason:         reason,
		MaxBitrateKbps: capKbps,
	})
}
