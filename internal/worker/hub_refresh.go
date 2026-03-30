package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// HubRefreshWorker periodically refreshes the hub_recently_added materialized view.
// Default interval is 5 minutes; adjust via NewHubRefreshWorker.
type HubRefreshWorker struct {
	pool     *pgxpool.Pool
	interval time.Duration
	logger   *slog.Logger
}

// NewHubRefreshWorker creates a HubRefreshWorker with the given refresh interval.
func NewHubRefreshWorker(pool *pgxpool.Pool, interval time.Duration, logger *slog.Logger) *HubRefreshWorker {
	return &HubRefreshWorker{pool: pool, interval: interval, logger: logger}
}

// Run blocks until ctx is cancelled, refreshing the view on each tick.
func (w *HubRefreshWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Refresh immediately on startup.
	w.refresh(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.refresh(ctx)
		}
	}
}

func (w *HubRefreshWorker) refresh(ctx context.Context) {
	q := gen.New(w.pool)
	if err := q.RefreshHubRecentlyAdded(ctx); err != nil {
		w.logger.WarnContext(ctx, "hub_recently_added refresh failed", "err", err)
		return
	}
	w.logger.DebugContext(ctx, "hub_recently_added refreshed")
}
