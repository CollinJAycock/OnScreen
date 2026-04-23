package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/livetv"
)

// EPGConfig is the JSON payload for the epg_refresh task.
//
// Force=true skips the per-source refresh_interval_min gate and runs
// every enabled source regardless of last pull — useful for a one-shot
// "refresh everything now" admin action. Default behavior only
// refreshes sources whose last pull is older than their configured
// interval (or have never been pulled).
type EPGConfig struct {
	Force bool `json:"force"`
}

// EPGRefreshService is the slice of livetv.Service this handler uses.
// Kept narrow so tests can stub it.
type EPGRefreshService interface {
	ListEPGSources(ctx context.Context) ([]livetv.EPGSource, error)
	RefreshEPGSource(ctx context.Context, id uuid.UUID) (livetv.RefreshResult, error)
}

// EPGRefreshHandler iterates enabled EPG sources and refreshes any whose
// last_pull_at is older than their per-source refresh_interval_min.
// Intended to run on a short cron cadence (5-10 min) — the per-source
// interval is what actually governs refresh frequency; this handler
// just wakes up frequently enough to notice when a source is due.
type EPGRefreshHandler struct {
	svc EPGRefreshService
}

// NewEPGRefreshHandler wires the handler.
func NewEPGRefreshHandler(svc EPGRefreshService) *EPGRefreshHandler {
	return &EPGRefreshHandler{svc: svc}
}

// Run implements scheduler.Handler.
func (h *EPGRefreshHandler) Run(ctx context.Context, cfg json.RawMessage) (string, error) {
	var c EPGConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &c); err != nil {
			return "", fmt.Errorf("epg_refresh config: %w", err)
		}
	}

	sources, err := h.svc.ListEPGSources(ctx)
	if err != nil {
		return "", fmt.Errorf("list epg sources: %w", err)
	}

	now := time.Now()
	var (
		refreshed int
		skipped   int
		failed    int
		totalProg int
	)
	for _, s := range sources {
		if !s.Enabled {
			skipped++
			continue
		}
		// Gate: only refresh when due. Force bypasses this entirely.
		if !c.Force && s.LastPullAt != nil {
			due := s.LastPullAt.Add(time.Duration(s.RefreshIntervalMin) * time.Minute)
			if now.Before(due) {
				skipped++
				continue
			}
		}
		res, err := h.svc.RefreshEPGSource(ctx, s.ID)
		if err != nil {
			failed++
			continue
		}
		refreshed++
		totalProg += res.ProgramsIngested
	}

	return fmt.Sprintf("refreshed=%d skipped=%d failed=%d programs=%d",
		refreshed, skipped, failed, totalProg), nil
}
