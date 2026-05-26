package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/scanner"
)

// ItemBulkAdminDB exposes the narrow database queries the bulk-re-enrich
// admin tool needs. Defined here (not as a method on the larger
// ItemMediaService) because this is a one-off recovery operation that
// shouldn't pollute the day-to-day service interface.
type ItemBulkAdminDB interface {
	ListUnmatchedTopLevelItems(ctx context.Context, arg gen.ListUnmatchedTopLevelItemsParams) ([]gen.ListUnmatchedTopLevelItemsRow, error)
	ListMediaItemsMissingArt(ctx context.Context, limit int32) ([]gen.ListMediaItemsMissingArtRow, error)
	UpdateMediaItemTitle(ctx context.Context, arg gen.UpdateMediaItemTitleParams) error
}

// ItemBulkAdminHandler implements POST /api/v1/admin/items/re-enrich-unmatched.
// Lets an operator recover top-level items (movies + shows) that the scanner
// couldn't match on TMDB — typically shows whose stored title still has a
// `[release-group]` prefix that poisoned the search query before the
// cleanTitle bracket-strip fix landed.
type ItemBulkAdminHandler struct {
	db       ItemBulkAdminDB
	enricher ItemEnricher
	audit    *audit.Logger
	logger   *slog.Logger
}

// NewItemBulkAdminHandler constructs the handler. enricher may be nil in
// tests / setups where TMDB isn't configured; in that case the route
// returns 503 with a clear message.
func NewItemBulkAdminHandler(db ItemBulkAdminDB, enricher ItemEnricher, logger *slog.Logger) *ItemBulkAdminHandler {
	return &ItemBulkAdminHandler{db: db, enricher: enricher, logger: logger}
}

// WithAudit wires the audit logger so admin invocations are recorded.
func (h *ItemBulkAdminHandler) WithAudit(a *audit.Logger) *ItemBulkAdminHandler {
	h.audit = a
	return h
}

// unmatchedItem is the per-row shape for GET /admin/items/unmatched.
// Mirrors the fields the frontend needs to render the "Fix Match" tray:
// ID for the per-item match call, library_id for scoping, type +
// title + year for display.
type unmatchedItem struct {
	ID        string `json:"id"`
	LibraryID string `json:"library_id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Year      *int32 `json:"year,omitempty"`
}

type unmatchedListResponse struct {
	Items []unmatchedItem `json:"items"`
	Total int             `json:"total"`
}

// missingArtItem is the per-row shape for GET /admin/items/missing-art.
// Mirrors unmatchedItem but adds tmdb_id so the missing-art tray can
// kick the existing TMDB poster picker (POST /items/{id}/posters
// needs a tmdb id) without an extra round-trip per row.
type missingArtItem struct {
	ID        string `json:"id"`
	LibraryID string `json:"library_id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Year      *int32 `json:"year,omitempty"`
	TMDBID    *int   `json:"tmdb_id,omitempty"`
}

type missingArtListResponse struct {
	Items []missingArtItem `json:"items"`
	Total int              `json:"total"`
}

// ListMissingArt handles GET /api/v1/admin/items/missing-art. Returns
// every top-level movie/show with no poster_path. Used by the admin
// "Set poster" tray so the operator can pick a TMDB poster variant
// or paste a URL for items where the auto-fetched poster came back
// empty. List-only — mutations go through the existing /items/{id}/
// posters and /items/{id}/poster endpoints.
func (h *ItemBulkAdminHandler) ListMissingArt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}

	limit := respond.ParseLimit(r, 200, 1000)
	rows, err := h.db.ListMediaItemsMissingArt(ctx, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "list items missing art", "err", err)
		respond.InternalError(w, r)
		return
	}

	items := make([]missingArtItem, 0, len(rows))
	for _, row := range rows {
		entry := missingArtItem{
			ID:        row.ID.String(),
			LibraryID: row.LibraryID.String(),
			Type:      row.Type,
			Title:     row.Title,
		}
		if row.Year != nil {
			y := *row.Year
			entry.Year = &y
		}
		if row.TmdbID != nil {
			v := int(*row.TmdbID)
			entry.TMDBID = &v
		}
		items = append(items, entry)
	}

	respond.Success(w, r, missingArtListResponse{
		Items: items,
		Total: len(items),
	})
}

