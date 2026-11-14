package v1

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// playbackTarget is what transcode Start and the playback decision both
// resolve before judging anything: the item, the file to play and, for a
// stacked movie, its parts.
type playbackTarget struct {
	item *media.Item
	// file is the file to play; part 1 for a stacked movie, whichever part
	// was named.
	file *media.File
	// parts is nil unless the file is one part of a stacked movie.
	parts []media.File
}

// resolvePlaybackTarget runs the gates transcode Start and the playback
// decision share, in one order with one set of answers, so the decision can't
// approve playback that Start then refuses:
//
//  1. the item, its library ACL and rating ceiling / labels (loadVisibleItem403:
//     404 for a missing item or a library the caller can't see, 403 for a
//     rating/label denial, which the Android clients show as "outside your
//     content rating limit");
//  2. the parental watch limit (403 PARENTAL_LIMIT, see watchLimitBlocks);
//  3. an admin stop of this (user, item, client) (403 PLAYBACK_STOPPED);
//  4. the file: fileID when given (400 if malformed, 404 if missing or
//     another item's), else the item's best file (404 if it has none);
//  5. the stack parts the session will stream.
//
// Before the decision ran 1, 4 and 5 only, so a restricted profile outside
// its allowed hours was told directPlay and then refused by Start or the
// stream. On refusal the response is written and ok is false.
func (h *NativeTranscodeHandler) resolvePlaybackTarget(w http.ResponseWriter, r *http.Request, claims *auth.Claims, itemID uuid.UUID, fileID *string) (playbackTarget, bool) {
	ctx := r.Context()
	item, ok := loadVisibleItem403(w, r, h.media, h.access, h.logger, itemID)
	if !ok {
		return playbackTarget{}, false
	}
	// Before any worker resources are spent. Fail-open on a store error (a
	// household cap, not a paywall); the progress heartbeat re-checks during
	// playback anyway.
	if watchLimitBlocks(w, r, h.watchLimit, h.logger, claims.UserID) {
		return playbackTarget{}, false
	}
	// Admin-stop window: a client an admin just stopped must not spin up a
	// replacement session (see playback_stop.go).
	if playbackStopBlocks(w, r, h.stops, claims.UserID, itemID) {
		return playbackTarget{}, false
	}

	var file *media.File
	var itemFiles []media.File
	if fileID != nil && *fileID != "" {
		fid, err := uuid.Parse(*fileID)
		if err != nil {
			respond.BadRequest(w, r, "invalid file_id")
			return playbackTarget{}, false
		}
		f, err := h.media.GetFile(ctx, fid)
		if err != nil {
			respond.NotFoundOrServerError(w, r, h.logger, err, "playback: get file", media.ErrNotFound)
			return playbackTarget{}, false
		}
		// Reject cross-item file IDs — otherwise a caller can use an allowed
		// item ID to launch a session that streams a different, disallowed
		// file.
		if f.MediaItemID != itemID {
			respond.NotFound(w, r)
			return playbackTarget{}, false
		}
		file = f
	} else {
		files, err := h.media.GetFiles(ctx, itemID)
		if err != nil {
			respond.ServerError(w, r, h.logger, err, "playback: list files", "item_id", itemID)
			return playbackTarget{}, false
		}
		if len(files) == 0 {
			respond.NotFound(w, r)
			return playbackTarget{}, false
		}
		itemFiles = files
		file = &files[0] // sorted best quality first
	}

	// Stacked movie ("cd1" + "cd2"): whichever part was asked for, the
	// session streams every part from the first, as one movie, and the
	// decision judges the movie from part 1. The parts come from the item's
	// own file rows, so the gates above cover all of them.
	parts := h.stackParts(ctx, itemID, file, itemFiles)
	if parts != nil {
		first := parts[0]
		file = &first
	}
	return playbackTarget{item: item, file: file, parts: parts}, true
}
