package v1

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/domain/library"
)

// MaintenanceLibraryService is the slice of library.Service the reprobe
// endpoint needs.
type MaintenanceLibraryService interface {
	List(ctx context.Context) ([]library.Library, error)
	EnqueueScan(ctx context.Context, id uuid.UUID) error
}

// MetadataReprober clears the content hashes that gate the scanner's
// fast-skip, forcing the next scan to re-probe and re-persist technical
// metadata. Backs the reprobe-metadata maintenance endpoint.
type MetadataReprober interface {
	ClearActiveFileHashesForReprobe(ctx context.Context, libraryID *uuid.UUID) (int64, error)
}

// MaintenanceHandler exposes admin-only one-shot operations. Only
// reprobe-metadata is left: v3 removed refresh-missing-art (the scheduled
// refresh_missing_art task does it), dedupe-shows / dedupe-movies (full
// scans repair duplicates) and purge-deleted-library, none of which any
// client called (docs/v3-api-audit.md).
type MaintenanceHandler struct {
	library  MaintenanceLibraryService
	reprober MetadataReprober
	logger   *slog.Logger
}

// NewMaintenanceHandler creates a MaintenanceHandler.
func NewMaintenanceHandler(lib MaintenanceLibraryService, reprober MetadataReprober, logger *slog.Logger) *MaintenanceHandler {
	return &MaintenanceHandler{library: lib, reprober: reprober, logger: logger}
}

// ReprobeMetadata handles POST /api/v1/maintenance/reprobe-metadata.
// Backfills technical metadata that the float64→Numeric persist bug
// silently dropped to NULL (frame_rate on video, replaygain_* on music)
// for files scanned before the fix. It NULLs file_hash on active files —
// which makes the scanner's mtime fast-skip AND content-hash fast-path
// both miss — then enqueues a scan so each file is re-probed and
// re-persisted through the corrected write path. Hashes are recomputed
// during that re-probe.
//
// Optional ?library_id=UUID scopes the reset + scan to one library;
// otherwise every library is reset and re-scanned. Returns the count of
// files cleared and libraries queued.
//
// This covers probe-derived fields only. Item ratings (also NULLed by
// the same bug) come from TMDB enrichment, not the probe — use the
// scheduled refresh_missing_art task or per-item enrich for those.
//
// Admin-only — mounted under AdminRequired in router.go.
func (h *MaintenanceHandler) ReprobeMetadata(w http.ResponseWriter, r *http.Request) {
	var libID *uuid.UUID
	if raw := r.URL.Query().Get("library_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			respond.BadRequest(w, r, "invalid library_id")
			return
		}
		libID = &parsed
	}

	cleared, err := h.reprober.ClearActiveFileHashesForReprobe(r.Context(), libID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "reprobe: clear file hashes", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}

	// Collect the libraries to re-scan: the one named, or all of them.
	var libIDs []uuid.UUID
	if libID != nil {
		libIDs = []uuid.UUID{*libID}
	} else {
		libs, err := h.library.List(r.Context())
		if err != nil {
			h.logger.ErrorContext(r.Context(), "reprobe: list libraries", "err", err)
			respond.InternalError(w, r)
			return
		}
		for _, l := range libs {
			libIDs = append(libIDs, l.ID)
		}
	}

	queued := 0
	for _, id := range libIDs {
		if err := h.library.EnqueueScan(r.Context(), id); err != nil {
			h.logger.WarnContext(r.Context(), "reprobe: enqueue scan", "library_id", id, "err", err)
			continue
		}
		queued++
	}

	h.logger.InfoContext(r.Context(), "reprobe metadata enqueued",
		"library_id", libID, "files_cleared", cleared, "libraries_queued", queued)

	respond.Success(w, r, map[string]any{
		"files_cleared":    cleared,
		"libraries_queued": queued,
	})
}
