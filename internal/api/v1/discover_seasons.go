package v1

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/tmdb"
	"github.com/onscreen/onscreen/internal/requests"
)

// Season-level requests on the Discover surface: the season picker's data
// (GET /discover/tv/{tmdb_id}/seasons) and the missing_seasons /
// partially_available decoration on search results for shows already in the
// caller's library.

// DiscoverSeasonTMDB lists a show's seasons with TMDB's airing position.
type DiscoverSeasonTMDB interface {
	GetTVSeasons(ctx context.Context, tmdbID int) (*metadata.TVSeasonList, error)
}

// DiscoverSeasonDB counts the episodes per season the caller can see.
type DiscoverSeasonDB interface {
	ListOwnedEpisodeCountsByShowTMDB(ctx context.Context, arg gen.ListOwnedEpisodeCountsByShowTMDBParams) ([]gen.ListOwnedEpisodeCountsByShowTMDBRow, error)
}

// ErrSeasonsTMDBUnavailable is what a DiscoverSeasonTMDB returns (wrapped)
// when no TMDB agent is configured; the seasons endpoint answers 503 for it.
// The same sentinel as ErrDiscoverTMDBUnavailable.
var ErrSeasonsTMDBUnavailable = ErrDiscoverTMDBUnavailable

// maxDiscoverSeasonLookups caps how many in-library shows in one search get
// their season list read from TMDB (each is a TMDB call on a cold cache).
const maxDiscoverSeasonLookups = 8

// WithSeasons wires season-level requests: t reads a show's season list, db
// counts the library's episodes per season (scoped to the caller like
// in_library). Returns the handler for chaining.
func (h *DiscoverHandler) WithSeasons(t DiscoverSeasonTMDB, db DiscoverSeasonDB) *DiscoverHandler {
	h.seasons = t
	h.seasonDB = db
	return h
}

// SeasonInfo is one row of GET /discover/tv/{tmdb_id}/seasons.
type SeasonInfo struct {
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	EpisodeCount int    `json:"episode_count"`
	// AiredEpisodes is how many of the season's episodes have aired.
	AiredEpisodes int `json:"aired_episodes"`
	// AirDate is the premiere (YYYY-MM-DD), null when unannounced.
	AirDate   *string `json:"air_date"`
	PosterURL string  `json:"poster_url,omitempty"`
	// OwnedEpisodes counts the season's episodes on disk in libraries the
	// caller can see (within their rating ceiling).
	OwnedEpisodes int `json:"owned_episodes"`
	// Requested is the status of the caller's own active request covering
	// the season (pending / approved / downloading), null when none.
	Requested *string    `json:"requested"`
	RequestID *uuid.UUID `json:"request_id,omitempty"`
}

// Seasons handles GET /api/v1/discover/tv/{tmdb_id}/seasons: the show's
// seasons from TMDB, specials (season 0) last, each with how much of it the
// caller's library holds and whether the caller has already requested it.
// 503 when TMDB isn't configured, 403 REQUESTS_DISABLED for an account that
// may not request, 404 when TMDB has no such show.
func (h *DiscoverHandler) Seasons(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	const unavailable = "TMDB API key is not configured — season requests need TMDB"
	if h.seasons == nil {
		writeTMDBUnavailable(w, r, unavailable)
		return
	}
	if !h.requestsAllowed(w, r, claims) {
		return
	}
	tmdbID, err := strconv.Atoi(chi.URLParam(r, "tmdb_id"))
	if err != nil || tmdbID <= 0 || tmdbID > 1<<31-1 {
		respond.BadRequest(w, r, "invalid tmdb id")
		return
	}
	list, err := h.seasons.GetTVSeasons(r.Context(), tmdbID)
	if err == nil && list == nil {
		err = ErrSeasonsTMDBUnavailable
	}
	if err != nil {
		if tmdb.IsNotFound(err) {
			respond.NotFound(w, r)
			return
		}
		if errors.Is(err, ErrSeasonsTMDBUnavailable) {
			writeTMDBUnavailable(w, r, unavailable)
			return
		}
		h.logger.WarnContext(r.Context(), "discover: tmdb season list", "tmdb_id", tmdbID, "err", err)
		respond.Error(w, r, http.StatusBadGateway, "TMDB_LOOKUP_FAILED",
			"couldn't read the season list from TMDB — try again shortly")
		return
	}

	owned := h.ownedSeasons(r.Context(), []int32{int32(tmdbID)})[int32(tmdbID)]
	var active []gen.MediaRequest
	if h.requests != nil {
		rows, err := h.requests.FindActiveForUserBatch(r.Context(), claims.UserID, []int{tmdbID})
		if err != nil {
			h.logger.WarnContext(r.Context(), "discover: active requests for seasons", "err", err)
		}
		for _, req := range rows {
			if req.Type == requests.TypeShow && int(req.TmdbID) == tmdbID {
				active = append(active, req)
			}
		}
	}

	now := time.Now()
	out := make([]SeasonInfo, 0, len(list.Seasons))
	for _, s := range list.Seasons {
		info := SeasonInfo{
			SeasonNumber:  s.Number,
			Name:          s.Name,
			EpisodeCount:  s.EpisodeCount,
			AiredEpisodes: list.AiredEpisodes(s.Number, now),
			PosterURL:     s.PosterURL,
			OwnedEpisodes: owned[s.Number],
		}
		if !s.AirDate.IsZero() {
			d := s.AirDate.Format("2006-01-02")
			info.AirDate = &d
		}
		for _, req := range active {
			if requests.CoversSeason(req, s.Number) {
				st, id := req.Status, req.ID
				info.Requested, info.RequestID = &st, &id
				break
			}
		}
		out = append(out, info)
	}
	respond.Success(w, r, out)
}

