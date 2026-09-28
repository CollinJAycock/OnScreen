package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/onscreen/onscreen/internal/franchise"
)

// FranchiseMaintainer is the franchise.Service surface the task drives.
type FranchiseMaintainer interface {
	Maintain(ctx context.Context, opts franchise.MaintainOptions) (franchise.MaintainResult, error)
}

// FranchiseCollectionsConfig is the optional JSON payload for the
// franchise_collections task. Zero values take the franchise package
// defaults; values above the package ceilings are clamped.
type FranchiseCollectionsConfig struct {
	// BackfillLimit: movies looked up per run (one TMDB movie-details
	// request each, usually a disk-cache hit).
	BackfillLimit int `json:"backfill_limit"`
	// RefreshLimit: stale collection snapshots re-fetched per run.
	RefreshLimit int `json:"refresh_limit"`
	// FetchBudget: TMDB /collection requests allowed per run.
	FetchBudget int `json:"fetch_budget"`
}

// FranchiseCollectionsHandler runs the nightly franchise-collection pass:
// a bounded TMDB-collection backfill for movies enriched before franchise
// tracking existed, a bounded refresh of stale collection snapshots, and a
// DB-only reconcile that creates/removes franchise collections as their
// member counts cross the two-movie threshold (deleted movies included).
type FranchiseCollectionsHandler struct {
	svc    FranchiseMaintainer
	logger *slog.Logger
}

// NewFranchiseCollectionsHandler constructs the handler.
func NewFranchiseCollectionsHandler(svc FranchiseMaintainer, logger *slog.Logger) *FranchiseCollectionsHandler {
	return &FranchiseCollectionsHandler{svc: svc, logger: logger}
}

// Run is the scheduler entry point.
func (h *FranchiseCollectionsHandler) Run(ctx context.Context, rawCfg json.RawMessage) (string, error) {
	var cfg FranchiseCollectionsConfig
	if len(rawCfg) > 0 && string(rawCfg) != "null" {
		if err := json.Unmarshal(rawCfg, &cfg); err != nil {
			return "", fmt.Errorf("parse config: %w", err)
		}
	}
	res, err := h.svc.Maintain(ctx, franchise.MaintainOptions{
		BackfillLimit: cfg.BackfillLimit,
		RefreshLimit:  cfg.RefreshLimit,
		FetchBudget:   cfg.FetchBudget,
	})
	if err != nil {
		return "", err
	}
	return res.String(), nil
}
