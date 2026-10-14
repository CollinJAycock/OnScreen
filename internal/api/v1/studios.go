package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/studios"
)

// StudioService is the studios surface the handler needs. Satisfied by
// *studios.Service.
type StudioService interface {
	Get(ctx context.Context, id uuid.UUID) (*studios.Studio, error)
	List(ctx context.Context, libraryID *uuid.UUID, kind string, scope studios.Scope) ([]studios.Studio, error)
	Items(ctx context.Context, p studios.ItemsParams) ([]media.Item, int64, error)
}

// StudioHandler serves the studio / network browse pages:
// GET /api/v1/studios, /studios/{id} and /studios/{id}/items.
//
// A studio has no visibility of its own, so every response is computed
// through its items with the caller's three rules — library grants, rating
// ceiling and label restriction — and a studio none of whose items the
// caller can see is a 404, indistinguishable from one that doesn't exist.
type StudioHandler struct {
	svc    StudioService
	access LibraryAccessChecker
	logger *slog.Logger
}

// NewStudioHandler returns a StudioHandler.
func NewStudioHandler(svc StudioService, logger *slog.Logger) *StudioHandler {
	return &StudioHandler{svc: svc, logger: logger}
}

// WithLibraryAccess wires the per-user library ACL. nil = no gate (dev/test).
func (h *StudioHandler) WithLibraryAccess(a LibraryAccessChecker) *StudioHandler {
	h.access = a
	return h
}

// LibraryAccessWired reports whether the library-ACL checker is wired (see
// api.Handlers.ValidateLibraryAccess).
func (h *StudioHandler) LibraryAccessWired() bool { return h.access != nil }

// StudioResponse is one studio or network.
type StudioResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"` // "studio" | "network"
	TMDBID *int   `json:"tmdb_id,omitempty"`
	// ItemCount is how many of its movies / shows the caller can see
	// (within ?library_id= when given).
	ItemCount int64 `json:"item_count"`
}

// scope builds the caller's visibility scope. ok=false means a response was
// written (an internal error).
func (h *StudioHandler) scope(w http.ResponseWriter, r *http.Request) (studios.Scope, bool) {
	var sc studios.Scope
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return sc, false
	}
	sc.MaxRatingRank = contentrating.MaxRatingRank(claims.MaxContentRating)
	sc.Labels = claims.LabelFilter()
	if h.access != nil {
		allowed, err := h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "studios: allowed libraries", "err", err)
			respond.InternalError(w, r)
			return sc, false
		}
		sc.LibraryIDs = allowedLibraryList(allowed) // nil = admin; empty = no grants
	}
	return sc, true
}

// libraryParam reads ?library_id=, 404ing a library the caller can't open
// (the same answer as a library that doesn't exist). ok=false means a
// response was written.
func (h *StudioHandler) libraryParam(w http.ResponseWriter, r *http.Request) (*uuid.UUID, bool) {
	raw := r.URL.Query().Get("library_id")
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		respond.BadRequest(w, r, "invalid library_id")
		return nil, false
	}
	if h.access != nil {
		claims := middleware.ClaimsFromContext(r.Context())
		ok, err := h.access.CanAccessLibrary(r.Context(), claims.UserID, id, claims.IsAdmin)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "studios: check library access", "library_id", id, "err", err)
			respond.InternalError(w, r)
			return nil, false
		}
		if !ok {
			respond.NotFound(w, r)
			return nil, false
		}
	}
	return &id, true
}

