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
	epDB    EpisodePosterDB // optional — when set, substitutes show posters for episode rows
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

// WithEpisodePoster wires the lookup used to substitute the show
// poster for episode rows in Continue Watching, Recently Added, and
// Trending. Honours the per-user `episode_use_show_poster` flag —
// when off, this is a no-op. When unset (tests), no substitution
// happens at all.
func (h *HubHandler) WithEpisodePoster(db EpisodePosterDB) *HubHandler {
	h.epDB = db
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
//
// continue_watching keeps the legacy combined feed every client has
// rendered since v1; continue_watching_tv / _movies / _other are the
// pre-split rows (TV first, then movies, then everything else) that
// every client should migrate to. We populate both for one release
// cycle so older client builds keep working.
type HubResponse struct {
	ContinueWatching       []HubItem       `json:"continue_watching"`
	ContinueWatchingTV     []HubItem       `json:"continue_watching_tv"`
	ContinueWatchingMovies []HubItem       `json:"continue_watching_movies"`
	ContinueWatchingOther  []HubItem       `json:"continue_watching_other"`
	RecentlyAdded          []HubItem       `json:"recently_added"`
	ByLibrary              []HubLibraryRow `json:"recently_added_by_library"`
	Trending               []HubItem       `json:"trending"`
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
		ContinueWatching:       []HubItem{},
		ContinueWatchingTV:     []HubItem{},
		ContinueWatchingMovies: []HubItem{},
		ContinueWatchingOther:  []HubItem{},
		RecentlyAdded:          []HubItem{},
		ByLibrary:              []HubLibraryRow{},
		Trending:               []HubItem{},
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
			item := HubItem{
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
			}
			out.ContinueWatching = append(out.ContinueWatching, item)
			switch row.Type {
			case "episode":
				out.ContinueWatchingTV = append(out.ContinueWatchingTV, item)
			case "movie":
				out.ContinueWatchingMovies = append(out.ContinueWatchingMovies, item)
			default:
				out.ContinueWatchingOther = append(out.ContinueWatchingOther, item)
			}
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
		seen := make(map[uuid.UUID]bool)
		for _, row := range raRows {
			if !libAllowed(row.LibraryID) {
				continue
			}
			// SQL already dedupes (one row per show via window function on
			// grandparent.id, one row per movie/album/photo). The seen-set
			// here is only a defensive guard against the same row appearing
			// twice in the result set (shouldn't happen, but cheap to check).
			if seen[row.ID] {
				continue
			}
			seen[row.ID] = true
			year := intPtrFrom32(row.Year)
			// Episodes inherit the show's poster via fallback_poster — the
			// episode's own poster_path is almost always NULL because TMDB
			// gives us per-show artwork, not per-episode stills.
			poster := row.PosterPath
			if poster == nil && row.FallbackPoster != nil {
				poster = row.FallbackPoster
			}
			out.RecentlyAdded = append(out.RecentlyAdded, HubItem{
				ID:         row.ID.String(),
				Title:      row.Title,
				Type:       row.Type,
				Year:       year,
				PosterPath: poster,
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

	// Episode-poster substitution. Collect every episode ID across
	// the four sections, hit the lookup once, then patch posters in
	// place. Episodes whose show ancestor chain breaks (orphan
	// season, missing show) are left with their original art —
	// callers always get *some* poster, never an empty cell.
	if h.epDB != nil {
		var epIDs []uuid.UUID
		collect := func(items []HubItem) {
			for _, it := range items {
				if it.Type != "episode" {
					continue
				}
				if id, err := uuid.Parse(it.ID); err == nil {
					epIDs = append(epIDs, id)
				}
			}
		}
		collect(out.ContinueWatching)
		collect(out.RecentlyAdded)
		collect(out.Trending)
		for _, row := range out.ByLibrary {
			collect(row.Items)
		}
		if posters := resolveEpisodeShowPosters(r.Context(), h.epDB, claims.UserID, epIDs); len(posters) > 0 {
			apply := func(items []HubItem) {
				for i := range items {
					if items[i].Type != "episode" {
						continue
					}
					id, err := uuid.Parse(items[i].ID)
					if err != nil {
						continue
					}
					if p, ok := posters[id]; ok {
						pp := p
						items[i].PosterPath = &pp
						items[i].ThumbPath = &pp
					}
				}
			}
			apply(out.ContinueWatching)
			apply(out.RecentlyAdded)
			apply(out.Trending)
			for i := range out.ByLibrary {
				apply(out.ByLibrary[i].Items)
			}
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
		seen := make(map[uuid.UUID]bool, perLib)
		for _, row := range s.rows {
			// See note on the global Recently Added path: SQL already
			// dedupes (per-show for episodes, per-item for movies/albums/
			// photos); this seen-set is just a defensive duplicate-row
			// guard. Keyed on row.ID so two different shows with the
			// same most-recent-episode title don't collide.
			if seen[row.ID] {
				continue
			}
			seen[row.ID] = true
			year := intPtrFrom32(row.Year)
			poster := row.PosterPath
			if poster == nil && row.FallbackPoster != nil {
				poster = row.FallbackPoster
			}
			items = append(items, HubItem{
				ID:         row.ID.String(),
				Title:      row.Title,
				Type:       row.Type,
				Year:       year,
				PosterPath: poster,
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
