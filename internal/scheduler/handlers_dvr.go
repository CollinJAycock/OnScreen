package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
)

// DVRMatcherService is the slice of livetv.DVRService the scheduled
// matcher handler uses. Runs every minute, scans enabled schedules
// against the upcoming EPG window, and upserts scheduled recordings.
type DVRMatcherService interface {
	Match(ctx context.Context) (matched int, conflicts int, err error)
}

// DVRMatcherHandler wraps DVRMatcherService as a scheduler.Handler.
type DVRMatcherHandler struct {
	svc DVRMatcherService
}

// NewDVRMatcherHandler wires the handler.
func NewDVRMatcherHandler(svc DVRMatcherService) *DVRMatcherHandler {
	return &DVRMatcherHandler{svc: svc}
}

// Run implements scheduler.Handler. Config is ignored.
func (h *DVRMatcherHandler) Run(ctx context.Context, _ json.RawMessage) (string, error) {
	matched, conflicts, err := h.svc.Match(ctx)
	if err != nil {
		return "", fmt.Errorf("dvr match: %w", err)
	}
	return fmt.Sprintf("matched=%d conflicts=%d", matched, conflicts), nil
}