// ownedSeasons maps show TMDB id → season → distinct episodes on disk, over
// the libraries the caller can see and within their rating ceiling. nil when
// not wired, on a lookup failure, or when the caller's grants couldn't be
// read (fail closed: nothing owned).
func (h *DiscoverHandler) ownedSeasons(ctx context.Context, ids []int32) map[int32]map[int]int {
	if h.seasonDB == nil || len(ids) == 0 {
		return nil
	}
	libIDs, maxRank, ok := h.callerScope(ctx)
	if !ok {
		return nil
	}
	rows, err := h.seasonDB.ListOwnedEpisodeCountsByShowTMDB(ctx, gen.ListOwnedEpisodeCountsByShowTMDBParams{
		TmdbIds:       ids,
		LibraryIds:    libIDs,
		MaxRatingRank: maxRank,
	})
	if err != nil {
		h.logger.WarnContext(ctx, "discover: library season counts", "err", err)
		return nil
	}
	out := map[int32]map[int]int{}
	for _, row := range rows {
		m := out[row.TmdbID]
		if m == nil {
			m = map[int]int{}
			out[row.TmdbID] = m
		}
		m[int(row.SeasonNumber)] = int(row.EpisodeCount)
	}
	return out
}

// decorateMissingSeasons fills missing_seasons / partially_available on the
// shows in results that are in the caller's library.
func (h *DiscoverHandler) decorateMissingSeasons(ctx context.Context, results []DiscoverItem) {
	if h.seasons == nil || h.seasonDB == nil {
		return
	}
	var idx []int
	var ids []int32
	for i := range results {
		if results[i].Type != requests.TypeShow || !results[i].InLibrary {
			continue
		}
		if len(idx) == maxDiscoverSeasonLookups {
			break
		}
		idx = append(idx, i)
		ids = append(ids, int32(results[i].TMDBID))
	}
	if len(idx) == 0 {
		return
	}
	owned := h.ownedSeasons(ctx, ids)
	now := time.Now()
	for _, i := range idx {
		list, err := h.seasons.GetTVSeasons(ctx, results[i].TMDBID)
		if err == nil && list == nil {
			continue
		}
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrSeasonsTMDBUnavailable) {
				h.logger.WarnContext(ctx, "discover: tmdb season list", "tmdb_id", results[i].TMDBID, "err", err)
			}
			continue
		}
		missing := missingSeasons(list, owned[int32(results[i].TMDBID)], now)
		results[i].MissingSeasons = &missing
		results[i].PartiallyAvailable = len(missing) > 0
	}
}

// missingSeasons lists the aired regular seasons with fewer episodes owned
// than have aired. Never nil.
func missingSeasons(list *metadata.TVSeasonList, owned map[int]int, now time.Time) []int {
	out := []int{}
	for _, n := range list.AiredSeasons(now) {
		if owned[n] < list.AiredEpisodes(n, now) {
			out = append(out, n)
		}
	}
	return out
}
