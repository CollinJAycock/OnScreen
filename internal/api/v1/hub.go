package v1

import (
	"context"
	"log/slog"
	"net/http"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/sync/errgroup"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
)

// HubDB defines the database queries the hub handler needs.
type HubDB interface {
	ListContinueWatching(ctx context.Context, arg gen.ListContinueWatchingParams) ([]gen.ListContinueWatchingRow, error)
	ListRecentlyAdded(ctx context.Context, arg gen.ListRecentlyAddedParams) ([]gen.ListRecentlyAddedRow, error)
	ListTrending(ctx context.Context, arg gen.ListTrendingParams) ([]gen.ListTrendingRow, error)
	ListSeedItemsForUser(ctx context.Context, arg gen.ListSeedItemsForUserParams) ([]gen.ListSeedItemsForUserRow, error)
	ListCooccurrentItems(ctx context.Context, arg gen.ListCooccurrentItemsParams) ([]gen.ListCooccurrentItemsRow, error)
}

// HubLibraryLister returns the libraries the home page should surface
// per-library recently-added rows for. Kept as its own interface so
// the hub handler doesn't need to know how the library list is fetched
// (tests supply a stub, production wires library.Service).
type HubLibraryLister interface {
	List(ctx context.Context) ([]library.Library, error)
}

// HubHandler serves the home page hub data.
type HubHandler struct {
	db      HubDB
	access  LibraryAccessChecker
	libs    HubLibraryLister
	logger  *slog.Logger
	perLib  int32 // items per library row; defaults to 12 if zero
}

// NewHubHandler creates a HubHandler.
func NewHubHandler(db HubDB, logger *slog.Logger) *HubHandler {
	return &HubHandler{db: db, logger: logger}
}

// WithLibraryAccess enables per-user library filtering on hub rows.
func (h *HubHandler) WithLibraryAccess(a LibraryAccessChecker) *HubHandler {
	h.access = a
	return h
}

// WithLibraries wires the library lister that drives the per-library
// "Recently added to <library>" home-screen rows. Without this the
// response's ByLibrary field stays empty and the home page just shows
// the global recently-added section.
func (h *HubHandler) WithLibraries(l HubLibraryLister) *HubHandler {
	h.libs = l
	return h
}

// HubResponse is the combined home page data.
type HubResponse struct {
	ContinueWatching   []HubItem            `json:"continue_watching"`
	RecentlyAdded      []HubItem            `json:"recently_added"`
	ByLibrary          []HubLibraryRow      `json:"recently_added_by_library"`
	Trending           []HubItem            `json:"trending"`
	BecauseYouWatched  []BecauseYouWatched  `json:"because_you_watched"`
}

// BecauseYouWatched is one row of personalised recommendations on the
// home hub: a seed item the user recently completed plus the top-N
// items most cooccurrent with it (excluding ones the user has already
// watched). The frontend renders one row per seed.
type BecauseYouWatched struct {
	Seed  HubSeedItem `json:"seed"`
	Items []HubItem   `json:"items"`
}

// HubSeedItem is the compact representation of the seed item that
// labels a "Because you watched X" row.
type HubSeedItem struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	PosterPath *string `json:"poster_path,omitempty"`
	ThumbPath  *string `json:"thumb_path,omitempty"`
	UpdatedAt  int64   `json:"updated_at"`
}

// HubLibraryRow is one "Recently added to <library>" strip on the home
// page. Library info is denormalized so the frontend can label each row
// without an extra lookup.
type HubLibraryRow struct {
	LibraryID   string    `json:"library_id"`
	LibraryName string    `json:"library_name"`
	LibraryType string    `json:"library_type"`
	Items       []HubItem `json:"items"`
}

// HubItem is a compact item for hub display.
type HubItem struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Type         string  `json:"type"`
	Year         *int    `json:"year,omitempty"`
	PosterPath   *string `json:"poster_path,omitempty"`
	FanartPath   *string `json:"fanart_path,omitempty"`
	ThumbPath    *string `json:"thumb_path,omitempty"`
	ViewOffsetMS *int64  `json:"view_offset_ms,omitempty"`
	DurationMS   *int64  `json:"duration_ms,omitempty"`
	UpdatedAt    int64   `json:"updated_at"`
}

