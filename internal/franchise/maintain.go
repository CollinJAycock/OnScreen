package franchise

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// Defaults and ceilings for one maintenance run. Every TMDB request the run
// makes is bounded by these: at most BackfillLimit movie-details lookups and
// at most FetchBudget collection fetches.
const (
	DefaultBackfillLimit = 200
	MaxBackfillLimit     = 1000
	DefaultRefreshLimit  = 25
	DefaultFetchBudget   = 100
	MaxFetchBudget       = 500
)

// MaintainOptions bounds one maintenance run. Zero values take the defaults.
type MaintainOptions struct {
	// BackfillLimit caps how many movies without a recorded TMDB collection
	// are looked up (one TMDB movie-details request each, usually served
	// from the disk cache the enricher already warmed).
	BackfillLimit int
	// RefreshLimit caps how many stale collection snapshots are re-fetched.
	RefreshLimit int
	// FetchBudget caps TMDB /collection requests across the whole run.
	FetchBudget int
}

func (o MaintainOptions) normalized() MaintainOptions {
	if o.BackfillLimit <= 0 {
		o.BackfillLimit = DefaultBackfillLimit
	}
	if o.BackfillLimit > MaxBackfillLimit {
		o.BackfillLimit = MaxBackfillLimit
	}
	if o.RefreshLimit <= 0 {
		o.RefreshLimit = DefaultRefreshLimit
	}
	if o.FetchBudget <= 0 {
		o.FetchBudget = DefaultFetchBudget
	}
	if o.FetchBudget > MaxFetchBudget {
		o.FetchBudget = MaxFetchBudget
	}
	return o
}

// MaintainResult summarises one maintenance run.
type MaintainResult struct {
	Checked      int   // movies looked up this run
	InCollection int   // … of which belong to a TMDB collection
	NotFound     int   // … whose TMDB id no longer exists (recorded as none)
	Failed       int   // … transient lookup failures (retried next run)
	Remaining    int64 // movies still awaiting a lookup (-1 = unknown)
	Refreshed    int   // stale snapshots queued for refresh
	Synced       int   // franchise collections reconciled
	SyncFailed   int
	// Aborted is set when TMDB was unavailable (no key, breaker open), so
	// the lookup phase stopped early; the DB-only reconcile still ran.
	Aborted bool
}

// String renders the result for the scheduler's run log.
func (r MaintainResult) String() string {
	s := fmt.Sprintf("checked=%d in_collection=%d not_found=%d failed=%d remaining=%d refreshed=%d synced=%d sync_failed=%d",
		r.Checked, r.InCollection, r.NotFound, r.Failed, r.Remaining, r.Refreshed, r.Synced, r.SyncFailed)
	if r.Aborted {
		s += " (tmdb unavailable; lookups stopped early)"
	}
	return s
}

// Maintain is the scheduled pass:
//
//  1. Backfill — movies matched before franchise tracking existed (or
//     re-matched since) get their TMDB collection looked up, a bounded batch
//     per run, oldest first. A 404 is recorded as "no collection" so the
//     movie leaves the queue; transient failures stay queued.
//  2. Refresh — a bounded batch of stale snapshots is re-fetched so parts
//     TMDB added since (announced sequels) show up.
//  3. Reconcile — every franchise row and every TMDB collection with >= 2
//     linked movies is re-synced (DB-only unless a snapshot is missing), which
//     removes collections whose movies were deleted and creates ones whose
//     second member arrived through a path that didn't sync.
func (s *Service) Maintain(ctx context.Context, opts MaintainOptions) (MaintainResult, error) {
	opts = opts.normalized()
	res := MaintainResult{Remaining: -1}
	b := &budget{remaining: opts.FetchBudget}

	rows, err := s.db.ListMoviesNeedingTMDBCollectionCheck(ctx, int32(opts.BackfillLimit))
	if err != nil {
		return res, fmt.Errorf("franchise: list backfill queue: %w", err)
	}
	if len(rows) > 0 {
		t := s.tmdb()
		if t == nil {
			res.Aborted = true
		} else {
			s.backfill(ctx, t, rows, b, &res)
		}
	}

	if !res.Aborted && ctx.Err() == nil {
		stale, err := s.db.ListStaleFranchiseSnapshots(ctx, gen.ListStaleFranchiseSnapshotsParams{
			OlderThan: pgtype.Timestamptz{Time: s.now().Add(-SnapshotTTL), Valid: true},
			Lim:       int32(opts.RefreshLimit),
		})
		if err != nil {
			s.logger.WarnContext(ctx, "franchise: list stale snapshots", "err", err)
		}
		for _, cid := range stale {
			if ctx.Err() != nil {
				break
			}
			if err := s.sync(ctx, cid, fetchIfStale, nil, b); err != nil {
				s.logger.WarnContext(ctx, "franchise: refresh snapshot", "tmdb_collection_id", cid, "err", err)
				continue
			}
			res.Refreshed++
		}
	}

	if ctx.Err() == nil {
		cands, err := s.db.ListFranchiseSyncCandidates(ctx)
		if err != nil {
			return res, fmt.Errorf("franchise: list sync candidates: %w", err)
		}
		policy := fetchIfMissing
		if res.Aborted {
			policy = fetchNever
		}
		for _, cid := range cands {
			if ctx.Err() != nil {
				break
			}
			if err := s.sync(ctx, cid, policy, nil, b); err != nil {
				s.logger.WarnContext(ctx, "franchise: reconcile", "tmdb_collection_id", cid, "err", err)
				res.SyncFailed++
				continue
			}
			res.Synced++
		}
	}

	if n, err := s.db.CountMoviesNeedingTMDBCollectionCheck(ctx); err == nil {
		res.Remaining = n
	}
	return res, nil
}

func (s *Service) backfill(ctx context.Context, t TMDB, rows []gen.ListMoviesNeedingTMDBCollectionCheckRow, b *budget, res *MaintainResult) {
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		if row.TmdbID == nil || *row.TmdbID <= 0 {
			continue
		}
		movieID := int(*row.TmdbID)
		ref, err := t.MovieCollection(ctx, movieID)
		switch {
		case err == nil:
		case errors.Is(err, ErrNotFound):
			// Recorded as "no collection" so a dead TMDB id doesn't sit at
			// the head of the queue forever; a re-match gives it a new id
			// and re-queues it.
			ref = nil
			res.NotFound++
		case errors.Is(err, ErrUnavailable):
			res.Aborted = true
			return
		default:
			s.logger.WarnContext(ctx, "franchise: movie collection lookup failed",
				"item_id", row.ID, "tmdb_id", movieID, "err", err)
			res.Failed++
			continue
		}
		res.Checked++
		if ref != nil {
			res.InCollection++
		}
		if err := s.recordMovie(ctx, row.ID, movieID, ref, b); err != nil {
			s.logger.WarnContext(ctx, "franchise: record movie", "item_id", row.ID, "err", err)
		}
	}
}
