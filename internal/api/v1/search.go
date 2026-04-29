package v1

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// SearchDB defines the database queries the search handler needs.
type SearchDB interface {
	SearchMediaItems(ctx context.Context, arg gen.SearchMediaItemsParams) ([]gen.SearchMediaItemsRow, error)
	SearchMediaItemsGlobal(ctx context.Context, arg gen.SearchMediaItemsGlobalParams) ([]gen.SearchMediaItemsGlobalRow, error)
}

// SearchHandler serves media search results.
type SearchHandler struct {
	db     SearchDB
	access LibraryAccessChecker
	logger *slog.Logger
}

// NewSearchHandler creates a SearchHandler.
func NewSearchHandler(db SearchDB, logger *slog.Logger) *SearchHandler {
	return &SearchHandler{db: db, logger: logger}
}

// WithLibraryAccess enables per-user library filtering on search results.
func (h *SearchHandler) WithLibraryAccess(a LibraryAccessChecker) *SearchHandler {
	h.access = a
	return h
}

// SearchResult is a compact result for search display.
type SearchResult struct {
	ID         string  `json:"id"`
	LibraryID  string  `json:"library_id"`
	Title      string  `json:"title"`
	Type       string  `json:"type"`
	Year       *int    `json:"year,omitempty"`
	PosterPath *string `json:"poster_path,omitempty"`
	ThumbPath  *string `json:"thumb_path,omitempty"`
}

// Search handles GET /api/v1/search?q=...&library_id=...&limit=20.
func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		respond.BadRequest(w, r, "search query required")
		return
	}

	limit := respond.ParseLimit(r, 20, 100)

	var results []SearchResult
	var err error

	// Extract content rating filter from auth claims.
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	maxRank := maxRatingRankFromClaims(claims.MaxContentRating)

	// Pre-compute allowed library set. Nil means admin → no filtering.
	var allowed map[uuid.UUID]struct{}
	if h.access != nil {
		allowed, err = h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "search: allowed libraries", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	libAllowed := func(id uuid.UUID) bool {
		if allowed == nil {
			return true
		}
		_, ok := allowed[id]
		return ok
	}

	if libID := r.URL.Query().Get("library_id"); libID != "" {
		uid, parseErr := uuid.Parse(libID)
		if parseErr != nil {
			respond.BadRequest(w, r, "invalid library_id")
			return
		}
		if !libAllowed(uid) {
			respond.Success(w, r, []SearchResult{})
			return
		}
		rows, qErr := h.db.SearchMediaItems(r.Context(), gen.SearchMediaItemsParams{
			LibraryID:          uid,
			WebsearchToTsquery: query,
			Limit:              limit,
			MaxRatingRank:      maxRank,
		})
		err = qErr
		results = make([]SearchResult, 0, len(rows))
		for _, row := range rows {
			results = append(results, SearchResult{
				ID: row.ID.String(), LibraryID: row.LibraryID.String(),
				Title: row.Title, Type: row.Type,
				Year: intPtrFrom32(row.Year), PosterPath: row.PosterPath, ThumbPath: row.ThumbPath,
			})
		}
	} else {
		rows, qErr := h.db.SearchMediaItemsGlobal(r.Context(), gen.SearchMediaItemsGlobalParams{
			WebsearchToTsquery: query,
			Limit:              limit,
			MaxRatingRank:      maxRank,
		})
		err = qErr
		results = make([]SearchResult, 0, len(rows))
		for _, row := range rows {
			if !libAllowed(row.LibraryID) {
				continue
			}
			results = append(results, SearchResult{
				ID: row.ID.String(), LibraryID: row.LibraryID.String(),
				Title: row.Title, Type: row.Type,
				Year: intPtrFrom32(row.Year), PosterPath: row.PosterPath, ThumbPath: row.ThumbPath,
			})
		}
	}

	if err != nil {
		// User typing fast in the search box cancels the prior
		// in-flight query (ctx is request-scoped); log that at debug
		// instead of error and skip InternalError — the response
		// connection is already gone, nothing to write.
		if respond.IsClientGone(err) {
			h.logger.DebugContext(r.Context(), "search query cancelled by client", "err", err)
			return
		}
		h.logger.ErrorContext(r.Context(), "search query failed", "err", err)
		respond.InternalError(w, r)
		return
	}

	respond.Success(w, r, results)
}