// Get handles GET /api/v1/hub.
func (h *HubHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}

	out := HubResponse{
		ContinueWatching:  []HubItem{},
		RecentlyAdded:     []HubItem{},
		ByLibrary:         []HubLibraryRow{},
		Trending:          []HubItem{},
		BecauseYouWatched: []BecauseYouWatched{},
	}

	// Convert max content rating from claims to a rank for SQL filtering.
	maxRank := maxRatingRankFromClaims(claims.MaxContentRating)

	// Pre-compute allowed library set. Nil means admin → no filtering.
	var allowed map[uuid.UUID]struct{}
	if h.access != nil {
		var err error
		allowed, err = h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "hub: allowed libraries", "err", err)
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

	// Continue watching — items the user has in progress.
	cwRows, err := h.db.ListContinueWatching(r.Context(), gen.ListContinueWatchingParams{
		UserID:        claims.UserID,
		Limit:         20,
		MaxRatingRank: maxRank,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "hub: continue watching", "err", err)
	} else {
		for _, row := range cwRows {
			if !libAllowed(row.LibraryID) {
				continue
			}
			year := intPtrFrom32(row.Year)
			offset := row.ViewOffset
			out.ContinueWatching = append(out.ContinueWatching, HubItem{
				ID:           row.ID.String(),
				Title:        row.Title,
				Type:         row.Type,
				Year:         year,
				PosterPath:   row.FallbackPoster,
				FanartPath:   row.FanartPath,
				ThumbPath:    row.ThumbPath,
				ViewOffsetMS: &offset,
				DurationMS:   row.DurationMs,
				UpdatedAt:    timestamptzToMilli(row.UpdatedAt),
			})
		}
	}

	// Recently added — newest items across all libraries (the mixed
	// top-of-home strip). Kept for discovery across the whole catalog;
	// the per-library strips below narrow the same idea per section.
	// Fetch extra rows so we still have ≥20 after deduplication.
	raRows, err := h.db.ListRecentlyAdded(r.Context(), gen.ListRecentlyAddedParams{
		Limit:         40,
		MaxRatingRank: maxRank,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "hub: recently added", "err", err)
	} else {
		seen := make(map[string]bool)
		for _, row := range raRows {
			if !libAllowed(row.LibraryID) {
				continue
			}
			// Deduplicate by title+type (handles duplicate media_items rows).
			key := row.Type + "|" + row.Title
			if seen[key] {
				continue
			}
			seen[key] = true
			year := intPtrFrom32(row.Year)
			out.RecentlyAdded = append(out.RecentlyAdded, HubItem{
				ID:         row.ID.String(),
				Title:      row.Title,
				Type:       row.Type,
				Year:       year,
				PosterPath: row.PosterPath,
				FanartPath: row.FanartPath,
				DurationMS: row.DurationMs,
				UpdatedAt:  timestamptzToMilli(row.UpdatedAt),
			})
			if len(out.RecentlyAdded) >= 20 {
				break
			}
		}
	}

	// Per-library recently added — one row per library the user can see.
	// Queries run in parallel because they're independent PK-indexed reads.
	if h.libs != nil {
		out.ByLibrary = h.perLibraryRecentlyAdded(r.Context(), libAllowed, maxRank)
	}

	// Trending — global "what others are watching" row over the last 7
	// days. Same content for every user (no personalisation), filtered
	// down by the caller's library access + parental ceiling. 30 raw
	// rows so the post-access-filter result still has 12+ candidates
	// even for a heavily-restricted user.
	trRows, err := h.db.ListTrending(r.Context(), gen.ListTrendingParams{
		WindowDays:    7,
		MaxRatingRank: maxRank,
		ResultLimit:   30,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "hub: trending", "err", err)
	} else {
		for _, row := range trRows {
			if !libAllowed(row.LibraryID) {
				continue
			}
			out.Trending = append(out.Trending, HubItem{
				ID:         row.ID.String(),
				Title:      row.Title,
				Type:       row.Type,
				Year:       intPtrFrom32(row.Year),
				PosterPath: row.PosterPath,
				FanartPath: row.FanartPath,
				ThumbPath:  row.ThumbPath,
				DurationMS: row.DurationMs,
				UpdatedAt:  row.UpdatedAt.Time.UnixMilli(),
			})
			if len(out.Trending) >= 12 {
				break
			}
		}
	}

	// Because-you-watched: per-user personalised recommendations.
	// Seeds = the user's last 3 completed items (more rows would clutter
	// the hub with similar-looking shelves). Each seed gets up to 8
	// cooccurrent items, library-access-filtered. Empty for new users
	// who haven't completed anything — the row simply doesn't render.
	seeds, err := h.db.ListSeedItemsForUser(r.Context(), gen.ListSeedItemsForUserParams{
		UserID: claims.UserID,
		Limit:  3,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "hub: byw seeds", "err", err)
	} else {
		for _, seed := range seeds {
			rows, err := h.db.ListCooccurrentItems(r.Context(), gen.ListCooccurrentItemsParams{
				Seed:          seed.ID,
				UserID:        claims.UserID,
				MaxRatingRank: maxRank,
				ResultLimit:   16, // raw — filtered down to 8 after lib-access
			})
			if err != nil {
				h.logger.WarnContext(r.Context(), "hub: byw lookup", "seed", seed.ID, "err", err)
				continue
			}
			items := make([]HubItem, 0, 8)
			for _, row := range rows {
				if !libAllowed(row.LibraryID) {
					continue
				}
				items = append(items, HubItem{
					ID:         row.ID.String(),
					Title:      row.Title,
					Type:       row.Type,
					Year:       intPtrFrom32(row.Year),
					PosterPath: row.PosterPath,
					FanartPath: row.FanartPath,
					ThumbPath:  row.ThumbPath,
					DurationMS: row.DurationMs,
					UpdatedAt:  row.UpdatedAt.Time.UnixMilli(),
				})
				if len(items) >= 8 {
					break
				}
			}
			if len(items) == 0 {
				// Skip rendering an empty shelf — user hasn't watched
				// anything cooccurrent yet (fresh install or niche item).
				continue
			}
			out.BecauseYouWatched = append(out.BecauseYouWatched, BecauseYouWatched{
				Seed: HubSeedItem{
					ID:         seed.ID.String(),
					Title:      seed.Title,
					PosterPath: seed.PosterPath,
					ThumbPath:  seed.ThumbPath,
					UpdatedAt:  seed.UpdatedAt.Time.UnixMilli(),
				},
				Items: items,
			})
		}
	}

	respond.Success(w, r, out)
}