// ListUnmatched handles GET /api/v1/admin/items/unmatched. Returns
// every top-level movie/show with no provider IDs (tmdb / tvdb /
// imdb). Used by the admin Fix Match tray as the data feed; the
// per-item search + apply path uses the existing
// /api/v1/items/{id}/match/search and /api/v1/items/{id}/match
// endpoints (matchShow / matchMovie internally), so this endpoint
// only does the list — no mutation.
func (h *ItemBulkAdminHandler) ListUnmatched(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}

	limit := respond.ParseLimit(r, 200, 1000)

	params := gen.ListUnmatchedTopLevelItemsParams{
		ResultLimit: limit,
	}
	if raw := r.URL.Query().Get("library_id"); raw != "" {
		libID, err := uuid.Parse(raw)
		if err != nil {
			respond.BadRequest(w, r, "invalid library_id")
			return
		}
		params.LibraryID = pgtype.UUID{Bytes: libID, Valid: true}
	}

	rows, err := h.db.ListUnmatchedTopLevelItems(ctx, params)
	if err != nil {
		h.logger.ErrorContext(ctx, "list unmatched items", "err", err)
		respond.InternalError(w, r)
		return
	}

	items := make([]unmatchedItem, 0, len(rows))
	for _, row := range rows {
		entry := unmatchedItem{
			ID:        row.ID.String(),
			LibraryID: row.LibraryID.String(),
			Type:      row.Type,
			Title:     row.Title,
		}
		if row.Year != nil {
			y := *row.Year
			entry.Year = &y
		}
		items = append(items, entry)
	}

	respond.Success(w, r, unmatchedListResponse{
		Items: items,
		Total: len(items),
	})
}

type reEnrichUnmatchedRequest struct {
	LibraryID *string `json:"library_id,omitempty"` // optional UUID; when set, scope to that library
	DryRun    bool    `json:"dry_run,omitempty"`    // when true, return candidates without modifying anything
	Limit     int     `json:"limit,omitempty"`      // max items to process this call; defaults to 50
}

type reEnrichUnmatchedItem struct {
	ID            string `json:"id"`
	LibraryID     string `json:"library_id"`
	Type          string `json:"type"`
	OldTitle      string `json:"old_title"`
	NewTitle      string `json:"new_title,omitempty"`      // present when title was rewritten
	TitleCleaned  bool   `json:"title_cleaned"`            // true when StripReleaseGroupPrefix changed the title
	EnrichQueued  bool   `json:"enrich_queued"`            // true when EnrichItem was scheduled
}

type reEnrichUnmatchedResponse struct {
	Found            int                       `json:"found"`             // total candidates returned by the query
	TitlesCleaned    int                       `json:"titles_cleaned"`    // how many had their title rewritten
	EnrichmentQueued int                       `json:"enrichment_queued"` // how many were queued for re-enrichment
	DryRun           bool                      `json:"dry_run"`
	Items            []reEnrichUnmatchedItem   `json:"items"`
}

