package v1

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/franchise"
	"github.com/onscreen/onscreen/internal/requests"
)

// collectionTypeFranchise is the collections.type of an auto-generated TMDB
// franchise collection (migration 00027, internal/franchise).
const collectionTypeFranchise = "franchise"

// CollectionFranchiseDB is the slice of generated queries the franchise
// additions to the collections API need. Separate from CollectionDB so
// existing fakes of that interface keep compiling; wired via WithFranchise.
type CollectionFranchiseDB interface {
	GetTMDBCollectionSnapshot(ctx context.Context, tmdbCollectionID int32) (gen.TmdbCollection, error)
	ListTMDBCollectionPosters(ctx context.Context, ids []int32) ([]gen.ListTMDBCollectionPostersRow, error)
	ListVisibleFranchiseCollectionIDs(ctx context.Context, arg gen.ListVisibleFranchiseCollectionIDsParams) ([]uuid.UUID, error)
	ListLibraryFranchiseCollections(ctx context.Context, arg gen.ListLibraryFranchiseCollectionsParams) ([]gen.ListLibraryFranchiseCollectionsRow, error)
	ListMediaItemsByTMDBIDs(ctx context.Context, arg gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error)
}

// tmdbItemLister resolves TMDB ids to the caller-visible media items
// (library grant + rating ceiling applied in SQL).
type tmdbItemLister interface {
	ListMediaItemsByTMDBIDs(ctx context.Context, arg gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error)
}

// FranchiseRequestLookup finds the caller's open requests for a set of TMDB
// ids (pending/approved/downloading). Satisfied by *requests.Service.
type FranchiseRequestLookup interface {
	FindActiveForUserBatch(ctx context.Context, userID uuid.UUID, tmdbIDs []int) ([]gen.MediaRequest, error)
}

// WithFranchise wires the franchise-collection additions: parts on the
// collection detail, TMDB posters and visibility filtering on the listing,
// and the per-library listing. reqs may be nil (parts then carry no
// request_status).
func (h *CollectionHandler) WithFranchise(db CollectionFranchiseDB, reqs FranchiseRequestLookup) *CollectionHandler {
	h.franchise = db
	h.franchiseReqs = reqs
	return h
}

// franchisePartResponse is one film of a franchise collection as the caller
// sees it. ItemID is set when the film is in the server AND visible to the
// caller (library grant + rating ceiling); otherwise it is a missing part,
// which is only listed for callers without a rating ceiling (see
// franchiseParts). RequestStatus is the caller's own open request for the
// film, if any.
//
// ItemIDs lists every visible copy of an owned film (ItemID first) — the same
// film can sit in several libraries (4K + 1080p) and the collection's items
// listing returns each copy, so clients use it to fold the extra copies into
// this part rather than showing them as further films.
type franchisePartResponse struct {
	TMDBID        int      `json:"tmdb_id"`
	Title         string   `json:"title"`
	Year          int      `json:"year,omitempty"`
	ReleaseDate   string   `json:"release_date,omitempty"`
	PosterURL     string   `json:"poster_url,omitempty"`
	Overview      string   `json:"overview,omitempty"`
	ItemID        *string  `json:"item_id"`
	ItemIDs       []string `json:"item_ids,omitempty"`
	RequestStatus *string  `json:"request_status"`
}

// viewerScope is the caller's visibility: allowedLibIDs nil = every library
// (admin / no ACL wired); maxRank nil = no rating ceiling.
type viewerScope struct {
	claims        *auth.Claims
	allowedLibIDs []uuid.UUID
	maxRank       *int32
}

func (v viewerScope) restricted() bool { return v.allowedLibIDs != nil || v.maxRank != nil }

// missingPartsVisible reports whether the caller may see parts that aren't
// in the server. TMDB collection parts carry no certification, so they rank
// as unrated — the most restrictive rank — and only a profile without a
// content-rating ceiling sees them.
func (v viewerScope) missingPartsVisible() bool {
	return v.claims != nil && contentrating.IsAllowed("", v.claims.MaxContentRating)
}

// scopeFor resolves the caller's visibility. ok=false means a response was
// already written.
func scopeFor(w http.ResponseWriter, r *http.Request, access LibraryAccessChecker, logWhat string, logf func(string, ...any)) (viewerScope, bool) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return viewerScope{}, false
	}
	v := viewerScope{claims: claims, maxRank: maxRatingRankFromClaims(claims.MaxContentRating)}
	if access != nil {
		allowed, err := access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
		if err != nil {
			logf(logWhat, "err", err)
			respond.InternalError(w, r)
			return viewerScope{}, false
		}
		if allowed != nil {
			v.allowedLibIDs = make([]uuid.UUID, 0, len(allowed))
			for id := range allowed {
				v.allowedLibIDs = append(v.allowedLibIDs, id)
			}
		}
	}
	return v, true
}

