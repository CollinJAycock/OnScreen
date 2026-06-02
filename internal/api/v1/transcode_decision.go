package v1

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/contentrating"
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
	Decision string `json:"decision"` // directPlay | directStream | transcode
	FileID   string `json:"file_id"`  // the file the decision applies to
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

	// Resolve the file: explicit file_id (with cross-item guard) or the best
	// file. Mirrors transcode-start's selection.
	var file *media.File
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
		file = &files[0] // sorted best-quality first
	}

	caps, hasProfile := clientCaps(r, body)
	serverCaps := transcode.ServerCaps{MaxHeight: h.cfg.TranscodeMaxHeight}
	decision := transcode.Decide(*file, caps, serverCaps)

	h.logger.DebugContext(ctx, "playback decision",
		"item_id", itemID, "file_id", file.ID, "decision", decision.String(),
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
		Decision: decision.String(),
		FileID:   file.ID.String(),
	})
}
