package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/domain/people"
)

// PeopleService is the people domain operations the handler needs.
type PeopleService interface {
	GetCredits(ctx context.Context, itemID uuid.UUID, itemType string, tmdbID *int) ([]people.Credit, error)
	GetPerson(ctx context.Context, id uuid.UUID) (people.Person, error)
	GetFilmography(ctx context.Context, personID uuid.UUID) ([]people.FilmographyEntry, error)
	Search(ctx context.Context, prefix string, limit int32) ([]people.Summary, error)
}

// PeopleItemLookup is the minimum item info the credits endpoint needs to
// drive the lazy TMDB fetch. ResolveTMDBID is called when an item lacks a
// stored tmdb_id — it searches TMDB by title+year and persists the match so
// future requests skip the search.
type PeopleItemLookup interface {
	GetItemTypeAndTMDB(ctx context.Context, id uuid.UUID) (itemType string, tmdbID *int, err error)
	ResolveTMDBID(ctx context.Context, id uuid.UUID) (*int, error)
	// ItemAccessInfo returns the owning library and content rating so the
	// credits endpoint can enforce the library ACL + content-rating ceiling.
	// found=false when the item doesn't exist.
	ItemAccessInfo(ctx context.Context, id uuid.UUID) (libraryID uuid.UUID, contentRating string, found bool)
}

type PeopleHandler struct {
	svc    PeopleService
	items  PeopleItemLookup
	access LibraryAccessChecker
	logger *slog.Logger
}

func NewPeopleHandler(svc PeopleService, items PeopleItemLookup, logger *slog.Logger) *PeopleHandler {
	return &PeopleHandler{svc: svc, items: items, logger: logger}
}

// WithLibraryAccess wires the per-user library ACL + (via claims) content-rating
// ceiling used to gate credits and filter filmography. nil = no gate (dev/test).
func (h *PeopleHandler) WithLibraryAccess(a LibraryAccessChecker) *PeopleHandler {
	h.access = a
	return h
}

type personSummaryResponse struct {
	ID          uuid.UUID `json:"id"`
	TMDBID      *int      `json:"tmdb_id,omitempty"`
	Name        string    `json:"name"`
	ProfilePath *string   `json:"profile_path,omitempty"`
}

type creditResponse struct {
	Person    personSummaryResponse `json:"person"`
	Role      string                `json:"role"`
	Character string                `json:"character,omitempty"`
	Job       string                `json:"job,omitempty"`
	Order     int                   `json:"order"`
}

type personResponse struct {
	ID           uuid.UUID  `json:"id"`
	TMDBID       *int       `json:"tmdb_id,omitempty"`
	Name         string     `json:"name"`
	ProfilePath  *string    `json:"profile_path,omitempty"`
	Bio          *string    `json:"bio,omitempty"`
	Birthday     *time.Time `json:"birthday,omitempty"`
	Deathday     *time.Time `json:"deathday,omitempty"`
	PlaceOfBirth *string    `json:"place_of_birth,omitempty"`
}

type filmographyEntryResponse struct {
	ItemID     uuid.UUID `json:"item_id"`
	LibraryID  uuid.UUID `json:"library_id"`
	Title      string    `json:"title"`
	Type       string    `json:"type"`
	Year       *int      `json:"year,omitempty"`
	PosterPath *string   `json:"poster_path,omitempty"`
	Rating     *float64  `json:"rating,omitempty"`
	Role       string    `json:"role"`
	Character  string    `json:"character,omitempty"`
	Job        string    `json:"job,omitempty"`
}