// franchiseParts resolves a stored parts list against what the caller can
// see. owned counts the parts that resolved to a visible item.
func franchiseParts(ctx context.Context, db tmdbItemLister, reqs FranchiseRequestLookup, v viewerScope, parts []franchise.Part) (out []franchisePartResponse, owned int, err error) {
	out = make([]franchisePartResponse, 0, len(parts))
	if len(parts) == 0 {
		return out, 0, nil
	}
	ids := make([]int32, 0, len(parts))
	for _, p := range parts {
		ids = append(ids, int32(p.TMDBID))
	}
	rows, err := db.ListMediaItemsByTMDBIDs(ctx, gen.ListMediaItemsByTMDBIDsParams{
		Type:          "movie",
		TmdbIds:       ids,
		LibraryIds:    v.allowedLibIDs,
		MaxRatingRank: v.maxRank,
	})
	if err != nil {
		return nil, 0, err
	}
	// The same film can sit in several libraries (4K + 1080p); the part
	// links the lowest id so the link is stable across reloads, and lists
	// every copy in ItemIDs.
	copies := make(map[int32][]string, len(rows))
	for _, row := range rows {
		if row.TmdbID == nil {
			continue
		}
		copies[*row.TmdbID] = append(copies[*row.TmdbID], row.ID.String())
	}
	for _, ids := range copies {
		sort.Strings(ids)
	}

	showMissing := v.missingPartsVisible()
	statuses := map[int]string{}
	if showMissing && reqs != nil {
		missing := make([]int, 0, len(parts))
		for _, p := range parts {
			if _, ok := copies[int32(p.TMDBID)]; !ok {
				missing = append(missing, p.TMDBID)
			}
		}
		if len(missing) > 0 {
			// Best-effort garnish: a lookup failure just leaves the
			// Request buttons without a status.
			if active, lerr := reqs.FindActiveForUserBatch(ctx, v.claims.UserID, missing); lerr == nil {
				for _, req := range active {
					if req.Type == requests.TypeMovie {
						statuses[int(req.TmdbID)] = req.Status
					}
				}
			}
		}
	}

	for _, p := range parts {
		pr := franchisePartResponse{
			TMDBID:      p.TMDBID,
			Title:       p.Title,
			Year:        p.Year,
			ReleaseDate: p.ReleaseDate,
			PosterURL:   p.PosterURL,
			Overview:    p.Overview,
		}
		if ids, ok := copies[int32(p.TMDBID)]; ok {
			s := ids[0]
			pr.ItemID = &s
			pr.ItemIDs = ids
			owned++
		} else if !showMissing {
			continue
		} else if st, ok := statuses[p.TMDBID]; ok {
			pr.RequestStatus = &st
		}
		out = append(out, pr)
	}
	return out, owned, nil
}

// franchiseVisible reports whether the caller can see at least one item of
// a franchise collection. Unrestricted callers always can.
func (h *CollectionHandler) franchiseVisible(ctx context.Context, v viewerScope, colID uuid.UUID) (bool, error) {
	if !v.restricted() {
		return true, nil
	}
	set, err := h.visibleFranchiseSet(ctx, v)
	if err != nil {
		return false, err
	}
	_, ok := set[colID]
	return ok, nil
}

func (h *CollectionHandler) visibleFranchiseSet(ctx context.Context, v viewerScope) (map[uuid.UUID]struct{}, error) {
	ids, err := h.franchise.ListVisibleFranchiseCollectionIDs(ctx, gen.ListVisibleFranchiseCollectionIDsParams{
		LibraryIds:    v.allowedLibIDs,
		MaxRatingRank: v.maxRank,
	})
	if err != nil {
		return nil, err
	}
	set := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set, nil
}

// decorateFranchiseList drops franchise collections the caller can't see
// into (no visible member — their names would otherwise leak titles from
// libraries without a grant, or above the rating ceiling) and attaches TMDB
// collection posters.
func (h *CollectionHandler) decorateFranchiseList(w http.ResponseWriter, r *http.Request, out []collectionResponse) ([]collectionResponse, bool) {
	hasFranchise := false
	for _, c := range out {
		if c.Type == collectionTypeFranchise {
			hasFranchise = true
			break
		}
	}
	if !hasFranchise {
		return out, true
	}
	if h.franchise == nil {
		// Not wired: fail closed — without the visibility query there is no
		// way to tell which franchise names this caller may see.
		kept := out[:0]
		for _, c := range out {
			if c.Type != collectionTypeFranchise {
				kept = append(kept, c)
			}
		}
		return kept, true
	}
	v, ok := scopeFor(w, r, h.access, "collections: allowed libraries", func(msg string, args ...any) {
		h.logger.ErrorContext(r.Context(), msg, args...)
	})
	if !ok {
		return nil, false
	}
	var visible map[uuid.UUID]struct{}
	if v.restricted() {
		set, err := h.visibleFranchiseSet(r.Context(), v)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "collections: visible franchises", "err", err)
			respond.InternalError(w, r)
			return nil, false
		}
		visible = set
	}
	kept := out[:0]
	var tmdbIDs []int32
	for _, c := range out {
		if c.Type == collectionTypeFranchise {
			if visible != nil {
				id, _ := uuid.Parse(c.ID)
				if _, ok := visible[id]; !ok {
					continue
				}
			}
			if c.TMDBCollectionID != nil {
				tmdbIDs = append(tmdbIDs, *c.TMDBCollectionID)
			}
		}
		kept = append(kept, c)
	}
	if len(tmdbIDs) > 0 {
		if rows, err := h.franchise.ListTMDBCollectionPosters(r.Context(), tmdbIDs); err == nil {
			posters := make(map[int32]*string, len(rows))
			for _, row := range rows {
				posters[row.TmdbCollectionID] = row.PosterUrl
			}
			for i := range kept {
				if kept[i].TMDBCollectionID != nil {
					kept[i].PosterURL = posters[*kept[i].TMDBCollectionID]
				}
			}
		}
	}
	return kept, true
}

