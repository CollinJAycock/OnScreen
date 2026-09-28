package requests

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/arrcrypt"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/notification"
)

// Season-level TV requests (migration 00028).
//
// A show request names the seasons it wants (media_requests.seasons; none =
// every season). A user may hold several active requests for one show as long
// as their seasons don't overlap, which is how "request more seasons" works
// for a show that is already partly in the library or partly requested.
//
// Fulfilment is per season: a season is complete when every aired episode is
// on disk and in the library. The request turns available only when all of
// its seasons are complete; each season that completes before then is
// recorded in seasons_available and announced once
// (notification.TypeRequestSeasonAvailable). The request's status enum is
// unchanged — clients parse it strictly — so "partially available" is
// seasons_available, not a new status.

// ErrSeasonsAlreadyRequested is ErrAlreadyRequested for a show: the user has
// an active request whose seasons overlap the requested ones.
var ErrSeasonsAlreadyRequested = fmt.Errorf("%w (overlapping seasons)", ErrAlreadyRequested)

// RequestSeasons returns the seasons a request asked for, ascending; nil means
// every season (and is what a movie request returns).
func RequestSeasons(r gen.MediaRequest) []int {
	if r.Type != TypeShow {
		return nil
	}
	seasons, err := decodeSeasons(r.Seasons)
	if err != nil {
		return nil
	}
	return normalizeSeasons(seasons)
}

// SeasonsOverlap reports whether two season selections share a season. An
// empty selection means every regular season (Sonarr's "all episodes" leaves
// specials out), so it overlaps any selection except one of specials alone —
// the same rule CoversSeason applies.
func SeasonsOverlap(a, b []int) bool {
	switch {
	case len(a) == 0 && len(b) == 0:
		return true
	case len(a) == 0:
		return hasRegularSeason(b)
	case len(b) == 0:
		return hasRegularSeason(a)
	}
	set := make(map[int]bool, len(a))
	for _, n := range a {
		set[n] = true
	}
	for _, n := range b {
		if set[n] {
			return true
		}
	}
	return false
}

func hasRegularSeason(seasons []int) bool {
	for _, n := range seasons {
		if n > 0 {
			return true
		}
	}
	return false
}

// CoversSeason reports whether a request asks for season n.
func CoversSeason(r gen.MediaRequest, n int) bool {
	seasons := RequestSeasons(r)
	if len(seasons) == 0 {
		// An all-seasons request covers every regular season; specials only
		// when named.
		return r.Type == TypeShow && n > 0
	}
	for _, s := range seasons {
		if s == n {
			return true
		}
	}
	return false
}

