package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/observability"
)

// WatchStateDB is the store behind the manual watch-state endpoints.
// *gen.Queries satisfies it; production wires the primary pool so an Up Next
// read right after a mark sees it.
type WatchStateDB interface {
	GetMediaItem(ctx context.Context, id uuid.UUID) (gen.GetMediaItemRow, error)
	MarkWatchStateForTarget(ctx context.Context, arg gen.MarkWatchStateForTargetParams) ([]uuid.UUID, error)
	DismissContinueWatching(ctx context.Context, arg gen.DismissContinueWatchingParams) (int64, error)
	ListUpNextEpisodes(ctx context.Context, arg gen.ListUpNextEpisodesParams) ([]gen.ListUpNextEpisodesRow, error)
}

// WatchStateHandler serves the caller's manual watch-state controls:
//
//	POST   /items/{id}/watched                   mark played
//	DELETE /items/{id}/watched                   mark unplayed
//	POST   /items/{id}/dismiss-continue-watching hide from Continue Watching
//	GET    /items/{id}/up-next                   what Play on a show/season starts
//
// Always scoped to the authenticated user. Every endpoint first applies the
// same item gate as lists and favorites (library ACL + content-rating
// ceiling, 404 when either fails), and show/season expansion only ever
// touches episodes within the ceiling.
type WatchStateHandler struct {
	db        WatchStateDB
	access    LibraryAccessChecker
	logger    *slog.Logger
	onWatched WatchedHook
}

// WatchedHook hears which items a "mark watched" changed (the external
// scrobble dispatcher, which adds them to the user's Trakt history). It is
// called async, after the response, with a context that outlives the request.
type WatchedHook func(ctx context.Context, userID uuid.UUID, mediaIDs []uuid.UUID)

// NewWatchStateHandler constructs the handler. access may be nil in dev
// setups (fails open, like every other handler's ACL hook).
func NewWatchStateHandler(db WatchStateDB, access LibraryAccessChecker, logger *slog.Logger) *WatchStateHandler {
	return &WatchStateHandler{db: db, access: access, logger: logger}
}

// WithWatchedHook attaches fn to every "mark watched" that changes
// something. nil is a no-op.
func (h *WatchStateHandler) WithWatchedHook(fn WatchedHook) *WatchStateHandler {
	h.onWatched = fn
	return h
}

// LibraryAccessWired reports whether the library-ACL checker is wired.
func (h *WatchStateHandler) LibraryAccessWired() bool { return h.access != nil }

// Mark states stored in watch_progress.mark_state.
const (
	markWatched   = "watched"
	markUnwatched = "unwatched"
)

// MarkWatched handles POST /api/v1/items/{id}/watched.
func (h *WatchStateHandler) MarkWatched(w http.ResponseWriter, r *http.Request) {
	h.mark(w, r, markWatched)
}

// MarkUnwatched handles DELETE /api/v1/items/{id}/watched.
func (h *WatchStateHandler) MarkUnwatched(w http.ResponseWriter, r *http.Request) {
	h.mark(w, r, markUnwatched)
}

