package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgconn"
)

// watchPlaysRefresher is the minimal DB surface the refresh handler needs.
// *pgxpool.Pool satisfies it.
type watchPlaysRefresher interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
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
func NewRefreshWatchPlaysHandler(db watchPlaysRefresher, logger *slog.Logger) HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		if _, err := db.Exec(ctx, "REFRESH MATERIALIZED VIEW CONCURRENTLY public.watch_plays"); err != nil {
			return "", fmt.Errorf("refresh watch_plays: %w", err)
		}
		return "watch_plays refreshed", nil
	}
}
