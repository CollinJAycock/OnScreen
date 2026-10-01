package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/auth"
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
	FindActiveForUserBatch(ctx context.Context, userID uuid.UUID, tmdbIDs []int) ([]gen.MediaRequest, error)
}

// DiscoverRequestGate says whether a user may request at all — admins
// always, everyone else per users.can_request. Satisfied by
// *requests.Service, which reads the users row rather than the token
// claims (an admin's change takes effect before the token is reissued).
type DiscoverRequestGate interface {
	CanRequest(ctx context.Context, userID uuid.UUID) (bool, error)
}

// ErrDiscoverTMDBUnavailable is what a DiscoverTMDB / DiscoverSeasonTMDB
// returns (wrapped) when no TMDB agent is configured. Both Discover
// endpoints answer it with the feature-off 503 rather than a 500.
var ErrDiscoverTMDBUnavailable = errors.New("discover: tmdb is not configured")

// DiscoverHandler powers the Request UI's discover surface: a single TMDB
// search-multi call enriched with library and request state so the user
// sees "in your library", "you already requested this", or "request now"
// without an extra round-trip per result.
type DiscoverHandler struct {
	db       DiscoverDB
	tmdb     DiscoverTMDB
	requests DiscoverRequestLookup
	logger   *slog.Logger
	// access scopes the in-library lookup to what the caller may actually
	// see. nil = no ACL wired (tests / minimal deployments) → no filtering.
	access LibraryAccessChecker
	// gate refuses the TMDB catalogue to users who may not request.
	// nil = not wired (tests) → every caller may search.
	gate DiscoverRequestGate
	// seasons / seasonDB back season-level requests (discover_seasons.go):
	// the per-season endpoint and the missing_seasons decoration. nil = not
	// wired; the endpoint then answers 503 and results carry no season info.
	seasons  DiscoverSeasonTMDB
	seasonDB DiscoverSeasonDB
}

// NewDiscoverHandler builds a handler. tmdb may be nil — the endpoint will
// then return a clean 503 instead of crashing, so the UI can show the
// "configure TMDB to enable Discover" copy.
func NewDiscoverHandler(db DiscoverDB, t DiscoverTMDB, reqs DiscoverRequestLookup, logger *slog.Logger) *DiscoverHandler {
	return &DiscoverHandler{db: db, tmdb: t, requests: reqs, logger: logger}
}

// WithAccess wires the library ACL so `in_library` reflects the caller's own
// view. Without it the flag answered for the whole catalogue, which made it a
// one-bit existence oracle for titles in libraries the caller has no grant on
// and titles above their content-rating ceiling — something no other endpoint
// will tell them. Returns the handler for chaining.
func (h *DiscoverHandler) WithAccess(a LibraryAccessChecker) *DiscoverHandler {
	h.access = a
	return h
}

// WithRequestGate wires the can-request check. Discover exists to pick
// something to request, so an account an admin has switched requesting off
// for gets 403 REQUESTS_DISABLED (the same answer POST /requests gives)
// instead of a TMDB catalogue it can't act on. Returns the handler for
// chaining.
func (h *DiscoverHandler) WithRequestGate(g DiscoverRequestGate) *DiscoverHandler {
	h.gate = g
	return h
}

// writeTMDBUnavailable is the feature-off answer: requests (and with them
// Discover) are off on a server without a TMDB key. A 503 with a code, so a
// client can tell it from a failure and show "not configured" copy.
func writeTMDBUnavailable(w http.ResponseWriter, r *http.Request, msg string) {
	respond.Error(w, r, http.StatusServiceUnavailable, "TMDB_UNAVAILABLE", msg)
}

// requestsAllowed answers 403 REQUESTS_DISABLED and returns false when the
// caller may not request. Admins skip the lookup. A failed lookup fails
// closed (500): the catalogue is only for accounts known to be allowed.
func (h *DiscoverHandler) requestsAllowed(w http.ResponseWriter, r *http.Request, claims *auth.Claims) bool {
	if h.gate == nil || claims.IsAdmin {
		return true
	}
	ok, err := h.gate.CanRequest(r.Context(), claims.UserID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "discover: can-request lookup", "err", err)
		respond.InternalError(w, r)
		return false
	}
	if !ok {
		respond.Error(w, r, http.StatusForbidden, "REQUESTS_DISABLED",
			"requesting is turned off for your account; ask an admin")
		return false
	}
	return true
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
	// MissingSeasons (shows in the caller's library only) lists the aired
	// regular seasons the library doesn't fully hold — what "Request more
	// seasons" would offer. [] when nothing is missing; absent for shows not
	// in the library, movies, and when the season list couldn't be read.
	MissingSeasons *[]int `json:"missing_seasons,omitempty"`
	// PartiallyAvailable: the show is in the caller's library but some aired
	// seasons are missing (in_library keeps its old meaning for older clients).
	PartiallyAvailable bool `json:"partially_available"`
	// Request state from the perspective of the requesting user.
	HasActiveRequest bool       `json:"has_active_request"`
	ActiveRequestID  *uuid.UUID `json:"active_request_id,omitempty"`
	ActiveStatus     *string    `json:"active_request_status,omitempty"`
}