// ReEnrichUnmatched handles POST /api/v1/admin/items/re-enrich-unmatched.
//
// Body (all fields optional):
//
//	{"library_id":"uuid", "dry_run":true, "limit":50}
//
// Returns the candidates with per-item action breakdown. With dry_run=true,
// the title rewrite + enrichment queueing are skipped — the caller can
// preview the effect before committing.
//
// Enrichment is queued in a single background goroutine that processes the
// list serially. EnrichItem is synchronous internally and the metadata
// agent applies its own rate limiting; serializing here keeps a 50-item
// burst from spiking TMDB load.
func (h *ItemBulkAdminHandler) ReEnrichUnmatched(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	if h.enricher == nil {
		respond.BadRequest(w, r, "metadata enrichment not configured")
		return
	}

	var body reEnrichUnmatchedRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond.BadRequest(w, r, "invalid JSON body")
			return
		}
	}

	limit := body.Limit
	if limit <= 0 {
		limit = 200
	}
	// 1000 keeps a single pass bounded for QA-style catch-up runs that
	// might face thousands of NFO-only items at once. The worker grinds
	// serially so a 1000-item batch translates to 1000 EnrichItem calls
	// against the rate-limited TMDB client — finishes when it finishes,
	// but doesn't fight other work in the meantime.
	if limit > 1000 {
		limit = 1000
	}

	listParams := gen.ListUnmatchedTopLevelItemsParams{
		ResultLimit: int32(limit),
	}
	if body.LibraryID != nil && *body.LibraryID != "" {
		libID, err := uuid.Parse(*body.LibraryID)
		if err != nil {
			respond.BadRequest(w, r, "invalid library_id")
			return
		}
		listParams.LibraryID = pgtype.UUID{Bytes: libID, Valid: true}
	}

	rows, err := h.db.ListUnmatchedTopLevelItems(ctx, listParams)
	if err != nil {
		h.logger.ErrorContext(ctx, "list unmatched items", "err", err)
		respond.InternalError(w, r)
		return
	}

	resp := reEnrichUnmatchedResponse{
		DryRun: body.DryRun,
		Items:  make([]reEnrichUnmatchedItem, 0, len(rows)),
	}
	resp.Found = len(rows)

	// Deferred-enrichment list: only collect the IDs we'll actually
	// queue, after any synchronous title cleanup has happened.
	enrichIDs := make([]uuid.UUID, 0, len(rows))

	for _, row := range rows {
		oldTitle := row.Title
		newTitle := scanner.StripReleaseGroupPrefix(oldTitle)
		newTitle = strings.TrimSpace(newTitle)

		entry := reEnrichUnmatchedItem{
			ID:        row.ID.String(),
			LibraryID: row.LibraryID.String(),
			Type:      row.Type,
			OldTitle:  oldTitle,
		}

		if newTitle != oldTitle && newTitle != "" {
			entry.NewTitle = newTitle
			entry.TitleCleaned = true
			if !body.DryRun {
				if err := h.db.UpdateMediaItemTitle(ctx, gen.UpdateMediaItemTitleParams{
					ID:        row.ID,
					Title:     newTitle,
					SortTitle: newTitle,
				}); err != nil {
					h.logger.WarnContext(ctx, "update title for re-enrich",
						"item_id", row.ID, "err", err)
					// Still queue enrichment — the cleanTitle in the
					// enricher will apply the same strip in-memory before
					// the TMDB search; the persisted title just stays
					// dirty until next time.
				} else {
					resp.TitlesCleaned++
				}
			} else {
				resp.TitlesCleaned++
			}
		}

		if !body.DryRun {
			enrichIDs = append(enrichIDs, row.ID)
			entry.EnrichQueued = true
			resp.EnrichmentQueued++
		} else {
			entry.EnrichQueued = true // would have been queued
			resp.EnrichmentQueued++
		}

		resp.Items = append(resp.Items, entry)
	}

	if h.audit != nil {
		actor := claims.UserID
		h.audit.Log(ctx, &actor, audit.ActionItemEnrich, "bulk:unmatched",
			map[string]any{
				"found":             resp.Found,
				"titles_cleaned":    resp.TitlesCleaned,
				"enrichment_queued": resp.EnrichmentQueued,
				"dry_run":           body.DryRun,
				"library_id":        body.LibraryID,
			}, audit.ClientIP(r))
	}

	if !body.DryRun && len(enrichIDs) > 0 {
		// Queue the per-item enrichment serially in the background. The
		// list is bounded by `limit` (default 50), so worst case is a
		// 50-item TMDB walk that finishes in seconds. Errors are logged
		// per-item; one failure doesn't abort the rest.
		bgCtx := context.WithoutCancel(ctx)
		ids := append([]uuid.UUID(nil), enrichIDs...)
		observability.SafeGo(h.logger, "items-admin:bulk-reenrich", func() {
			for _, id := range ids {
				if err := h.enricher.EnrichItem(bgCtx, id); err != nil {
					h.logger.WarnContext(bgCtx, "bulk re-enrich item failed",
						"item_id", id, "err", err)
				}
			}
			h.logger.InfoContext(bgCtx, "bulk re-enrich finished",
				"count", len(ids))
		})
	}

	respond.Success(w, r, resp)
}