// franchiseDetail fills the franchise-specific detail fields (art, overview,
// parts) for Get. ok=false means a response was already written.
func (h *CollectionHandler) franchiseDetail(w http.ResponseWriter, r *http.Request, col gen.Collection, resp *collectionResponse) bool {
	if h.franchise == nil {
		return true
	}
	v, ok := scopeFor(w, r, h.access, "collections: allowed libraries", func(msg string, args ...any) {
		h.logger.ErrorContext(r.Context(), msg, args...)
	})
	if !ok {
		return false
	}
	visible, err := h.franchiseVisible(r.Context(), v, col.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "collections: franchise visibility", "id", col.ID, "err", err)
		respond.InternalError(w, r)
		return false
	}
	if !visible {
		// 404, not 403: the collection's existence (and name) is itself
		// the thing a restricted caller must not learn.
		respond.NotFound(w, r)
		return false
	}
	resp.Parts = []franchisePartResponse{}
	if col.TmdbCollectionID == nil {
		return true
	}
	snap, err := h.franchise.GetTMDBCollectionSnapshot(r.Context(), *col.TmdbCollectionID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			h.logger.WarnContext(r.Context(), "collections: franchise snapshot", "id", col.ID, "err", err)
		}
		return true // no snapshot yet: the client falls back to the items list
	}
	resp.PosterURL = snap.PosterUrl
	resp.BackdropURL = snap.BackdropUrl
	parts, _, err := franchiseParts(r.Context(), h.franchise, h.franchiseReqs, v, franchise.DecodeParts(snap.Parts))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "collections: franchise parts", "id", col.ID, "err", err)
		respond.InternalError(w, r)
		return false
	}
	resp.Parts = parts
	return true
}

// libraryCollectionResponse is one card on a library's Collections tab.
type libraryCollectionResponse struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Type             string  `json:"type"`
	TMDBCollectionID *int32  `json:"tmdb_collection_id,omitempty"`
	PosterURL        *string `json:"poster_url,omitempty"`
	PosterPath       *string `json:"poster_path,omitempty"`
	ItemCount        int64   `json:"item_count"`
}

// LibraryCollections handles GET /api/v1/libraries/{id}/collections: the
// franchise and manual collections with at least one item in this library
// that the caller can see, with that visible count, by name. 404 when the
// caller has no grant on the library.
func (h *CollectionHandler) LibraryCollections(w http.ResponseWriter, r *http.Request) {
	libID, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	if h.access != nil {
		ok, aerr := h.access.CanAccessLibrary(r.Context(), claims.UserID, libID, claims.IsAdmin)
		if aerr != nil {
			h.logger.ErrorContext(r.Context(), "library collections: access", "library_id", libID, "err", aerr)
			respond.InternalError(w, r)
			return
		}
		if !ok {
			respond.NotFound(w, r)
			return
		}
	}
	out := []libraryCollectionResponse{}
	maxRank := maxRatingRankFromClaims(claims.MaxContentRating)
	manual, err := h.libraryManualCollections(r.Context(), libID, maxRank)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "library manual collections", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}
	out = append(out, manual...)
	if h.franchise == nil {
		sortLibraryCollections(out)
		respond.Success(w, r, out)
		return
	}
	rows, err := h.franchise.ListLibraryFranchiseCollections(r.Context(), gen.ListLibraryFranchiseCollectionsParams{
		LibraryID:     libID,
		MaxRatingRank: maxRank,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "library collections", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}
	for _, row := range rows {
		c := libraryCollectionResponse{
			ID:               row.ID.String(),
			Name:             row.Name,
			Type:             row.Type,
			TMDBCollectionID: row.TmdbCollectionID,
			PosterURL:        row.PosterUrl,
			ItemCount:        row.ItemCount,
		}
		if row.FirstPosterPath != "" {
			p := row.FirstPosterPath
			c.PosterPath = &p
		}
		out = append(out, c)
	}
	sortLibraryCollections(out)
	respond.Success(w, r, out)
}

// rejectManagedMutation answers 409 for writes to a franchise collection:
// its name and membership are rebuilt from TMDB on every sync, so an edit
// would silently revert. Returns true when it wrote the response.
func rejectManagedMutation(w http.ResponseWriter, r *http.Request, col gen.Collection) bool {
	if col.Type != collectionTypeFranchise {
		return false
	}
	respond.Error(w, r, http.StatusConflict, "MANAGED_COLLECTION",
		"franchise collections are maintained automatically from TMDB and can't be edited")
	return true
}