// List handles GET /api/v1/studios?library_id=&kind=. Studios and networks
// with at least one item the caller can see, with counts, by name.
func (h *StudioHandler) List(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.scope(w, r)
	if !ok {
		return
	}
	libraryID, ok := h.libraryParam(w, r)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind != "" && !studios.ValidKind(kind) {
		respond.BadRequest(w, r, "kind must be studio or network")
		return
	}
	list, err := h.svc.List(r.Context(), libraryID, kind, sc)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list studios", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]StudioResponse, len(list))
	for i, s := range list {
		out[i] = StudioResponse{ID: s.ID.String(), Name: s.Name, Kind: s.Kind, TMDBID: s.TMDBID, ItemCount: s.ItemCount}
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// Get handles GET /api/v1/studios/{id}: the studio and how many of its items
// the caller can see. 404 when that's none.
func (h *StudioHandler) Get(w http.ResponseWriter, r *http.Request) {
	st, sc, ok := h.studio(w, r)
	if !ok {
		return
	}
	_, total, err := h.svc.Items(r.Context(), studios.ItemsParams{StudioID: st.ID, Scope: sc, Limit: 1})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "studio items count", "id", st.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if total == 0 {
		respond.NotFound(w, r)
		return
	}
	respond.Success(w, r, StudioResponse{ID: st.ID.String(), Name: st.Name, Kind: st.Kind, TMDBID: st.TMDBID, ItemCount: total})
}

// Items handles GET /api/v1/studios/{id}/items: the studio's movies or
// shows the caller can see, paged (limit / offset) and sorted (sort = title
// | year | rating | created_at, sort_dir = asc | desc) like library browse,
// optionally within ?library_id=. Same item shape as the library listing.
func (h *StudioHandler) Items(w http.ResponseWriter, r *http.Request) {
	st, sc, ok := h.studio(w, r)
	if !ok {
		return
	}
	libraryID, ok := h.libraryParam(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	sort := q.Get("sort")
	if !studios.ValidSort(sort) {
		respond.BadRequest(w, r, "sort must be title, year, rating or created_at")
		return
	}
	asc := true
	switch q.Get("sort_dir") {
	case "asc":
	case "desc":
		asc = false
	default:
		// Same defaults as library browse: newest / best / latest first.
		asc = sort == "" || sort == "title"
	}
	page := respond.ParsePagination(r, 50, 200)
	items, total, err := h.svc.Items(r.Context(), studios.ItemsParams{
		StudioID:  st.ID,
		LibraryID: libraryID,
		Scope:     sc,
		Sort:      sort,
		SortAsc:   asc,
		Limit:     page.Limit,
		Offset:    page.Offset,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "studio items", "id", st.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	// A studio whose every title is out of the caller's reach doesn't
	// exist for them (only checked unfiltered by library: an empty library
	// slice of a visible studio is an honest empty page).
	if total == 0 && libraryID == nil {
		respond.NotFound(w, r)
		return
	}
	out := make([]MediaItemResponse, len(items))
	for i, item := range items {
		out[i] = MediaItemResponse{
			ID:            item.ID.String(),
			Title:         item.Title,
			Type:          item.Type,
			Year:          item.Year,
			Summary:       item.Summary,
			Rating:        item.Rating,
			DurationMS:    item.DurationMS,
			Genres:        item.Genres,
			PosterPath:    item.PosterPath,
			OriginalTitle: item.OriginalTitle,
			TakenAt:       item.OriginallyAvailableAt,
			CreatedAt:     item.CreatedAt,
			UpdatedAt:     item.UpdatedAt.UnixMilli(),
		}
	}
	respond.List(w, r, out, total, "")
}

// studio resolves {id} and the caller's scope. ok=false means a response was
// written.
func (h *StudioHandler) studio(w http.ResponseWriter, r *http.Request) (*studios.Studio, studios.Scope, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid studio id")
		return nil, studios.Scope{}, false
	}
	sc, ok := h.scope(w, r)
	if !ok {
		return nil, sc, false
	}
	st, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, studios.ErrNotFound) {
			respond.NotFound(w, r)
		} else {
			h.logger.ErrorContext(r.Context(), "get studio", "id", id, "err", err)
			respond.InternalError(w, r)
		}
		return nil, sc, false
	}
	return st, sc, true
}