// normalizeSeasons sorts and de-duplicates a season list; nil for empty.
func normalizeSeasons(in []int) []int {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[int]bool, len(in))
	out := make([]int, 0, len(in))
	for _, n := range in {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// checkDuplicate rejects a request that duplicates one of the user's active
// requests: any active request for the same movie, or an active request for
// the same show whose seasons overlap in.Seasons.
func (s *Service) checkDuplicate(ctx context.Context, in CreateInput) error {
	if in.Type != TypeShow {
		if _, err := s.db.FindActiveRequestForUser(ctx, gen.FindActiveRequestForUserParams{
			UserID: in.UserID,
			Type:   in.Type,
			TmdbID: int32(in.TMDBID),
		}); err == nil {
			return ErrAlreadyRequested
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("requests: lookup duplicate: %w", err)
		}
		return nil
	}
	rows, err := s.db.FindActiveRequestsForUserByTMDB(ctx, gen.FindActiveRequestsForUserByTMDBParams{
		UserID:  in.UserID,
		TmdbIds: []int32{int32(in.TMDBID)},
	})
	if err != nil {
		return fmt.Errorf("requests: lookup duplicate: %w", err)
	}
	for _, r := range rows {
		if r.Type != TypeShow || int(r.TmdbID) != in.TMDBID {
			continue
		}
		if SeasonsOverlap(RequestSeasons(r), in.Seasons) {
			return ErrSeasonsAlreadyRequested
		}
	}
	return nil
}

// validateSeasons checks every requested season against TMDB's season list.
func (s *Service) validateSeasons(ctx context.Context, tmdbID int, seasons []int) error {
	list, err := s.tmdb.GetTVSeasons(ctx, tmdbID)
	if err != nil {
		return fmt.Errorf("%w: seasons: %v", ErrTMDBLookupFailed, err)
	}
	for _, n := range seasons {
		if !list.Has(n) {
			return fmt.Errorf("%w: season %d is not listed on TMDB", ErrInvalidSeasons, n)
		}
	}
	return nil
}

// isUniqueViolation reports a Postgres unique_violation (23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ── per-season fulfilment ──────────────────────────────────────────────────

// settleShowRequests judges each in-flight show request season by season and
// applies the outcome (see applySeasonProgress). items maps a show's TMDB id
// to its library item; every request passed must have one. Returns how many
// requests became available.
func (s *Service) settleShowRequests(ctx context.Context, reqs []gen.MediaRequest, items map[int32]uuid.UUID) int {
	if len(reqs) == 0 {
		return 0
	}
	ev, err := s.newSeasonEvaluator(ctx, reqs)
	if err != nil {
		s.logger.ErrorContext(ctx, "reconcile: library season counts", "err", err)
		return 0
	}
	fulfilled := 0
	for _, r := range reqs {
		if ctx.Err() != nil {
			break
		}
		itemID, ok := items[r.TmdbID]
		if !ok {
			continue
		}
		p, ok := ev.progress(ctx, r)
		if !ok {
			continue
		}
		if s.applySeasonProgress(ctx, r, itemID, p) {
			fulfilled++
		}
	}
	return fulfilled
}

// seasonProgress is one show request's verdict: the seasons it covers right
// now and which of them are complete.
type seasonProgress struct {
	covered  []int
	complete map[int]bool
}

// applySeasonProgress records newly complete seasons and, once every covered
// season is complete, marks the request available. Seasons that complete
// before the rest are recorded one at a time, each with its own notice (the
// conditional update makes that once per season); the season(s) completing
// the request are recorded silently — the request_available notice covers
// them. Reports whether the request became available.
func (s *Service) applySeasonProgress(ctx context.Context, r gen.MediaRequest, itemID uuid.UUID, p seasonProgress) bool {
	if len(p.covered) == 0 {
		// Nothing has aired yet: nothing can be complete.
		return false
	}
	stored := make(map[int]bool, len(r.SeasonsAvailable))
	for _, n := range r.SeasonsAvailable {
		stored[int(n)] = true
	}
	all := true
	var newly []int
	for _, n := range p.covered {
		if !p.complete[n] {
			all = false
			continue
		}
		if !stored[n] {
			newly = append(newly, n)
		}
	}

	for _, n := range newly {
		rows, err := s.db.MarkMediaRequestSeasonAvailable(ctx, gen.MarkMediaRequestSeasonAvailableParams{
			ID:     r.ID,
			Season: int32(n),
		})
		if err != nil {
			s.logger.ErrorContext(ctx, "reconcile: record season available",
				"request_id", r.ID, "season", n, "err", err)
			continue
		}
		if rows == 1 && !all && s.notify != nil {
			s.notify.Notify(ctx, r.UserID, notification.TypeRequestSeasonAvailable,
				fmt.Sprintf("Season %d now available: %s", n, r.Title),
				fmt.Sprintf("Season %d of %q is ready to watch.", n, r.Title),
				&itemID)
		}
	}
	if !all {
		if len(newly) > 0 {
			s.logger.InfoContext(ctx, "request seasons available",
				"request_id", r.ID, "seasons", newly, "covered", p.covered)
		}
		return false
	}
	return s.markAvailable(ctx, r, itemID)
}

// seasonEvaluator answers "which seasons of this show request are complete"
// for one reconcile pass, caching what it asks the library, Sonarr and TMDB.
type seasonEvaluator struct {
	s *Service
	// owned: show TMDB id → season → distinct episodes on disk in the library.
	owned map[int32]map[int]int
	// services: arr service id → client, nil when unusable this pass.
	services map[uuid.UUID]*arr.Client
	// series: "service/series id" → series, nil when the lookup failed.
	series map[string]*arr.Series
	// tmdb: show TMDB id → season list, nil when the lookup failed.
	tmdb map[int32]*metadata.TVSeasonList
}

func (s *Service) newSeasonEvaluator(ctx context.Context, reqs []gen.MediaRequest) (*seasonEvaluator, error) {
	ids := make([]int32, 0, len(reqs))
	seen := map[int32]bool{}
	for _, r := range reqs {
		if !seen[r.TmdbID] {
			seen[r.TmdbID] = true
			ids = append(ids, r.TmdbID)
		}
	}
	rows, err := s.db.ListOwnedEpisodeCountsByShowTMDB(ctx, gen.ListOwnedEpisodeCountsByShowTMDBParams{TmdbIds: ids})
	if err != nil {
		return nil, err
	}
	ev := &seasonEvaluator{
		s:        s,
		owned:    map[int32]map[int]int{},
		services: map[uuid.UUID]*arr.Client{},
		series:   map[string]*arr.Series{},
		tmdb:     map[int32]*metadata.TVSeasonList{},
	}
	for _, row := range rows {
		m := ev.owned[row.TmdbID]
		if m == nil {
			m = map[int]int{}
			ev.owned[row.TmdbID] = m
		}
		m[int(row.SeasonNumber)] = int(row.EpisodeCount)
	}
	return ev, nil
}

// progress judges one request. With the request linked to a Sonarr series
// (service + arr_item_id) Sonarr's per-season statistics decide — a season is
// complete when every aired, monitored episode has a file, and the library
// must hold at least one of its episodes (the files have been scanned in).
// Otherwise the library's episode count must reach TMDB's aired-episode
// count. An all-seasons request covers the regular seasons that have aired
// (per Sonarr, else per TMDB). ok=false when neither source could answer —
// the request is left as it is until the next pass.
func (ev *seasonEvaluator) progress(ctx context.Context, r gen.MediaRequest) (seasonProgress, bool) {
	requested := RequestSeasons(r)
	owned := ev.owned[r.TmdbID]
	if series := ev.sonarrSeries(ctx, r); series != nil {
		p := seasonProgress{covered: requested, complete: map[int]bool{}}
		if len(p.covered) == 0 {
			for _, se := range series.Seasons {
				if se.SeasonNumber > 0 && se.Aired() {
					p.covered = append(p.covered, se.SeasonNumber)
				}
			}
			sort.Ints(p.covered)
		}
		for _, n := range p.covered {
			if se, ok := series.Season(n); ok && se.Complete() && owned[n] > 0 {
				p.complete[n] = true
			}
		}
		return p, true
	}

	list := ev.tmdbSeasons(ctx, r.TmdbID)
	if list == nil {
		return seasonProgress{}, false
	}
	now := ev.s.now()
	p := seasonProgress{covered: requested, complete: map[int]bool{}}
	if len(p.covered) == 0 {
		p.covered = list.AiredSeasons(now)
	}
	for _, n := range p.covered {
		if aired := list.AiredEpisodes(n, now); aired > 0 && owned[n] >= aired {
			p.complete[n] = true
		}
	}
	return p, true
}

// sonarrSeries returns the Sonarr series the request is linked to, or nil
// when it isn't linked, the service is gone / disabled / not Sonarr, or the
// lookup failed (the caller then falls back to TMDB).
func (ev *seasonEvaluator) sonarrSeries(ctx context.Context, r gen.MediaRequest) *arr.Series {
	if !r.ServiceID.Valid || r.ArrItemID == nil || *r.ArrItemID <= 0 {
		return nil
	}
	svcID := uuid.UUID(r.ServiceID.Bytes)
	key := fmt.Sprintf("%s/%d", svcID, *r.ArrItemID)
	if series, ok := ev.series[key]; ok {
		return series
	}
	client := ev.client(ctx, svcID)
	if client == nil {
		return nil
	}
	series, err := client.SeriesByID(ctx, int(*r.ArrItemID))
	if err != nil {
		ev.s.logger.WarnContext(ctx, "reconcile: sonarr series lookup failed; judging seasons from the library and TMDB",
			"request_id", r.ID, "service_id", svcID, "series_id", *r.ArrItemID, "err", err)
		series = nil
		if !errors.Is(err, arr.ErrNotFound) {
			// Unreachable / bad key: don't wait on it again this pass.
			ev.services[svcID] = nil
		}
	}
	ev.series[key] = series
	return series
}

// client returns the arr client for a Sonarr service, nil when unusable.
func (ev *seasonEvaluator) client(ctx context.Context, id uuid.UUID) *arr.Client {
	if c, ok := ev.services[id]; ok {
		return c
	}
	var c *arr.Client
	svc, err := ev.s.db.GetArrService(ctx, id)
	switch {
	case err != nil:
		if !errors.Is(err, pgx.ErrNoRows) {
			ev.s.logger.WarnContext(ctx, "reconcile: arr service lookup failed", "service_id", id, "err", err)
		}
	case svc.Kind != "sonarr" || !svc.Enabled:
	default:
		apiKey, err := arrcrypt.Open(ev.s.enc, svc.ID, svc.ApiKey)
		if err != nil {
			ev.s.logger.WarnContext(ctx, "reconcile: open arr api key", "service_id", id, "err", err)
			break
		}
		c = ev.s.arrClient(svc.BaseUrl, apiKey)
	}
	ev.services[id] = c
	return c
}

// tmdbSeasons returns the show's TMDB season list, nil when unavailable.
func (ev *seasonEvaluator) tmdbSeasons(ctx context.Context, tmdbID int32) *metadata.TVSeasonList {
	if list, ok := ev.tmdb[tmdbID]; ok {
		return list
	}
	var list *metadata.TVSeasonList
	if ev.s.tmdb != nil {
		l, err := ev.s.tmdb.GetTVSeasons(ctx, int(tmdbID))
		if err != nil {
			ev.s.logger.WarnContext(ctx, "reconcile: tmdb season list", "tmdb_id", tmdbID, "err", err)
		} else {
			list = l
		}
	}
	ev.tmdb[tmdbID] = list
	return list
}