// Credits handles GET /api/v1/items/{id}/credits.
// First call may be slow (lazy TMDB fetch); subsequent calls hit the DB.
func (h *PeopleHandler) Credits(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid id")
		return
	}
	// Access gate BEFORE GetCredits — credits is an item-scoped endpoint and on a
	// cache miss drives a lazy TMDB fetch on the operator's quota, so a caller
	// who can't see the item must be turned away here (library ACL + rating
	// ceiling), matching ItemHandler.Get. 404 throughout to avoid an existence
	// oracle around the gated detail endpoint.
	libID, contentRating, ok := h.items.ItemAccessInfo(r.Context(), id)
	if !ok {
		respond.NotFound(w, r)
		return
	}
	if !h.allowItem(w, r, libID, contentRating) {
		return
	}
	itemType, tmdbID, err := h.items.GetItemTypeAndTMDB(r.Context(), id)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	if tmdbID == nil || *tmdbID == 0 {
		if resolved, rerr := h.items.ResolveTMDBID(r.Context(), id); rerr == nil && resolved != nil {
			tmdbID = resolved
		}
	}
	credits, err := h.svc.GetCredits(r.Context(), id, itemType, tmdbID)
	if err != nil {
		h.logger.Error("get credits", "item_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]creditResponse, len(credits))
	for i, c := range credits {
		out[i] = creditResponse{
			Person: personSummaryResponse{
				ID:          c.Person.ID,
				TMDBID:      c.Person.TMDBID,
				Name:        c.Person.Name,
				ProfilePath: c.Person.ProfilePath,
			},
			Role:      c.Role,
			Character: c.Character,
			Job:       c.Job,
			Order:     c.Order,
		}
	}
	respond.Success(w, r, out)
}

// allowItem enforces the per-library ACL and the per-user content-rating ceiling
// for a single item, writing a 404 and returning false when the caller may not
// see it (404 not 403 so it can't be used as an existence oracle). A nil access
// checker (dev/test) skips the ACL; an empty MaxContentRating skips the ceiling.
func (h *PeopleHandler) allowItem(w http.ResponseWriter, r *http.Request, libraryID uuid.UUID, contentRating string) bool {
	claims := middleware.ClaimsFromContext(r.Context())
	if h.access != nil {
		if claims == nil {
			respond.NotFound(w, r)
			return false
		}
		ok, err := h.access.CanAccessLibrary(r.Context(), claims.UserID, libraryID, claims.IsAdmin)
		if err != nil {
			respond.InternalError(w, r)
			return false
		}
		if !ok {
			respond.NotFound(w, r)
			return false
		}
	}
	if claims != nil && !contentrating.IsAllowed(contentRating, claims.MaxContentRating) {
		respond.NotFound(w, r)
		return false
	}
	return true
}

// GetPerson handles GET /api/v1/people/{id}.
func (h *PeopleHandler) GetPerson(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid id")
		return
	}
	p, err := h.svc.GetPerson(r.Context(), id)
	if err != nil {
		if errors.Is(err, people.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.Error("get person", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, personResponse{
		ID:           p.ID,
		TMDBID:       p.TMDBID,
		Name:         p.Name,
		ProfilePath:  p.ProfilePath,
		Bio:          p.Bio,
		Birthday:     p.Birthday,
		Deathday:     p.Deathday,
		PlaceOfBirth: p.PlaceOfBirth,
	})
}

// Filmography handles GET /api/v1/people/{id}/filmography.
func (h *PeopleHandler) Filmography(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid id")
		return
	}
	entries, err := h.svc.GetFilmography(r.Context(), id)
	if err != nil {
		h.logger.Error("get filmography", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	// Filter to what the caller may see. The query joins media_items across the
	// whole library set with no per-user predicate, so without this a restricted
	// profile could enumerate titles in libraries it can't access and items above
	// its content-rating ceiling — every sibling listing injects both filters.
	claims := middleware.ClaimsFromContext(r.Context())
	maxRating := ""
	var allowed map[uuid.UUID]struct{}
	if claims != nil {
		maxRating = claims.MaxContentRating
		if h.access != nil {
			allowed, err = h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
			if err != nil {
				respond.InternalError(w, r)
				return
			}
		}
	}

	out := make([]filmographyEntryResponse, 0, len(entries))
	for _, e := range entries {
		if allowed != nil { // nil = admin / unrestricted; non-nil = explicit grant set
			if _, ok := allowed[e.LibraryID]; !ok {
				continue
			}
		}
		cr := ""
		if e.ContentRating != nil {
			cr = *e.ContentRating
		}
		if !contentrating.IsAllowed(cr, maxRating) {
			continue
		}
		out = append(out, filmographyEntryResponse{
			ItemID:     e.ItemID,
			LibraryID:  e.LibraryID,
			Title:      e.Title,
			Type:       e.Type,
			Year:       e.Year,
			PosterPath: e.PosterPath,
			Rating:     e.Rating,
			Role:       e.Role,
			Character:  e.Character,
			Job:        e.Job,
		})
	}
	respond.Success(w, r, out)
}

// Search handles GET /api/v1/people?q=foo.
func (h *PeopleHandler) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	results, err := h.svc.Search(r.Context(), q, 20)
	if err != nil {
		h.logger.Error("search people", "q", q, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]personSummaryResponse, len(results))
	for i, p := range results {
		out[i] = personSummaryResponse{
			ID:          p.ID,
			TMDBID:      p.TMDBID,
			Name:        p.Name,
			ProfilePath: p.ProfilePath,
		}
	}
	respond.Success(w, r, out)
}
