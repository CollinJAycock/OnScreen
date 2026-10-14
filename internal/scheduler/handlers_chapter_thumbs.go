package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// ChapterThumbSource is what the chapter_thumbnails task needs: items whose
// seek-bar sprites predate chapter thumbnails, and a generator that adds the
// thumbnails without regenerating the sprites. Satisfied by
// *trickplay.Service.
type ChapterThumbSource interface {
	ChapterThumbCandidates(ctx context.Context, limit int) ([]uuid.UUID, error)
	BackfillChapterThumbs(ctx context.Context, itemID uuid.UUID) (int, error)
}

// ChapterThumbsConfig is the optional JSON payload for chapter_thumbnails.
type ChapterThumbsConfig struct {
	// Limit caps how many items one run lists.
	Limit int `json:"limit"`
	// BudgetMinutes bounds a run's wall clock; an item already started
	// finishes.
	BudgetMinutes int `json:"budget_minutes"`
}

const (
	defaultChapterThumbsLimit  = 200
	maxChapterThumbsLimit      = 5000
	defaultChapterThumbsBudget = 30
	maxChapterThumbsBudget     = 6 * 60
)

// ChapterThumbsHandler backfills chapter thumbnails for items whose sprites
// were generated before chapter thumbnails existed. New and regenerated
// sprites get them in the same run, so this only works through the back
// catalogue — oldest first, within a time budget. Opt-in (seeded disabled):
// nothing is regenerated at boot.
type ChapterThumbsHandler struct {
	src    ChapterThumbSource
	logger *slog.Logger
	now    func() time.Time
}

// NewChapterThumbsHandler constructs the handler.
func NewChapterThumbsHandler(src ChapterThumbSource, logger *slog.Logger) *ChapterThumbsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ChapterThumbsHandler{src: src, logger: logger, now: time.Now}
}

// Run is the scheduler entry point.
func (h *ChapterThumbsHandler) Run(ctx context.Context, rawCfg json.RawMessage) (string, error) {
	cfg := ChapterThumbsConfig{}
	if len(rawCfg) > 0 {
		if err := json.Unmarshal(rawCfg, &cfg); err != nil {
			return "", fmt.Errorf("parse config: %w", err)
		}
	}
	if cfg.Limit <= 0 {
		cfg.Limit = defaultChapterThumbsLimit
	}
	cfg.Limit = min(cfg.Limit, maxChapterThumbsLimit)
	if cfg.BudgetMinutes <= 0 {
		cfg.BudgetMinutes = defaultChapterThumbsBudget
	}
	cfg.BudgetMinutes = min(cfg.BudgetMinutes, maxChapterThumbsBudget)
	deadline := h.now().Add(time.Duration(cfg.BudgetMinutes) * time.Minute)

	items, err := h.src.ChapterThumbCandidates(ctx, cfg.Limit)
	if err != nil {
		return "", fmt.Errorf("list chapter thumbnail candidates: %w", err)
	}
	if len(items) == 0 {
		return "no items awaiting chapter thumbnails", nil
	}
	var done, thumbs, failed int
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
		n, err := h.src.BackfillChapterThumbs(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				stopped = "shutdown"
				break
			}
			h.logger.WarnContext(ctx, "chapter_thumbnails: generate failed", "item_id", id, "err", err)
			failed++
			continue
		}
		done++
		thumbs += n
	}
	msg := fmt.Sprintf("items=%d thumbnails=%d failed=%d", done, thumbs, failed)
	if stopped != "" {
		msg += " stopped=" + stopped
	}
	return msg, nil
}
