package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
)

// DVRRetentionService is the slice of livetv.DVRService the retention
// purge handler uses. Daily sweep deletes recordings past their
// schedule's retention_days window and removes the underlying files
// from disk.
type DVRRetentionService interface {
	PurgeExpiredRecordings(ctx context.Context) (int, error)
}

// DVRRetentionHandler wraps DVRRetentionService as a scheduler.Handler.
type DVRRetentionHandler struct {
	svc DVRRetentionService
}

// NewDVRRetentionHandler wires the handler.
func NewDVRRetentionHandler(svc DVRRetentionService) *DVRRetentionHandler {
	return &DVRRetentionHandler{svc: svc}
}

// Run implements scheduler.Handler. Config is ignored — retention is
// a schedule-level setting, not a task-level one.
func (h *DVRRetentionHandler) Run(ctx context.Context, _ json.RawMessage) (string, error) {
	purged, err := h.svc.PurgeExpiredRecordings(ctx)
	if err != nil {
		return "", fmt.Errorf("dvr retention purge: %w", err)
	}
	return fmt.Sprintf("purged=%d", purged), nil
}
