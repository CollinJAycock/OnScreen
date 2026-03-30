package worker

import (
	"context"
	"log/slog"
	"time"
)

// GracePeriodProvider returns the current missing file grace period
// (hot-reloadable via SIGHUP, ADR-027).
type GracePeriodProvider interface {
	MissingFileGracePeriod() time.Duration
}

// MissingFilesWorker promotes missing → deleted for files that have exceeded
// the grace period, then soft-deletes orphaned media_items (ADR-011).
type MissingFilesWorker struct {
	mediaSvc MissingFilesService
	grace    GracePeriodProvider
	interval time.Duration
	logger   *slog.Logger
}

// MissingFilesService is the domain interface this worker uses.
type MissingFilesService interface {
	PromoteExpiredMissing(ctx context.Context, gracePeriod time.Duration) (int, error)
}

// NewMissingFilesWorker creates the worker. interval is how often it runs.
func NewMissingFilesWorker(svc MissingFilesService, grace GracePeriodProvider,
	interval time.Duration, logger *slog.Logger) *MissingFilesWorker {
	return &MissingFilesWorker{
		mediaSvc: svc,
		grace:    grace,
		interval: interval,
		logger:   logger,
	}
}

// Run ticks at the configured interval until ctx is cancelled.
func (w *MissingFilesWorker) Run(ctx context.Context) {
	// Run once immediately.
	w.tick(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *MissingFilesWorker) tick(ctx context.Context) {
	grace := w.grace.MissingFileGracePeriod()
	count, err := w.mediaSvc.PromoteExpiredMissing(ctx, grace)
	if err != nil {
		w.logger.ErrorContext(ctx, "missing files worker: promote failed", "err", err)
		return
	}
	if count > 0 {
		w.logger.InfoContext(ctx, "missing files worker: promoted to deleted",
			"count", count, "grace_period", grace)
	}
}