// mark applies a played/unplayed mark. A movie, episode, music video or home
// video is marked directly; a season marks every episode in it and a show
// every episode under it (only those within the caller's rating ceiling), in
// one statement. Other types have no watch state: 422.
func (h *WatchStateHandler) mark(w http.ResponseWriter, r *http.Request, state string) {
	item, ok := h.loadVisibleItem(w, r)
	if !ok {
		return
	}
	if !isWatchLeafType(item.Type) && !isWatchContainerType(item.Type) {
		respond.ValidationError(w, r, "only videos, seasons and shows can be marked watched or unwatched")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	marked, err := h.db.MarkWatchStateForTarget(r.Context(), gen.MarkWatchStateForTargetParams{
		UserID:        claims.UserID,
		State:         state,
		TargetType:    item.Type,
		TargetID:      item.ID,
		MaxRatingRank: maxRatingRankFromClaims(claims.MaxContentRating),
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "mark watch state",
			"user_id", claims.UserID, "item_id", item.ID, "state", state, "err", err)
		respond.InternalError(w, r)
		return
	}
	if state == markWatched && len(marked) > 0 && h.onWatched != nil {
		ctx, userID := context.WithoutCancel(r.Context()), claims.UserID
		observability.SafeGo(h.logger, "watchstate.scrobble", func() {
			h.onWatched(ctx, userID, marked)
		})
	}
	respond.NoContent(w)
}

// DismissContinueWatching handles POST /api/v1/items/{id}/dismiss-continue-watching.
// The item leaves every Continue Watching row (the legacy combined one and
// the split TV / movie rows) and Next Up until the user next records watch
// activity on it. An episode or season dismisses its show's tile — Continue
// Watching TV shows one tile per show.
func (h *WatchStateHandler) DismissContinueWatching(w http.ResponseWriter, r *http.Request) {
	item, ok := h.loadVisibleItem(w, r)
	if !ok {
		return
	}
	if !isWatchLeafType(item.Type) && !isWatchContainerType(item.Type) {
		respond.ValidationError(w, r, "only videos, seasons and shows appear in Continue Watching")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	n, err := h.db.DismissContinueWatching(r.Context(), gen.DismissContinueWatchingParams{
		UserID:  claims.UserID,
		MediaID: item.ID,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "dismiss continue watching",
			"user_id", claims.UserID, "item_id", item.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if n == 0 {
		respond.NotFound(w, r)
		return
	}
	respond.NoContent(w)
}

// UpNextEpisode is the episode an UpNextResponse points at.
type UpNextEpisode struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	SeasonID      string  `json:"season_id"`
	SeasonNumber  int     `json:"season_number"`
	EpisodeNumber int     `json:"episode_number"`
	ViewOffsetMS  *int64  `json:"view_offset_ms,omitempty"`
	DurationMS    *int64  `json:"duration_ms,omitempty"`
	ThumbPath     *string `json:"thumb_path,omitempty"`
}

// UpNextResponse is the body of GET /items/{id}/up-next. Mode is one of
// resume / next / start / rewatch / none (see pickUpNext); Episode is
// omitted only for none.
type UpNextResponse struct {
	Mode    string         `json:"mode"`
	Episode *UpNextEpisode `json:"episode,omitempty"`
}

// Up Next modes.
const (
	upNextResume  = "resume"
	upNextNext    = "next"
	upNextStart   = "start"
	upNextRewatch = "rewatch"
	upNextNone    = "none"
)

// UpNext handles GET /api/v1/items/{id}/up-next for a show or a season (any
// other type: 422). Only episodes within the caller's rating ceiling are
// considered; for a season, only that season's.
func (h *WatchStateHandler) UpNext(w http.ResponseWriter, r *http.Request) {
	item, ok := h.loadVisibleItem(w, r)
	if !ok {
		return
	}
	if !isWatchContainerType(item.Type) {
		respond.ValidationError(w, r, "up-next is only available for shows and seasons")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	rows, err := h.db.ListUpNextEpisodes(r.Context(), gen.ListUpNextEpisodesParams{
		UserID:        claims.UserID,
		ContainerID:   item.ID,
		MaxRatingRank: maxRatingRankFromClaims(claims.MaxContentRating),
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "up next episodes",
			"user_id", claims.UserID, "item_id", item.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	mode, idx := pickUpNext(rows, item.Type == "show")
	out := UpNextResponse{Mode: mode}
	if idx >= 0 {
		ep := rows[idx]
		e := &UpNextEpisode{
			ID:            ep.ID.String(),
			Title:         ep.Title,
			SeasonID:      ep.SeasonID.String(),
			SeasonNumber:  int(derefInt32(ep.SeasonNumber)),
			EpisodeNumber: int(derefInt32(ep.EpisodeNumber)),
			DurationMS:    ep.DurationMs,
			ThumbPath:     ep.ThumbPath,
		}
		if mode == upNextResume {
			off := ep.PositionMs
			e.ViewOffsetMS = &off
		}
		out.Episode = e
	}
	respond.Success(w, r, out)
}

// pickUpNext decides what Play on a show or season starts. rows are the
// episodes in play order (season, then episode number). For a show, season 0
// specials are ignored unless the show has nothing else. Returns the mode and
// the index into rows of the chosen episode (-1 for none):
//
//	resume  — an episode has a resume point: the most recently touched one
//	next    — the first not-watched episode after the anchor, the most
//	          recently watched episode (ties go to the later episode). With
//	          nothing unwatched after the anchor but gaps before it, the
//	          first not-watched episode overall.
//	start   — nothing watched yet: the first episode
//	rewatch — everything watched: the first episode
//	none    — no episodes
//
// The hub's ListNextUp applies the same anchor rule in SQL.
func pickUpNext(rows []gen.ListUpNextEpisodesRow, showScope bool) (string, int) {
	cand := make([]int, 0, len(rows))
	if showScope {
		for i, r := range rows {
			if !isSpecialSeason(r.SeasonNumber) {
				cand = append(cand, i)
			}
		}
	}
	if len(cand) == 0 {
		for i := range rows {
			cand = append(cand, i)
		}
	}
	if len(cand) == 0 {
		return upNextNone, -1
	}

	// Resume: the most recently touched episode with a resume point.
	resume := -1
	for _, i := range cand {
		if !rows[i].Resumable {
			continue
		}
		if resume < 0 || rows[i].LastActivityAt.Time.After(rows[resume].LastActivityAt.Time) {
			resume = i
		}
	}
	if resume >= 0 {
		return upNextResume, resume
	}

	// Anchor: most recently watched; ties (a season marked at once) go to
	// the later episode, which is the later candidate position.
	anchor := -1
	for pos, i := range cand {
		if rows[i].Status != markWatched {
			continue
		}
		if anchor < 0 || !rows[i].LastActivityAt.Time.Before(rows[cand[anchor]].LastActivityAt.Time) {
			anchor = pos
		}
	}
	if anchor < 0 {
		return upNextStart, cand[0]
	}
	for _, i := range cand[anchor+1:] {
		if rows[i].Status != markWatched {
			return upNextNext, i
		}
	}
	for _, i := range cand[:anchor] {
		if rows[i].Status != markWatched {
			return upNextNext, i
		}
	}
	return upNextRewatch, cand[0]
}

// isSpecialSeason reports season 0 (specials). An unknown season number is
// treated as a regular season.
func isSpecialSeason(n *int32) bool {
	return n != nil && *n == 0
}

func derefInt32(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

// loadVisibleItem parses {id}, loads the item and applies the caller's item
// gate. On false a response has been written: 401 without claims, 400 for a
// malformed id, 404 for a missing / inaccessible / over-ceiling item (one
// answer for all three, so the endpoint is not an existence oracle).
func (h *WatchStateHandler) loadVisibleItem(w http.ResponseWriter, r *http.Request) (gen.GetMediaItemRow, bool) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return gen.GetMediaItemRow{}, false
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return gen.GetMediaItemRow{}, false
	}
	item, err := h.db.GetMediaItem(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return gen.GetMediaItemRow{}, false
		}
		h.logger.ErrorContext(r.Context(), "watch state: get item", "item_id", id, "err", err)
		respond.InternalError(w, r)
		return gen.GetMediaItemRow{}, false
	}
	if !itemAddAllowed(w, r, h.access, h.logger, item) {
		return gen.GetMediaItemRow{}, false
	}
	return item, true
}
