package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/observability"
)

// HubRefreshWorker periodically refreshes the hub_recently_added materialized view.
// Default interval is 5 minutes; adjust via NewHubRefreshWorker.
type HubRefreshWorker struct {
	pool     *pgxpool.Pool
	interval time.Duration
	logger   *slog.Logger
	metrics  *observability.Metrics
}

// NewHubRefreshWorker creates a HubRefreshWorker with the given refresh interval.
func NewHubRefreshWorker(pool *pgxpool.Pool, interval time.Duration, logger *slog.Logger) *HubRefreshWorker {
	return &HubRefreshWorker{pool: pool, interval: interval, logger: logger}
}

// WithMetrics enables Prometheus instrumentation (refresh duration). nil no-op.
func (w *HubRefreshWorker) WithMetrics(m *observability.Metrics) *HubRefreshWorker {
	w.metrics = m
	return w
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
	start := time.Now()
	q := gen.New(w.pool)
	if err := q.RefreshHubRecentlyAdded(ctx); err != nil {
		w.logger.WarnContext(ctx, "hub_recently_added refresh failed", "err", err)
		return
	}
	if w.metrics != nil {
		w.metrics.HubRefreshDuration.WithLabelValues("recently_added").Observe(time.Since(start).Seconds())
	}
	w.logger.DebugContext(ctx, "hub_recently_added refreshed")
}
