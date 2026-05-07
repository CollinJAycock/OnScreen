package v1

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// MaintenanceMediaService is the slice of media.Service that maintenance
// endpoints depend on. Kept narrow so tests can provide fakes easily.
type MaintenanceMediaService interface {
	ListItemsMissingArt(ctx context.Context, limit int32) ([]media.Item, error)
	DedupeTopLevelItems(ctx context.Context, itemType string, libraryID *uuid.UUID) (media.DedupeResult, error)
}

// MaintenanceLibraryService is the slice of library.Service the
// purge endpoint needs. Separate from MaintenanceMediaService so
// the existing media-only handlers can keep their narrow surface.
type MaintenanceLibraryService interface {
	PurgeDeleted(ctx context.Context, id uuid.UUID) (int64, error)
}

// MaintenanceHandler exposes admin-only one-shot operations such as backfilling
// missing artwork after a new metadata source (e.g. TVDB key) is configured.
type MaintenanceHandler struct {
	media    MaintenanceMediaService
	library  MaintenanceLibraryService
	enricher ItemEnricher
	logger   *slog.Logger
}

// NewMaintenanceHandler creates a MaintenanceHandler.
func NewMaintenanceHandler(svc MaintenanceMediaService, lib MaintenanceLibraryService, enricher ItemEnricher, logger *slog.Logger) *MaintenanceHandler {
	return &MaintenanceHandler{media: svc, library: lib, enricher: enricher, logger: logger}
}

// RefreshMissingArt handles POST /api/v1/maintenance/refresh-missing-art.
// It re-runs metadata enrichment for up to ?limit=N (default 200, max 1000)
// top-level items that currently have no poster. Successes and failures are
// counted individually so one bad item doesn't abort the batch.
func (h *MaintenanceHandler) RefreshMissingArt(w http.ResponseWriter, r *http.Request) {
	limit := respond.ParseLimit(r, 200, 1000)

	items, err := h.media.ListItemsMissingArt(r.Context(), limit)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list items missing art", "err", err)
		respond.InternalError(w, r)
		return
	}

	type failure struct {
		ID    uuid.UUID `json:"id"`
		Title string    `json:"title"`
		Error string    `json:"error"`
	}
	var (
		refreshed int
		failed    []failure
	)
	for _, it := range items {
		if err := h.enricher.EnrichItem(r.Context(), it.ID); err != nil {
			h.logger.WarnContext(r.Context(), "refresh missing art failed",
				"item_id", it.ID, "title", it.Title, "err", err)
			failed = append(failed, failure{ID: it.ID, Title: it.Title, Error: err.Error()})
			continue
		}
		refreshed++
	}

	h.logger.InfoContext(r.Context(), "refresh missing art complete",
		"candidates", len(items), "refreshed", refreshed, "failed", len(failed))

	respond.Success(w, r, map[string]any{
		"candidates": len(items),
		"refreshed":  refreshed,
		"failed":     failed,
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
