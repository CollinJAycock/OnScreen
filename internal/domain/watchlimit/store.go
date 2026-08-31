package watchlimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxTickSeconds clamps the per-heartbeat usage delta. Progress heartbeats
// arrive roughly every 10s; capping the added delta at 30s means a paused or
// backgrounded gap (or a string of missed heartbeats) contributes at most 30s
// instead of the full wall-clock gap — so the daily counter tracks active
// watching, not elapsed time since the player was opened.
const maxTickSeconds = 30

// Store is the Postgres-backed policy + daily-usage source for parental watch
// limits. Raw SQL over the read-write pool, mirroring scrobble.Store — the two
// tables are addressed directly rather than through sqlc since the access
// patterns (an upsert with a clamped delta) are simpler to express inline.
type Store struct {
	db *pgxpool.Pool
}

// NewStore builds a Store over the read-write pool.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// effectiveOwnerSQL resolves the id whose watch-limit policy and usage budget
// apply to a given user: the user itself when it has a policy row of its own,
// otherwise its parent when it is a managed profile, otherwise itself.
//
// Creating a managed profile is open to every authenticated user, and the
// INSERT copies max_content_rating but NOT the watch limit — which lives in a
// separate table with no row for the new profile. Every gate short-circuits on
// an unrestricted policy, so a child with a 60-minute cap could create a
// second profile, PIN-switch into it, and watch without limit. Nothing
// cascades: there are no triggers in the schema.
//
// Resolving here rather than copying at INSERT keeps the child correct when
// the parent's policy changes later, and — because usage keys off the same
// expression — makes the household share ONE budget instead of one per
// profile. An admin who wants a child on its own budget sets a policy on the
// child, which gives it a row and makes it its own effective owner. Mirrors
// the effective-owner CASE the library ACL already uses.
const effectiveOwnerSQL = `
	SELECT CASE
	    WHEN EXISTS (SELECT 1 FROM user_watch_limit wl WHERE wl.user_id = u.id) THEN u.id
	    WHEN u.parent_user_id IS NOT NULL THEN u.parent_user_id
	    ELSE u.id
	END
	FROM users u WHERE u.id = `

// GetPolicy returns the user's policy. A missing row is the unrestricted zero
// value, not an error.
func (s *Store) GetPolicy(ctx context.Context, userID uuid.UUID) (Policy, error) {
	var p Policy
	err := s.db.QueryRow(ctx,
		`SELECT daily_limit_minutes, allowed_start_minute, allowed_end_minute
		   FROM user_watch_limit WHERE user_id = (`+effectiveOwnerSQL+`$1)`, userID).
		Scan(&p.DailyLimitMinutes, &p.AllowedStartMinute, &p.AllowedEndMinute)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Policy{}, nil
		}
		return Policy{}, fmt.Errorf("watchlimit: get policy: %w", err)
	}
	return p, nil
}

// SetPolicy upserts the user's policy. An all-nil policy leaves the row present
// but imposes nothing (equivalent to unrestricted) — the admin API treats that
// as "clear all limits".
func (s *Store) SetPolicy(ctx context.Context, userID uuid.UUID, p Policy) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO user_watch_limit
		    (user_id, daily_limit_minutes, allowed_start_minute, allowed_end_minute, updated_at)
		 VALUES ($1, $2, $3, $4, now())
		 ON CONFLICT (user_id) DO UPDATE
		    SET daily_limit_minutes  = EXCLUDED.daily_limit_minutes,
		        allowed_start_minute = EXCLUDED.allowed_start_minute,
		        allowed_end_minute   = EXCLUDED.allowed_end_minute,
		        updated_at           = now()`,
		userID, p.DailyLimitMinutes, p.AllowedStartMinute, p.AllowedEndMinute)
	if err != nil {
		return fmt.Errorf("watchlimit: set policy: %w", err)
	}
	return nil
}

// TodayUsageSeconds returns the active watch seconds accumulated for the given
// local day. A missing row is 0.
func (s *Store) TodayUsageSeconds(ctx context.Context, userID uuid.UUID, day time.Time) (int, error) {
	var sec int
	// Same effective owner as GetPolicy, so a household shares one daily
	// budget and a user cannot mint a fresh allowance per profile.
	err := s.db.QueryRow(ctx,
		`SELECT watched_seconds FROM user_watch_usage
		  WHERE user_id = (`+effectiveOwnerSQL+`$1) AND day = $2`,
		userID, day).Scan(&sec)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("watchlimit: today usage: %w", err)
	}
	return sec, nil
}

// AddTick advances the day's accumulator by the clamped wall-clock delta since
// the previous tick and returns the new total. The first tick of a day just
// establishes last_tick_at (adds 0); each subsequent tick adds up to
// maxTickSeconds. The whole add is a single atomic upsert, so concurrent ticks
// for the same user can't lose an increment.
func (s *Store) AddTick(ctx context.Context, userID uuid.UUID, day, now time.Time) (int, error) {
	var sec int
	err := s.db.QueryRow(ctx,
		`INSERT INTO user_watch_usage (user_id, day, watched_seconds, last_tick_at)
		 VALUES ($1, $2, 0, $3)
		 ON CONFLICT (user_id, day) DO UPDATE
		    SET watched_seconds = user_watch_usage.watched_seconds
		          + LEAST(GREATEST(EXTRACT(EPOCH FROM ($3::timestamptz - user_watch_usage.last_tick_at))::int, 0), $4),
		        last_tick_at = $3
		 RETURNING watched_seconds`,
		userID, day, now, maxTickSeconds).Scan(&sec)
	if err != nil {
		return 0, fmt.Errorf("watchlimit: add tick: %w", err)
	}
	return sec, nil
}
