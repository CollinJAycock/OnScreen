package v1

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// MaintenanceMediaService is the slice of media.Service that maintenance
// endpoints depend on. Kept narrow so tests can provide fakes easily.
type MaintenanceMediaService interface {
	ListItemsMissingArt(ctx context.Context, limit int32) ([]media.Item, error)
	DedupeTopLevelItems(ctx context.Context, itemType string, libraryID *uuid.UUID) (media.DedupeResult, error)
}

// MaintenanceLibraryService is the slice of library.Service the
// purge + reprobe endpoints need. Separate from MaintenanceMediaService
// so the existing media-only handlers can keep their narrow surface.
type MaintenanceLibraryService interface {
	PurgeDeleted(ctx context.Context, id uuid.UUID) (int64, error)
	List(ctx context.Context) ([]library.Library, error)
	EnqueueScan(ctx context.Context, id uuid.UUID) error
}

// MetadataReprober clears the content hashes that gate the scanner's
// fast-skip, forcing the next scan to re-probe and re-persist technical
// metadata. Backs the reprobe-metadata maintenance endpoint.
type MetadataReprober interface {
	ClearActiveFileHashesForReprobe(ctx context.Context, libraryID *uuid.UUID) (int64, error)
}

// MaintenanceHandler exposes admin-only one-shot operations such as backfilling
// missing artwork after a new metadata source (e.g. TVDB key) is configured.
type MaintenanceHandler struct {
	media    MaintenanceMediaService
	library  MaintenanceLibraryService
	reprober MetadataReprober
	enricher ItemEnricher
	logger   *slog.Logger
}

// NewMaintenanceHandler creates a MaintenanceHandler.
func NewMaintenanceHandler(svc MaintenanceMediaService, lib MaintenanceLibraryService, reprober MetadataReprober, enricher ItemEnricher, logger *slog.Logger) *MaintenanceHandler {
	return &MaintenanceHandler{media: svc, library: lib, reprober: reprober, enricher: enricher, logger: logger}
}

// RefreshMissingArt handles POST /api/v1/maintenance/refresh-missing-art.
// Lists up to ?limit=N (default 200, max 1000) top-level items missing a
// poster, then queues per-item enrichment in a single background
// goroutine. Returns immediately with the candidate count so the HTTP
// request doesn't block on a 5–30-minute TMDB walk (which the
// cancelled request context would have killed mid-way through, plus
// Cloudflare's 100s edge timeout would 524 anyway).
func (h *MaintenanceHandler) RefreshMissingArt(w http.ResponseWriter, r *http.Request) {
	limit := respond.ParseLimit(r, 200, 1000)

	items, err := h.media.ListItemsMissingArt(r.Context(), limit)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list items missing art", "err", err)
		respond.InternalError(w, r)
		return
	}

	if len(items) > 0 {
		// WithoutCancel so the worker keeps processing after the HTTP
		// request returns (or gets cancelled by Cloudflare). Same shape
		// as ReEnrichUnmatched.
		bgCtx := context.WithoutCancel(r.Context())
		ids := make([]uuid.UUID, 0, len(items))
		titles := make(map[uuid.UUID]string, len(items))
		for _, it := range items {
			ids = append(ids, it.ID)
			titles[it.ID] = it.Title
		}
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					h.logger.ErrorContext(bgCtx, "refresh missing art panic",
						"panic", rec)
				}
			}()
			var refreshed, failed int
			for _, id := range ids {
				if err := h.enricher.EnrichItem(bgCtx, id); err != nil {
					h.logger.WarnContext(bgCtx, "refresh missing art failed",
						"item_id", id, "title", titles[id], "err", err)
					failed++
					continue
				}
				refreshed++
			}
			h.logger.InfoContext(bgCtx, "refresh missing art complete",
				"candidates", len(ids), "refreshed", refreshed, "failed", failed)
		}()
	}

	respond.Success(w, r, map[string]any{
		"candidates": len(items),
		"queued":     len(items),
	})
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
// the same bug) come from TMDB enrichment, not the probe — use
// refresh-missing-art / per-item enrich for those.
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

