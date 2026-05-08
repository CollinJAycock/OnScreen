package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// MissingArtLister returns top-level items (movies + shows) with no
// poster, capped to limit. The maintenance package owns the
// ListMediaItemsMissingArt SQL query; we lean on the media service
// surface here so handlers don't drag in db/gen.
type MissingArtLister interface {
	ListItemsMissingArt(ctx context.Context, limit int32) ([]media.Item, error)
}

// MissingArtEnricher is the EnrichItem half of the enricher surface.
// Mirrors the v1 package's ItemEnricher but kept local so the
// scheduler doesn't import the API layer.
type MissingArtEnricher interface {
	EnrichItem(ctx context.Context, itemID uuid.UUID) error
}

// RefreshArtConfig is the optional JSON payload for the
// refresh_missing_art task. Limit caps how many items the run will
// touch; defaults to 200 when unset, matches the maintenance
// endpoint's default. Operator can crank it up via the Tasks UI for
// a one-off catch-up sweep on a freshly imported library.
type RefreshArtConfig struct {
	Limit int32 `json:"limit"`
}

// RefreshMissingArtHandler runs the same enrichment loop as the
// admin POST /api/v1/maintenance/refresh-missing-art endpoint, but
// on a cron — so a partially-enriched library self-heals without
// the operator hitting a button. The endpoint itself stays for
// manual "do this NOW" triggers; this handler is the safety net
// so users don't have to know it exists.
type RefreshMissingArtHandler struct {
	media    MissingArtLister
	enricher MissingArtEnricher
	logger   *slog.Logger
}

// NewRefreshMissingArtHandler constructs the handler.
func NewRefreshMissingArtHandler(media MissingArtLister, enricher MissingArtEnricher, logger *slog.Logger) *RefreshMissingArtHandler {
	return &RefreshMissingArtHandler{media: media, enricher: enricher, logger: logger}
}

// Run is the scheduler entry point.
func (h *RefreshMissingArtHandler) Run(ctx context.Context, rawCfg json.RawMessage) (string, error) {
	cfg := RefreshArtConfig{Limit: 200}
	if len(rawCfg) > 0 {
		if err := json.Unmarshal(rawCfg, &cfg); err != nil {
			return "", fmt.Errorf("parse config: %w", err)
		}
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 200
	}
	if cfg.Limit > 1000 {
		cfg.Limit = 1000
	}

	items, err := h.media.ListItemsMissingArt(ctx, cfg.Limit)
	if err != nil {
		return "", fmt.Errorf("list items missing art: %w", err)
	}
	if len(items) == 0 {
		return "no items missing art", nil
	}

	var refreshed, failed int
	for _, it := range items {
		if err := h.enricher.EnrichItem(ctx, it.ID); err != nil {
			h.logger.WarnContext(ctx, "refresh_missing_art: item failed",
				"item_id", it.ID, "title", it.Title, "err", err)
			failed++
			continue
		}
		refreshed++
	}

	return fmt.Sprintf("candidates=%d refreshed=%d failed=%d", len(items), refreshed, failed), nil
}
