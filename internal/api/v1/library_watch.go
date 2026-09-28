package v1

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/dbconv"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// LibraryWatchDB is the optional watch-state store behind the library grid:
// one batched rollup query per page and the "Surprise me" random pick.
// *gen.Queries satisfies it.
type LibraryWatchDB interface {
	ListItemWatchSummaries(ctx context.Context, arg gen.ListItemWatchSummariesParams) ([]gen.ListItemWatchSummariesRow, error)
	PickRandomLibraryItem(ctx context.Context, arg gen.PickRandomLibraryItemParams) (gen.PickRandomLibraryItemRow, error)
}

// WithWatchState wires the per-user watch fields on GET /libraries/:id/items
// and enables GET /libraries/:id/random. Without it the listing omits the
// watch fields (the ?watch= filter still works — it runs inside the listing
// queries) and /random returns 501.
func (h *LibraryHandler) WithWatchState(db LibraryWatchDB) *LibraryHandler {
	h.watchDB = db
	return h
}

// Watch filter values accepted by ?watch= on the library listing and /random.
//
// Playable leaves (movie, episode, music video, home video) are in exactly
// one bucket, from the caller's watch state:
//
//	watched      finished (a play past 90%, or marked played) since the
//	             latest unplayed mark — sticky across a rewatch
//	in_progress  started, not finished
//	unwatched    never started, or marked unplayed since
//
// Shows and seasons roll up the episodes within the caller's rating ceiling:
//
//	watched      at least one episode, and every one watched
//	in_progress  some episode watched or started, but not all watched
//	unwatched    no episode watched or started (including no episodes)
//
// Other item types (tracks, photos, …) have no watch state and are always
// "unwatched". The three buckets partition the listing.
const (
	watchFilterUnwatched  = "unwatched"
	watchFilterInProgress = "in_progress"
	watchFilterWatched    = "watched"
)

const watchFilterError = "watch must be one of unwatched, in_progress, watched"

// parseWatchFilter validates a ?watch= value. "" means no filter; ok is false
// for anything that is not a known bucket.
func parseWatchFilter(v string) (watch string, ok bool) {
	switch v {
	case "", watchFilterUnwatched, watchFilterInProgress, watchFilterWatched:
		return v, true
	}
	return "", false
}

// isWatchLeafType reports whether items of this type carry their own watch
// state (a playable video). Kept in step with the leaf types
// MarkWatchStateForTarget accepts.
func isWatchLeafType(t string) bool {
	switch t {
	case "movie", "episode", "music_video", "home_video":
		return true
	}
	return false
}

// isWatchContainerType reports whether an item's watch state is the rollup
// of its episodes.
func isWatchContainerType(t string) bool {
	return t == "show" || t == "season"
}

// parseItemFilterParams reads the browse filters shared by the library
// listing and /random (genre, year range, minimum rating). Unparseable
// numbers are ignored, as they always have been on the listing. Sort
// defaults to title ascending; the listing layers its sort params on top.
func parseItemFilterParams(q url.Values) media.FilterParams {
	fp := media.FilterParams{
		Sort:    "title",
		SortAsc: true,
	}
	if g := q.Get("genre"); g != "" {
		fp.Genre = &g
	}
	if v, err := strconv.Atoi(q.Get("year_min")); err == nil {
		fp.YearMin = &v
	}
	if v, err := strconv.Atoi(q.Get("year_max")); err == nil {
		fp.YearMax = &v
	}
	if v, err := strconv.ParseFloat(q.Get("rating_min"), 64); err == nil {
		fp.RatingMin = &v
	}
	return fp
}

// attachWatchState fills the caller's watch fields on one listing page with a
// single batched query (no per-item round trips). Failures only drop the
// fields — the listing itself already succeeded.
func (h *LibraryHandler) attachWatchState(ctx context.Context, userID uuid.UUID, maxRank *int, items []media.Item, out []MediaItemResponse) {
	if h.watchDB == nil || len(items) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		if isWatchLeafType(it.Type) || isWatchContainerType(it.Type) {
			ids = append(ids, it.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := h.watchDB.ListItemWatchSummaries(ctx, gen.ListItemWatchSummariesParams{
		UserID:        userID,
		MaxRatingRank: dbconv.IntPtrToInt32Ptr(maxRank),
		MediaIds:      ids,
	})
	if err != nil {
		h.logger.WarnContext(ctx, "library items: watch summaries", "err", err)
		return
	}
	byID := make(map[uuid.UUID]gen.ListItemWatchSummariesRow, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	for i := range out {
		row, ok := byID[items[i].ID]
		if !ok {
			continue
		}
		applyWatchSummary(&out[i], items[i].Type, row)
	}
}

// applyWatchSummary maps one ListItemWatchSummaries row onto a listing entry.
func applyWatchSummary(dst *MediaItemResponse, itemType string, row gen.ListItemWatchSummariesRow) {
	switch {
	case isWatchLeafType(itemType):
		st := row.Status
		dst.WatchState = &st
		if row.Resumable && row.PositionMs > 0 {
			off := row.PositionMs
			dst.ViewOffsetMS = &off
		}
	case isWatchContainerType(itemType):
		leaves := row.LeafCount
		unwatched := row.LeafCount - row.WatchedCount
		if unwatched < 0 {
			unwatched = 0
		}
		dst.LeafCount = &leaves
		dst.UnwatchedCount = &unwatched
	}
}

// RandomItemResponse is the body of GET /libraries/:id/random.
type RandomItemResponse struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// Random handles GET /api/v1/libraries/{id}/random — one random item the
// caller can see, under the same filters as the listing (genre, year_min,
// year_max, rating_min, type, watch; sort params are ignored). 404 when the
// library is not visible or nothing matches.
func (h *LibraryHandler) Random(w http.ResponseWriter, r *http.Request) {
	if h.watchDB == nil {
		respond.Error(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", "random pick not available")
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	ok, err := h.svc.CanAccessLibrary(r.Context(), claims.UserID, id, claims.IsAdmin)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "check library access", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !ok {
		respond.NotFound(w, r)
		return
	}
	lib, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		respond.InternalError(w, r)
		return
	}

	q := r.URL.Query()
	fp := parseItemFilterParams(q)
	watch, ok := parseWatchFilter(q.Get("watch"))
	if !ok {
		respond.BadRequest(w, r, watchFilterError)
		return
	}
	itemType := rootItemType(lib.Type)
	if t := q.Get("type"); t != "" {
		if !validItemTypeForLibrary(lib.Type, t) {
			respond.BadRequest(w, r, "type "+t+" is not valid for "+lib.Type+" library")
			return
		}
		itemType = t
	}

	row, err := h.watchDB.PickRandomLibraryItem(r.Context(), gen.PickRandomLibraryItemParams{
		LibraryID:     id,
		ItemType:      itemType,
		Genre:         fp.Genre,
		YearMin:       dbconv.IntPtrToInt32Ptr(fp.YearMin),
		YearMax:       dbconv.IntPtrToInt32Ptr(fp.YearMax),
		RatingMin:     dbconv.Float64PtrToNumeric(fp.RatingMin),
		MaxRatingRank: dbconv.IntPtrToInt32Ptr(contentrating.MaxRatingRank(claims.MaxContentRating)),
		Watch:         nonEmptyStringPtr(watch),
		WatchUserID:   claims.UserID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "random library item", "library_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, RandomItemResponse{ID: row.ID.String(), Type: row.Type})
}

// nonEmptyStringPtr maps "" to a NULL narg.
func nonEmptyStringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
