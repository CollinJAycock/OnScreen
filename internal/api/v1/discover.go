package v1

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata/tmdb"
	"github.com/onscreen/onscreen/internal/requests"
)

// DiscoverDB is the slice of generated queries Discover needs. Defined as
// an interface so tests can wire an in-memory fake.
type DiscoverDB interface {
	ListMediaItemsByTMDBIDs(ctx context.Context, arg gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error)
}

// DiscoverTMDB is the TMDB capability surface — only the multi-search call.
// Receiving the *tmdb.Client directly would couple this handler to the
// dynamic agent indirection in main.go; an interface keeps the wiring in
// the adapter where it already lives.
type DiscoverTMDB interface {
	SearchMulti(ctx context.Context, query string, maxResults int) ([]tmdb.DiscoverResult, error)
}

// DiscoverRequestLookup is the requests.Service surface used to mark a hit
// as already-requested by the calling user. Defined as an interface so the
// handler can be tested without spinning up the full request workflow.
type DiscoverRequestLookup interface {
	FindActiveForUser(ctx context.Context, userID uuid.UUID, mediaType string, tmdbID int) (*gen.MediaRequest, error)
}

// DiscoverHandler powers the Request UI's discover surface: a single TMDB
// search-multi call enriched with library and request state so the user
// sees "in your library", "you already requested this", or "request now"
// without an extra round-trip per result.
type DiscoverHandler struct {
	db       DiscoverDB
	tmdb     DiscoverTMDB
	requests DiscoverRequestLookup
	logger   *slog.Logger
}

// NewDiscoverHandler builds a handler. tmdb may be nil — the endpoint will
// then return a clean 503 instead of crashing, so the UI can show the
// "configure TMDB to enable Discover" copy.
func NewDiscoverHandler(db DiscoverDB, t DiscoverTMDB, reqs DiscoverRequestLookup, logger *slog.Logger) *DiscoverHandler {
	return &DiscoverHandler{db: db, tmdb: t, requests: reqs, logger: logger}
}

// DiscoverItem is one row in the Discover response. The shape is flat on
// purpose so the request UI can render directly without a normalizer.
type DiscoverItem struct {
	Type        string  `json:"type"` // "movie" | "show"
	TMDBID      int     `json:"tmdb_id"`
	Title       string  `json:"title"`
	Year        int     `json:"year,omitempty"`
	Overview    string  `json:"overview,omitempty"`
	Rating      float64 `json:"rating,omitempty"`
	PosterURL   string  `json:"poster_url,omitempty"`
	FanartURL   string  `json:"fanart_url,omitempty"`
	InLibrary   bool    `json:"in_library"`
	LibraryItem *string `json:"library_item_id,omitempty"`
	// Request state from the perspective of the requesting user.
	HasActiveRequest bool       `json:"has_active_request"`
	ActiveRequestID  *uuid.UUID `json:"active_request_id,omitempty"`
	ActiveStatus     *string    `json:"active_request_status,omitempty"`
}

// Search handles GET /api/v1/discover/search?q=...&limit=20.
//
// Returns up to `limit` results (capped at 50) from TMDB's /search/multi,
// each marked with whether it's already in the library and whether the
// caller already has an open request for it.
func (h *DiscoverHandler) Search(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	if h.tmdb == nil {
		respond.Error(w, r, http.StatusServiceUnavailable, "TMDB_UNAVAILABLE",
			"TMDB API key is not configured — Discover requires TMDB to enrich titles")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		respond.BadRequest(w, r, "search query required")
		return
	}
	limit := int(respond.ParseLimit(r, 20, 50))

	results, err := h.tmdb.SearchMulti(r.Context(), query, limit)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "tmdb search multi failed", "q", query, "err", err)
		respond.InternalError(w, r)
		return
	}

	out := make([]DiscoverItem, 0, len(results))
	movieIDs := make([]int32, 0, len(results))
	showIDs := make([]int32, 0, len(results))
	for _, row := range results {
		item := DiscoverItem{
			Type:      normalizeType(row.MediaType),
			TMDBID:    row.TMDBID,
			Title:     row.Title,
			Year:      row.Year,
			Overview:  row.Overview,
			Rating:    row.Rating,
			PosterURL: row.PosterURL,
			FanartURL: row.FanartURL,
		}
		switch item.Type {
		case requests.TypeMovie:
			movieIDs = append(movieIDs, int32(row.TMDBID))
		case requests.TypeShow:
			showIDs = append(showIDs, int32(row.TMDBID))
		}
		out = append(out, item)
	}

	// Batched library lookup: one query per type. TMDB IDs are unique within
	// a type, so the result map is keyed by tmdb_id alone.
	movieHits := h.lookupLibrary(r.Context(), requests.TypeMovie, movieIDs)
	showHits := h.lookupLibrary(r.Context(), requests.TypeShow, showIDs)

	for i := range out {
		var hits map[int32]uuid.UUID
		switch out[i].Type {
		case requests.TypeMovie:
			hits = movieHits
		case requests.TypeShow:
			hits = showHits
		}
		if id, ok := hits[int32(out[i].TMDBID)]; ok {
			out[i].InLibrary = true
			s := id.String()
			out[i].LibraryItem = &s
		}

		// Per-result request lookup. Discover surfaces ~20 rows so this is
		// cheap; batching becomes worthwhile only if the row cap grows.
		if h.requests != nil {
			req, lerr := h.requests.FindActiveForUser(r.Context(), claims.UserID, out[i].Type, out[i].TMDBID)
			if lerr != nil {
				h.logger.WarnContext(r.Context(), "discover: lookup active request",
					"tmdb_id", out[i].TMDBID, "type", out[i].Type, "err", lerr)
				continue
			}
			if req != nil {
				out[i].HasActiveRequest = true
				id := req.ID
				out[i].ActiveRequestID = &id
				status := req.Status
				out[i].ActiveStatus = &status
			}
		}
	}

	respond.Success(w, r, out)
}

func (h *DiscoverHandler) lookupLibrary(ctx context.Context, mediaType string, ids []int32) map[int32]uuid.UUID {
	if len(ids) == 0 {
		return nil
	}
	rows, err := h.db.ListMediaItemsByTMDBIDs(ctx, gen.ListMediaItemsByTMDBIDsParams{
		Type:    libraryItemType(mediaType),
		TmdbIds: ids,
	})
	if err != nil {
		// Log and degrade — Discover still returns TMDB rows without the
		// library/request decoration rather than failing the whole request.
		h.logger.WarnContext(ctx, "discover: library lookup failed",
			"type", mediaType, "err", err)
		return nil
	}
	out := make(map[int32]uuid.UUID, len(rows))
	for _, row := range rows {
		if row.TmdbID == nil {
			continue
		}
		out[*row.TmdbID] = row.ID
	}
	return out
}

// normalizeType maps TMDB's "tv" to the OnScreen "show" string used
// everywhere else (media_items.type, media_requests.type, the request DTO).
func normalizeType(mediaType string) string {
	if mediaType == "tv" {
		return requests.TypeShow
	}
	return requests.TypeMovie
}

// libraryItemType is the inverse mapping: media_items stores movies as
// "movie" and shows as "show", matching the request type alphabet.
func libraryItemType(mediaType string) string {
	switch mediaType {
	case requests.TypeShow:
		return "show"
	default:
		return "movie"
	}
}

// Compile-time guard: requests.Service satisfies DiscoverRequestLookup so
// the wiring in main.go can pass *requests.Service directly.
var _ DiscoverRequestLookup = (*requests.Service)(nil)
