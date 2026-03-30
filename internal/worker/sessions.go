package worker

import (
	"context"
	"log/slog"
	"time"
)

// SessionCleanupService deletes expired sessions from the sessions table.
type SessionCleanupService interface {
	DeleteExpiredSessions(ctx context.Context) error
}

// SessionCleanupWorker periodically purges expired refresh token sessions.
type SessionCleanupWorker struct {
	svc      SessionCleanupService
	interval time.Duration
	logger   *slog.Logger
}

// NewSessionCleanupWorker creates the worker.
func NewSessionCleanupWorker(svc SessionCleanupService, interval time.Duration, logger *slog.Logger) *SessionCleanupWorker {
	return &SessionCleanupWorker{svc: svc, interval: interval, logger: logger}
}

// Run ticks at the configured interval until ctx is cancelled.
func (w *SessionCleanupWorker) Run(ctx context.Context) {
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

func (w *SessionCleanupWorker) tick(ctx context.Context) {
	if err := w.svc.DeleteExpiredSessions(ctx); err != nil {
		w.logger.WarnContext(ctx, "session cleanup failed", "err", err)
	}
}
