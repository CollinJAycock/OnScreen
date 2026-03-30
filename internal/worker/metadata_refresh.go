package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// MetadataRefreshService is the domain interface this worker needs.
type MetadataRefreshService interface {
	ListLibrariesDueForMetadataRefresh(ctx context.Context) ([]RefreshableLibrary, error)
	MarkLibraryMetadataRefreshed(ctx context.Context, libraryID uuid.UUID) error
}

// RefreshableLibrary is the minimal library info the worker needs.
type RefreshableLibrary struct {
	ID   uuid.UUID
	Name string
}

// MetadataEnricher runs the metadata agent for a library.
type MetadataEnricher interface {
	RefreshLibrary(ctx context.Context, libraryID uuid.UUID) error
}

// MetadataRefreshWorker periodically checks for libraries whose metadata
// refresh interval has elapsed and triggers a refresh (ADR-010).
type MetadataRefreshWorker struct {
	svc      MetadataRefreshService
	enricher MetadataEnricher
	interval time.Duration
	logger   *slog.Logger
}

// NewMetadataRefreshWorker creates the worker. interval is the check frequency
// (not the per-library refresh interval — that's stored in the library record).
func NewMetadataRefreshWorker(svc MetadataRefreshService, enricher MetadataEnricher,
	interval time.Duration, logger *slog.Logger) *MetadataRefreshWorker {
	return &MetadataRefreshWorker{
		svc:      svc,
		enricher: enricher,
		interval: interval,
		logger:   logger,
	}
}

// Run ticks at the configured interval until ctx is cancelled.
func (w *MetadataRefreshWorker) Run(ctx context.Context) {
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

func (w *MetadataRefreshWorker) tick(ctx context.Context) {
	libs, err := w.svc.ListLibrariesDueForMetadataRefresh(ctx)
	if err != nil {
		w.logger.ErrorContext(ctx, "metadata refresh worker: list libraries failed", "err", err)
		return
	}

	for _, lib := range libs {
		lib := lib
		w.logger.InfoContext(ctx, "refreshing library metadata",
			"library_id", lib.ID, "name", lib.Name)

		if err := w.enricher.RefreshLibrary(ctx, lib.ID); err != nil {
			w.logger.WarnContext(ctx, "metadata refresh failed",
				"library_id", lib.ID, "err", err)
			continue
		}

		if err := w.svc.MarkLibraryMetadataRefreshed(ctx, lib.ID); err != nil {
			w.logger.WarnContext(ctx, "failed to mark metadata refreshed",
				"library_id", lib.ID, "err", err)
		}
	}
}