// DedupeShows handles POST /api/v1/maintenance/dedupe-shows.
// It collapses top-level show duplicates that share a normalized title
// (regardless of year) into the most-enriched survivor, walking seasons
// and episodes by index so episode files get reparented onto the survivor's
// episode rows. Optional ?library_id=UUID limits the scope; otherwise every
// library is processed.
func (h *MaintenanceHandler) DedupeShows(w http.ResponseWriter, r *http.Request) {
	h.dedupe(w, r, "show")
}

// DedupeMovies handles POST /api/v1/maintenance/dedupe-movies. Same shape as
// DedupeShows but for movie items (no children to merge).
func (h *MaintenanceHandler) DedupeMovies(w http.ResponseWriter, r *http.Request) {
	h.dedupe(w, r, "movie")
}

func (h *MaintenanceHandler) dedupe(w http.ResponseWriter, r *http.Request, itemType string) {
	var libID *uuid.UUID
	if raw := r.URL.Query().Get("library_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			respond.BadRequest(w, r, "invalid library_id")
			return
		}
		libID = &parsed
	}

	res, err := h.media.DedupeTopLevelItems(r.Context(), itemType, libID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "dedupe top-level items",
			"type", itemType, "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}

	h.logger.InfoContext(r.Context(), "dedupe complete",
		"type", itemType, "library_id", libID,
		"merged_items", res.MergedItems,
		"merged_seasons", res.MergedSeasons,
		"merged_episodes", res.MergedEpisodes,
		"reparented", res.ReparentedRows)

	respond.Success(w, r, res)
}

// PurgeDeletedLibrary handles POST /api/v1/maintenance/purge-deleted-library?library_id=UUID.
// Hard-removes every media_items row for an already-soft-deleted
// library; FK cascades clean up media_files, watch_state, favorites,
// collection memberships, intro markers, etc. Refuses to operate on
// a live library — the SQL itself enforces the soft-delete gate, so
// passing a live library_id returns purged_items=0 without touching
// anything.
//
// Use case: a library was deleted-and-recreated at the same scan
// path before 00080 / the cascade-soft-delete fix landed, leaving
// orphaned media_files rows that block the new library's scanner
// from claiming the same paths (symptom: scan reports
// found:N / new:0). This endpoint cleans up those orphans so the
// new library can re-scan from a clean slate.
//
// Returns 202 Accepted and runs the DELETE on a detached background
// goroutine, because the cascade through every FK-CASCADE table
// (media_files, watch_state, watch_events, favorites,
// collection_items, intro_markers, trickplay_*, external_subtitles,
// people_credits, ...) for a multi-thousand-item library reliably
// blows past Cloudflare's 100s edge timeout. The synchronous variant
// hit ERR 524 on QA and the request-context cancellation rolled the
// transaction back, leaving the orphans in place. Caller polls
// /admin/logs for "purge deleted library complete" / failure.
//
// Admin-only — mounted under AdminRequired in router.go.
func (h *MaintenanceHandler) PurgeDeletedLibrary(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("library_id")
	if raw == "" {
		respond.BadRequest(w, r, "library_id is required")
		return
	}
	libID, err := uuid.Parse(raw)
	if err != nil {
		respond.BadRequest(w, r, "invalid library_id")
		return
	}

	// Detach the context so an HTTP-level cancellation (Cloudflare
	// 524 timeout) doesn't roll back the transaction halfway through.
	bgCtx := context.WithoutCancel(r.Context())
	go func() {
		n, err := h.library.PurgeDeleted(bgCtx, libID)
		if err != nil {
			h.logger.ErrorContext(bgCtx, "purge deleted library",
				"library_id", libID, "err", err)
			return
		}
		h.logger.InfoContext(bgCtx, "purge deleted library complete",
			"library_id", libID, "purged_items", n)
	}()

	h.logger.InfoContext(r.Context(), "purge deleted library enqueued",
		"library_id", libID)

	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"data":{"status":"accepted"}}`))
}
