package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// watchPlaysRefresher is the minimal DB surface the refresh handler needs:
// the change probe (a sqlc query) and the REFRESH itself. *pgxpool.Pool
// satisfies it.
type watchPlaysRefresher interface {
	gen.DBTX
}

// watchPlaysSource is what the matview was last rebuilt from (see
// WatchPlaysSourceFingerprint).
type watchPlaysSource struct {
	events int64
	latest time.Time
}

// NewRefreshWatchPlaysHandler returns a scheduler handler that recomputes the
// watch_plays materialized view off the request path. watch_plays applies a
// lead() window over all stop/scrobble history (a 30-min per-(user,media)
// dedupe); the analytics dashboard reads it 5× per load and auto-refreshes
// every 30s, so as a plain view it recomputed that window constantly. As a
// materialized view it's rebuilt here on a cron instead.
//
// REFRESH ... CONCURRENTLY keeps the dashboard's reads unblocked during the
// rebuild (requires the unique index from migration 00004, and that the view is
// already populated — CREATE MATERIALIZED VIEW populates it).
//
// The rebuild re-runs that window and diffs the whole view even when nothing
// was watched, which on an idle server is every run. So each run first reads
// the view's source fingerprint (terminal-event count + newest timestamp, one
// index-only pass) and skips the rebuild when it matches the one taken before
// the last successful refresh. The fingerprint is held in memory: after a
// restart the first run always refreshes, and a probe error refreshes too, so
// the skip can only ever save work, never leave the view behind.
func NewRefreshWatchPlaysHandler(db watchPlaysRefresher, logger *slog.Logger) HandlerFunc {
	q := gen.New(db)
	var (
		mu   sync.Mutex
		last *watchPlaysSource
	)
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		// Taken BEFORE the refresh: an event landing between the probe and
		// the REFRESH makes the stored value older than the view, so the next
		// run refreshes again (redundant, never stale).
		var cur *watchPlaysSource
		if fp, err := q.WatchPlaysSourceFingerprint(ctx); err != nil {
			if logger != nil {
				logger.WarnContext(ctx, "watch_plays change probe failed; refreshing anyway", "err", err)
			}
		} else {
			cur = &watchPlaysSource{events: fp.TerminalEvents, latest: fp.LatestAt.Time}
			mu.Lock()
			unchanged := last != nil && last.events == cur.events && last.latest.Equal(cur.latest)
			mu.Unlock()
			if unchanged {
				return "watch_plays unchanged since the last refresh; skipped", nil
			}
		}
		if _, err := db.Exec(ctx, "REFRESH MATERIALIZED VIEW CONCURRENTLY public.watch_plays"); err != nil {
			return "", fmt.Errorf("refresh watch_plays: %w", err)
		}
		mu.Lock()
		last = cur
		mu.Unlock()
		return "watch_plays refreshed", nil
	}
}
