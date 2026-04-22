package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// ScanConfig is the JSON payload for the scan_library task.
//
// LibraryID selects a specific library; "all" (or an empty string) runs
// scans for every library the server knows about.
type ScanConfig struct {
	LibraryID string `json:"library_id"`
}

// ScanEnqueuer is the slice of the main scanEnqueuer type that
// scan_library depends on. Kept narrow so tests can provide fakes.
type ScanEnqueuer interface {
	EnqueueScan(ctx context.Context, libraryID uuid.UUID) error
}

// LibraryLister enumerates libraries for the "scan all" variant.
type LibraryLister interface {
	ListLibraryIDs(ctx context.Context) ([]uuid.UUID, error)
}

// LibraryListerFunc adapts a function into a LibraryLister so callers can
// inline an adapter without declaring a type.
type LibraryListerFunc func(ctx context.Context) ([]uuid.UUID, error)

func (f LibraryListerFunc) ListLibraryIDs(ctx context.Context) ([]uuid.UUID, error) {
	return f(ctx)
}

// ScanHandler enqueues a library scan. The actual scan runs asynchronously
// in the scanner worker — this handler returns as soon as the scan is
// queued, which is what cron semantics want (otherwise a long-running
// scan would block subsequent ticks from even seeing the row).
type ScanHandler struct {
	enq    ScanEnqueuer
	lister LibraryLister
}

// NewScanHandler constructs a ScanHandler.
func NewScanHandler(enq ScanEnqueuer, lister LibraryLister) *ScanHandler {
	return &ScanHandler{enq: enq, lister: lister}
}

func (h *ScanHandler) Run(ctx context.Context, rawCfg json.RawMessage) (string, error) {
	var cfg ScanConfig
	if len(rawCfg) > 0 {
		if err := json.Unmarshal(rawCfg, &cfg); err != nil {
			return "", fmt.Errorf("parse config: %w", err)
		}
	}

	if cfg.LibraryID == "" || cfg.LibraryID == "all" {
		ids, err := h.lister.ListLibraryIDs(ctx)
		if err != nil {
			return "", fmt.Errorf("list libraries: %w", err)
		}
		enqueued := 0
		for _, id := range ids {
			if err := h.enq.EnqueueScan(ctx, id); err != nil {
				return fmt.Sprintf("enqueued %d of %d before error", enqueued, len(ids)),
					fmt.Errorf("enqueue %s: %w", id, err)
			}
			enqueued++
		}
		return fmt.Sprintf("enqueued scans for %d libraries", enqueued), nil
	}

	id, err := uuid.Parse(cfg.LibraryID)
	if err != nil {
		return "", fmt.Errorf("parse library_id %q: %w", cfg.LibraryID, err)
	}
	if err := h.enq.EnqueueScan(ctx, id); err != nil {
		return "", fmt.Errorf("enqueue scan: %w", err)
	}
	return fmt.Sprintf("enqueued scan for library %s", id), nil
}
