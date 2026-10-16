package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/watchstatus"
)

// WatchlistDB is the listing half of the watch-status feature: the caller's
// statuses as a pageable list plus per-(status, type) counts. Satisfied by
// *gen.Queries.
type WatchlistDB interface {
	ListWatchlist(ctx context.Context, arg gen.ListWatchlistParams) ([]gen.ListWatchlistRow, error)
	CountWatchlist(ctx context.Context, arg gen.CountWatchlistParams) (int64, error)
	CountWatchlistByStatusType(ctx context.Context, arg gen.CountWatchlistByStatusTypeParams) ([]gen.CountWatchlistByStatusTypeRow, error)
}

// WithWatchlist enables GET /api/v1/watchlist and /watchlist/counts. The
// listing applies the library ACL from WithItemGate's checker, so both must
// be wired (ValidateLibraryAccess asserts the checker at startup).
func (h *WatchStatusHandler) WithWatchlist(db WatchlistDB) *WatchStatusHandler {
	h.list = db
	return h
}

// LibraryAccessWired reports whether the library-ACL checker is wired. The
// watchlist listing and the per-item gate both fail open without it.
func (h *WatchStatusHandler) LibraryAccessWired() bool { return h.access != nil }

// watchlistStatusAll is the `status` value that lists every status at once.
const watchlistStatusAll = "all"

// watchlistItemTypes is the accepted `type` filter set — every media_items
// type a status can be attached to (the table's CHECK constraint list).
var watchlistItemTypes = map[string]bool{
	"movie": true, "show": true, "season": true, "episode": true,
	"track": true, "album": true, "artist": true, "photo": true,
	"music_video": true, "audiobook": true, "audiobook_chapter": true,
	"book_author": true, "book_series": true, "podcast": true,
	"podcast_episode": true, "home_video": true, "book": true,
}