// Search handles GET /api/v1/discover/search?q=...&limit=20.
//
// Returns up to `limit` results (capped at 50) from TMDB's /search/multi,
// each marked with whether it's already in the library and whether the
// caller already has an open request for it. 503 TMDB_UNAVAILABLE when no
// TMDB key is configured (the requests feature is off); 403
// REQUESTS_DISABLED for an account that may not request.
func (h *DiscoverHandler) Search(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	const unavailable = "TMDB API key is not configured — Discover requires TMDB to enrich titles"
	if h.tmdb == nil {
		writeTMDBUnavailable(w, r, unavailable)
		return
	}
	if !h.requestsAllowed(w, r, claims) {
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
		if errors.Is(err, ErrDiscoverTMDBUnavailable) {
			writeTMDBUnavailable(w, r, unavailable)
			return
		}
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

	// Batch the active-request lookup: one query for all results instead of one
	// per row. Keyed by (type, tmdb_id) since an id can collide across movie/show.
	var activeReqs map[string]gen.MediaRequest
	if h.requests != nil {
		tmdbIDs := make([]int, len(out))
		for i := range out {
			tmdbIDs[i] = out[i].TMDBID
		}
		if reqs, lerr := h.requests.FindActiveForUserBatch(r.Context(), claims.UserID, tmdbIDs); lerr != nil {
			h.logger.WarnContext(r.Context(), "discover: batch active requests", "err", lerr)
		} else {
			activeReqs = make(map[string]gen.MediaRequest, len(reqs))
			for _, req := range reqs {
				activeReqs[req.Type+":"+strconv.Itoa(int(req.TmdbID))] = req
			}
		}
	}

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

		if req, ok := activeReqs[out[i].Type+":"+strconv.Itoa(out[i].TMDBID)]; ok {
			out[i].HasActiveRequest = true
			id := req.ID
			out[i].ActiveRequestID = &id
			status := req.Status
			out[i].ActiveStatus = &status
		}
	}

	// Shows already in the library: which aired seasons are still missing.
	h.decorateMissingSeasons(r.Context(), out)

	respond.Success(w, r, out)
}

// callerScope is the caller's own view of the library: their granted
// libraries (nil = all, for admins / no ACL wired) and their content-rating
// ceiling (nil = none). ok=false when the grants couldn't be read — callers
// then fail CLOSED and report nothing as in the library rather than answer
// across the whole catalogue.
func (h *DiscoverHandler) callerScope(ctx context.Context) (libIDs []uuid.UUID, maxRank *int32, ok bool) {
	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, nil, true
	}
	maxRank = maxRatingRankFromClaims(claims.MaxContentRating)
	if h.access == nil {
		return nil, maxRank, true
	}
	allowed, err := h.access.AllowedLibraryIDs(ctx, claims.UserID, claims.IsAdmin)
	if err != nil {
		h.logger.WarnContext(ctx, "discover: allowed libraries failed; suppressing in-library flags",
			"err", err)
		return nil, nil, false
	}
	if allowed != nil {
		libIDs = make([]uuid.UUID, 0, len(allowed))
		for id := range allowed {
			libIDs = append(libIDs, id)
		}
	}
	return libIDs, maxRank, true
}

func (h *DiscoverHandler) lookupLibrary(ctx context.Context, mediaType string, ids []int32) map[int32]uuid.UUID {
	if len(ids) == 0 {
		return nil
	}
	// Scope to the caller's own view: their granted libraries and their
	// content-rating ceiling. A hit they cannot see must report as "not in
	// library" with no id, exactly as it would from every other endpoint.
	libIDs, maxRank, ok := h.callerScope(ctx)
	if !ok {
		return nil
	}
	rows, err := h.db.ListMediaItemsByTMDBIDs(ctx, gen.ListMediaItemsByTMDBIDsParams{
		Type:          libraryItemType(mediaType),
		TmdbIds:       ids,
		LibraryIds:    libIDs,
		MaxRatingRank: maxRank,
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

// Compile-time guard: requests.Service satisfies DiscoverRequestLookup and
// DiscoverRequestGate so the wiring in main.go can pass *requests.Service
// directly.
var (
	_ DiscoverRequestLookup = (*requests.Service)(nil)
	_ DiscoverRequestGate   = (*requests.Service)(nil)
)