// perLibraryRecentlyAdded fires one ListRecentlyAdded per library the
// user can see, in parallel, and returns them in the library's own
// creation order so the home page has a stable layout across reloads.
// Libraries with no items in the result are omitted — the section
// wouldn't render anything useful and an empty row is just noise.
func (h *HubHandler) perLibraryRecentlyAdded(ctx context.Context, libAllowed func(uuid.UUID) bool, maxRank *int32) []HubLibraryRow {
	libs, err := h.libs.List(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "hub: list libraries", "err", err)
		return nil
	}

	perLib := h.perLib
	if perLib <= 0 {
		perLib = 12
	}

	// Filter to libraries the user may access and that have a visible
	// item type. Non-music/movie/show/photo libraries (e.g. DVR) have
	// no items matching the recently-added WHERE clause and would
	// always come back empty — skip the round trip entirely.
	type slot struct {
		lib  library.Library
		rows []gen.ListRecentlyAddedRow
	}
	slots := make([]*slot, 0, len(libs))
	for _, lib := range libs {
		if lib.DeletedAt != nil {
			continue
		}
		if !libAllowed(lib.ID) {
			continue
		}
		switch lib.Type {
		case "movie", "show", "music", "photo":
		default:
			continue
		}
		slots = append(slots, &slot{lib: lib})
	}

	// Stable order: library creation time ascending, so newly added
	// libraries always land at the bottom of the home page rather than
	// shuffling older ones around on the user every time.
	sort.SliceStable(slots, func(i, j int) bool {
		return slots[i].lib.CreatedAt.Before(slots[j].lib.CreatedAt)
	})

	g, gctx := errgroup.WithContext(ctx)
	for _, s := range slots {
		s := s
		g.Go(func() error {
			id := s.lib.ID
			rows, err := h.db.ListRecentlyAdded(gctx, gen.ListRecentlyAddedParams{
				LibraryID:     pgtype.UUID{Bytes: id, Valid: true},
				Limit:         int32(perLib) * 2, // dedupe budget
				MaxRatingRank: maxRank,
			})
			if err != nil {
				// Logged; don't fail the whole hub response because one
				// library's strip couldn't be built.
				h.logger.WarnContext(gctx, "hub: per-library recently added",
					"library_id", id, "err", err)
				return nil
			}
			s.rows = rows
			return nil
		})
	}
	_ = g.Wait()

	out := make([]HubLibraryRow, 0, len(slots))
	for _, s := range slots {
		items := make([]HubItem, 0, perLib)
		seen := make(map[string]bool, perLib)
		for _, row := range s.rows {
			key := row.Type + "|" + row.Title
			if seen[key] {
				continue
			}
			seen[key] = true
			year := intPtrFrom32(row.Year)
			items = append(items, HubItem{
				ID:         row.ID.String(),
				Title:      row.Title,
				Type:       row.Type,
				Year:       year,
				PosterPath: row.PosterPath,
				FanartPath: row.FanartPath,
				DurationMS: row.DurationMs,
				UpdatedAt:  timestamptzToMilli(row.UpdatedAt),
			})
			if int32(len(items)) >= perLib {
				break
			}
		}
		if len(items) == 0 {
			continue
		}
		out = append(out, HubLibraryRow{
			LibraryID:   s.lib.ID.String(),
			LibraryName: s.lib.Name,
			LibraryType: s.lib.Type,
			Items:       items,
		})
	}
	return out
}

func intPtrFrom32(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func timestamptzToMilli(ts pgtype.Timestamptz) int64 {
	if !ts.Valid {
		return 0
	}
	return ts.Time.UnixMilli()
}

// maxRatingRankFromClaims converts a Claims.MaxContentRating string to the
// *int32 expected by sqlc narg parameters. Returns nil when unrestricted.
func maxRatingRankFromClaims(maxContentRating string) *int32 {
	rk := contentrating.MaxRatingRank(maxContentRating)
	if rk == nil {
		return nil
	}
	v := int32(*rk)
	return &v
}