// WatchlistItem is one card on the watchlist: the item, enough ancestry to
// label an episode ("Show · S2E5") or a track, the status, and the caller's
// watch state for a progress badge.
type WatchlistItem struct {
	ID        string `json:"id"`
	LibraryID string `json:"library_id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Year      *int32 `json:"year,omitempty"`
	// Index is the episode / season / track number when the type has one.
	Index    *int32  `json:"index,omitempty"`
	ParentID *string `json:"parent_id,omitempty"`
	// ParentTitle / ParentIndex: the season (title + number) for an
	// episode, the album for a track, the show for a season.
	ParentTitle *string `json:"parent_title,omitempty"`
	ParentIndex *int32  `json:"parent_index,omitempty"`
	// GrandparentID / GrandparentTitle: the show for an episode, the artist
	// for a track.
	GrandparentID    *string `json:"grandparent_id,omitempty"`
	GrandparentTitle *string `json:"grandparent_title,omitempty"`
	// PosterPath is the item's own poster, else its parent's, else its
	// grandparent's — an episode or season card shows the show's art.
	PosterPath *string `json:"poster_path,omitempty"`
	FanartPath *string `json:"fanart_path,omitempty"`
	ThumbPath  *string `json:"thumb_path,omitempty"`
	DurationMS *int64  `json:"duration_ms,omitempty"`
	// WatchState is watched / in_progress / unwatched, as the library
	// watch filter computes it (a show rolls up its episodes).
	WatchState string `json:"watch_state"`
	Status     string `json:"status"`
	// StatusCreatedAt is when the item first got any status;
	// StatusUpdatedAt when it entered the current one.
	StatusCreatedAt time.Time `json:"status_created_at"`
	StatusUpdatedAt time.Time `json:"status_updated_at"`
}

// watchlistFilter is the caller's visibility scope, shared by List and
// Counts so the rows, the total and the tab badges agree.
type watchlistFilter struct {
	userID     uuid.UUID
	libraryIDs []uuid.UUID // nil = every library (admin)
	maxRank    *int32
	blocked    []string
	allowed    []string
}

// watchlistScope resolves the caller's library grants, rating ceiling and
// label restriction. false means a response was already written.
func (h *WatchStatusHandler) watchlistScope(w http.ResponseWriter, r *http.Request) (watchlistFilter, bool) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return watchlistFilter{}, false
	}
	if h.list == nil {
		respond.NotFound(w, r)
		return watchlistFilter{}, false
	}
	f := watchlistFilter{
		userID:  claims.UserID,
		maxRank: maxRatingRankFromClaims(claims.MaxContentRating),
		blocked: claims.LabelFilter().BlockedArg(),
		allowed: claims.LabelFilter().AllowedArg(),
	}
	if h.access != nil {
		allowed, err := h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "watchlist: allowed libraries", "err", err)
			respond.InternalError(w, r)
			return watchlistFilter{}, false
		}
		// Non-nil (possibly empty) restricts the SQL to the grant set, so
		// paging and totals are computed over what the caller can see.
		f.libraryIDs = allowedLibraryList(allowed)
	}
	return f, true
}

// List handles GET /api/v1/watchlist — the caller's watching-status list.
//
//	status  plan_to_watch (default) | watching | on_hold | completed | dropped | all
//	type    optional media type filter (movie, show, episode, album, …)
//	sort    recent (default: newest status change first) | title
//	limit / offset  paging (default 50, max 200)
func (h *WatchStatusHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	status := q.Get("status")
	if status == "" {
		status = watchstatus.StatusPlanToWatch
	}
	var statusArg *string
	if status != watchlistStatusAll {
		if !watchstatus.IsValidStatus(status) {
			respond.BadRequest(w, r, "status must be one of plan_to_watch, watching, on_hold, completed, dropped, all")
			return
		}
		statusArg = &status
	}

	var typeArg *string
	if t := q.Get("type"); t != "" {
		if !watchlistItemTypes[t] {
			respond.BadRequest(w, r, "unknown item type")
			return
		}
		typeArg = &t
	}

	sort := q.Get("sort")
	switch sort {
	case "":
		sort = "recent"
	case "recent", "title":
	default:
		respond.BadRequest(w, r, "sort must be recent or title")
		return
	}

	f, ok := h.watchlistScope(w, r)
	if !ok {
		return
	}
	page := respond.ParsePagination(r, 50, 200)

	rows, err := h.list.ListWatchlist(r.Context(), gen.ListWatchlistParams{
		UserID:        f.userID,
		MaxRatingRank: f.maxRank,
		Status:        statusArg,
		ItemType:      typeArg,
		LibraryIds:    f.libraryIDs,
		BlockedLabels: f.blocked,
		AllowedLabels: f.allowed,
		Sort:          sort,
		Off:           page.Offset,
		Lim:           page.Limit,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "watchlist: list", "user_id", f.userID, "err", err)
		respond.InternalError(w, r)
		return
	}
	total, err := h.list.CountWatchlist(r.Context(), gen.CountWatchlistParams{
		UserID:        f.userID,
		Status:        statusArg,
		ItemType:      typeArg,
		LibraryIds:    f.libraryIDs,
		MaxRatingRank: f.maxRank,
		BlockedLabels: f.blocked,
		AllowedLabels: f.allowed,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "watchlist: count", "user_id", f.userID, "err", err)
		respond.InternalError(w, r)
		return
	}

	out := make([]WatchlistItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, watchlistItemFromRow(row))
	}
	respond.List(w, r, out, total, "")
}

func watchlistItemFromRow(row gen.ListWatchlistRow) WatchlistItem {
	poster := row.PosterPath
	if poster == nil {
		poster = row.ParentPosterPath
	}
	if poster == nil {
		poster = row.GrandparentPosterPath
	}
	it := WatchlistItem{
		ID:               row.ID.String(),
		LibraryID:        row.LibraryID.String(),
		Type:             row.Type,
		Title:            row.Title,
		Year:             row.Year,
		Index:            row.Index,
		ParentID:         pgUUIDString(row.ParentID),
		ParentTitle:      row.ParentTitle,
		ParentIndex:      row.ParentIndex,
		GrandparentID:    pgUUIDString(row.GrandparentID),
		GrandparentTitle: row.GrandparentTitle,
		PosterPath:       poster,
		FanartPath:       row.FanartPath,
		ThumbPath:        row.ThumbPath,
		DurationMS:       row.DurationMs,
		WatchState:       row.WatchState,
		Status:           row.Status,
	}
	if row.StatusCreatedAt.Valid {
		it.StatusCreatedAt = row.StatusCreatedAt.Time
	}
	if row.StatusUpdatedAt.Valid {
		it.StatusUpdatedAt = row.StatusUpdatedAt.Time
	}
	return it
}

// WatchlistStatusCount is one status tab's badge: its total and the
// per-type breakdown the type filter offers.
type WatchlistStatusCount struct {
	Total int64            `json:"total"`
	Types map[string]int64 `json:"types"`
}

// WatchlistCounts is the GET /api/v1/watchlist/counts payload. Statuses
// always carries all five keys (zero when empty).
type WatchlistCounts struct {
	Total    int64                           `json:"total"`
	Statuses map[string]WatchlistStatusCount `json:"statuses"`
}

// Counts handles GET /api/v1/watchlist/counts: per-status totals with a
// per-type breakdown, under the same visibility filters as List.
func (h *WatchStatusHandler) Counts(w http.ResponseWriter, r *http.Request) {
	f, ok := h.watchlistScope(w, r)
	if !ok {
		return
	}
	rows, err := h.list.CountWatchlistByStatusType(r.Context(), gen.CountWatchlistByStatusTypeParams{
		UserID:        f.userID,
		LibraryIds:    f.libraryIDs,
		MaxRatingRank: f.maxRank,
		BlockedLabels: f.blocked,
		AllowedLabels: f.allowed,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "watchlist: counts", "user_id", f.userID, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := WatchlistCounts{Statuses: make(map[string]WatchlistStatusCount, len(watchstatus.AllStatuses()))}
	for _, s := range watchstatus.AllStatuses() {
		out.Statuses[s] = WatchlistStatusCount{Types: map[string]int64{}}
	}
	for _, row := range rows {
		sc, known := out.Statuses[row.Status]
		if !known {
			continue
		}
		sc.Total += row.Count
		sc.Types[row.Type] += row.Count
		out.Statuses[row.Status] = sc
		out.Total += row.Count
	}
	respond.Success(w, r, out)
}
