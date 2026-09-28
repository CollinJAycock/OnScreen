package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/trickplay"
)

// TrickplayBackfillSource is the planner surface the backfill task needs:
// the oldest-first work list across trickplay-enabled libraries, a progress
// count, and a synchronous generate that shares the post-scan worker's
// single generation slot (so the nightly run never doubles up ffmpeg jobs
// with scan-triggered generation). Satisfied by *trickplay.Planner.
type TrickplayBackfillSource interface {
	ListBackfillCandidates(ctx context.Context, limit int) ([]uuid.UUID, error)
	CountBackfillCandidates(ctx context.Context) (int64, error)
	Process(ctx context.Context, itemID uuid.UUID, startBy time.Time) (trickplay.Outcome, error)
}

// TrickplayBackfillConfig is the optional JSON payload for the
// trickplay_backfill task.
type TrickplayBackfillConfig struct {
	// Limit caps how many items one run lists (and so can process).
	Limit int `json:"limit"`
	// BudgetMinutes bounds a run's wall clock: no new item starts after the
	// budget is spent, and an item already generating finishes. A night's
	// run therefore overruns by at most one item's generation time.
	BudgetMinutes int `json:"budget_minutes"`
}

const (
	defaultTrickplayBackfillLimit  = 500
	maxTrickplayBackfillLimit      = 5000
	defaultTrickplayBackfillBudget = 45
	maxTrickplayBackfillBudget     = 6 * 60
)

// TrickplayBackfillHandler works through the back catalogue of
// trickplay-enabled libraries: items that have never had seek-bar sprites
// generated, oldest first, within a time budget per run. New items are
// covered sooner by the post-scan queue; this task catches everything that
// predates the feature (or the library's toggle) and anything a restart
// dropped from the in-memory queue. Failed items are not retried — a file
// ffmpeg can't read would otherwise burn the budget every night.
//
// Generation waits while a transcode session is active (the queue's busy
// check), so a night-owl viewer's stream never competes with sprite
// extraction; if the box stays busy past the budget the run just ends and
// the next night continues.
type TrickplayBackfillHandler struct {
	src    TrickplayBackfillSource
	logger *slog.Logger
	now    func() time.Time
}

// NewTrickplayBackfillHandler constructs the handler.
func NewTrickplayBackfillHandler(src TrickplayBackfillSource, logger *slog.Logger) *TrickplayBackfillHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &TrickplayBackfillHandler{src: src, logger: logger, now: time.Now}
}

// Run is the scheduler entry point.
func (h *TrickplayBackfillHandler) Run(ctx context.Context, rawCfg json.RawMessage) (string, error) {
	cfg := TrickplayBackfillConfig{}
	if len(rawCfg) > 0 {
		if err := json.Unmarshal(rawCfg, &cfg); err != nil {
			return "", fmt.Errorf("parse config: %w", err)
		}
	}
	if cfg.Limit <= 0 {
		cfg.Limit = defaultTrickplayBackfillLimit
	}
	if cfg.Limit > maxTrickplayBackfillLimit {
		cfg.Limit = maxTrickplayBackfillLimit
	}
	if cfg.BudgetMinutes <= 0 {
		cfg.BudgetMinutes = defaultTrickplayBackfillBudget
	}
	if cfg.BudgetMinutes > maxTrickplayBackfillBudget {
		cfg.BudgetMinutes = maxTrickplayBackfillBudget
	}
	deadline := h.now().Add(time.Duration(cfg.BudgetMinutes) * time.Minute)

	items, err := h.src.ListBackfillCandidates(ctx, cfg.Limit)
	if err != nil {
		return "", fmt.Errorf("list trickplay candidates: %w", err)
	}
	if len(items) == 0 {
		return "no items awaiting trickplay", nil
	}

	var done, failed, skipped, dup int
	stopped := ""
	for _, id := range items {
		if ctx.Err() != nil {
			stopped = "shutdown"
			break
		}
		if !h.now().Before(deadline) {
			stopped = "budget"
			break
		}
		out, err := h.src.Process(ctx, id, deadline)
		if err != nil {
			if errors.Is(err, trickplay.ErrNotStarted) {
				// Couldn't get the slot / an idle moment before the budget
				// ran out — typically a transcode that outlasted it.
				stopped = "busy"
				break
			}
			if ctx.Err() != nil {
				stopped = "shutdown"
				break
			}
			h.logger.WarnContext(ctx, "trickplay_backfill: generate failed", "item_id", id, "err", err)
			failed++
			continue
		}
		switch out {
		case trickplay.OutcomeDone:
			done++
		case trickplay.OutcomeSkipped:
			skipped++
		case trickplay.OutcomeDuplicate:
			dup++
		default:
			failed++
		}
	}

	remaining, err := h.src.CountBackfillCandidates(context.WithoutCancel(ctx))
	if err != nil {
		// Progress count is best-effort garnish; the run itself succeeded.
		remaining = -1
	}
	msg := fmt.Sprintf("generated=%d failed=%d skipped=%d already_queued=%d remaining=%d",
		done, failed, skipped, dup, remaining)
	if stopped != "" {
		msg += " stopped=" + stopped
	}
	return msg, nil
}
