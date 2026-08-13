package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

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

// ArtVerifierStore is the slice of the media service the dangling-artwork
// verification sweep needs: walk every art-claiming movie/show row, and
// clear the paths whose files are confirmed gone. Separate from
// MissingArtLister so verification stays optional (the handler runs
// list-only when it isn't wired).
type ArtVerifierStore interface {
	ListItemsWithArt(ctx context.Context, afterID uuid.UUID, limit int32) ([]media.ArtPathsItem, error)
	ClearItemArtPaths(ctx context.Context, id uuid.UUID, clearPoster, clearFanart bool) error
}

// RefreshArtConfig is the optional JSON payload for the
// refresh_missing_art task. Limit caps how many items the run will
// touch; defaults to 200 when unset, matches the maintenance
// endpoint's default. Operator can crank it up via the Tasks UI for
// a one-off catch-up sweep on a freshly imported library.
type RefreshArtConfig struct {
	Limit int32 `json:"limit"`
}

// artVerifyBatchSize is the keyset page size for the dangling-artwork
// sweep. Bounds per-query memory; the sweep itself always walks the
// full art-bearing set (a stat per claimed path is cheap next to the
// TMDB round-trips the enrich loop below makes).
const artVerifyBatchSize = 500

// artPresence classifies one stored artwork path against the filesystem
// (or object store) it is supposed to live in.
type artPresence int

const (
	// artPresent: the file exists under at least one library root.
	artPresent artPresence = iota
	// artDangling: the file is gone but its parent directory exists under
	// some root — the media folder is reachable and the image inside it
	// was deleted (release upgrade rewrote the folder contents, operator
	// removed poster.jpg, sync tool dropped it). Safe to clear + re-enrich.
	artDangling
	// artUnverifiable: neither the file nor its parent directory resolves
	// under any root. That's what an unmounted volume, a renamed folder, or
	// a fully deleted release looks like — the scan-side orphan handling
	// owns those lifecycles, and clearing art on a temporarily missing
	// mount would trigger a pointless refetch storm when it returns. Also
	// the steady state on object-storage backends, where directory keys
	// don't exist; those installs keep today's behaviour. Leave untouched.
	artUnverifiable
)

// RefreshMissingArtHandler runs the same enrichment loop as the
// admin POST /api/v1/maintenance/refresh-missing-art endpoint, but
// on a cron — so a partially-enriched library self-heals without
// the operator hitting a button. The endpoint itself stays for
// manual "do this NOW" triggers; this handler is the safety net
// so users don't have to know it exists.
//
// When WithArtVerification is wired it additionally sweeps items that
// CLAIM artwork and verifies the files still exist: a poster_path can
// outlive its file, and until it is cleared every client renders a 404
// tile while both the missing-art query (poster IS NULL) and the
// scanner's enrich gate (poster set + well-shaped ⇒ enriched) look the
// other way. Confirmed-dangling paths are cleared and the items fed
// through the same enrich loop, so the poster is refetched when the
// provider still has one and the row falls back to a clean titled
// placeholder when it doesn't.
type RefreshMissingArtHandler struct {
	media    MissingArtLister
	enricher MissingArtEnricher
	logger   *slog.Logger

	// Dangling-artwork verification deps — all three set via
	// WithArtVerification, all nil otherwise (verification skipped).
	artStore ArtVerifierStore
	roots    func() []string
	stat     func(ctx context.Context, absPath string) error
	// batchSize overrides artVerifyBatchSize in tests.
	batchSize int32
}

// NewRefreshMissingArtHandler constructs the handler.
func NewRefreshMissingArtHandler(media MissingArtLister, enricher MissingArtEnricher, logger *slog.Logger) *RefreshMissingArtHandler {
	return &RefreshMissingArtHandler{media: media, enricher: enricher, logger: logger, batchSize: artVerifyBatchSize}
}

// WithArtVerification enables the dangling-artwork sweep. roots returns
// every library scan path (the same set the /artwork/* route resolves
// against); stat reports whether an absolute path exists, and must go
// through the artwork media store so object-storage installs don't see
// every file as missing. Returns the handler for chaining.
func (h *RefreshMissingArtHandler) WithArtVerification(
	store ArtVerifierStore,
	roots func() []string,
	stat func(ctx context.Context, absPath string) error,
) *RefreshMissingArtHandler {
	h.artStore = store
	h.roots = roots
	h.stat = stat
	return h
}

func (h *RefreshMissingArtHandler) verificationWired() bool {
	return h.artStore != nil && h.roots != nil && h.stat != nil
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

	// Phase 1 (optional): verify claimed artwork, clear confirmed-dangling
	// paths. Runs before the missing-art listing so a cleared poster lands
	// in this run's pool instead of waiting for the next tick.
	stats, healIDs := h.verifyArtPaths(ctx)
	verifySummary := ""
	if h.verificationWired() {
		verifySummary = fmt.Sprintf("art_checked=%d dangling_cleared=%d", stats.checked, stats.cleared)
		if stats.unverifiable > 0 {
			// Surfaced because a LARGE count is an operator signal: a
			// scan-path volume that isn't mounted looks exactly like this.
			verifySummary += fmt.Sprintf(" unverifiable=%d", stats.unverifiable)
		}
		verifySummary += "; "
	}

	items, err := h.media.ListItemsMissingArt(ctx, cfg.Limit)
	if err != nil {
		return "", fmt.Errorf("list items missing art: %w", err)
	}

	// Candidates = the missing-art pool plus any just-cleared item the pool
	// query didn't return: a fanart-only clear leaves poster_path set, and
	// on an over-cap backlog the capped listing can crowd cleared items
	// out. Dedupe so nothing is enriched twice in one run.
	type candidate struct {
		id    uuid.UUID
		title string
	}
	seen := make(map[uuid.UUID]bool, len(items)+len(healIDs))
	cands := make([]candidate, 0, len(items)+len(healIDs))
	for _, it := range items {
		if seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		cands = append(cands, candidate{id: it.ID, title: it.Title})
	}
	for _, id := range healIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		cands = append(cands, candidate{id: id})
	}

	if len(cands) == 0 {
		return verifySummary + "no items missing art", nil
	}

	var refreshed, failed int
	for _, c := range cands {
		if err := h.enricher.EnrichItem(ctx, c.id); err != nil {
			h.logger.WarnContext(ctx, "refresh_missing_art: item failed",
				"item_id", c.id, "title", c.title, "err", err)
			failed++
			continue
		}
		refreshed++
	}

	return fmt.Sprintf("%scandidates=%d refreshed=%d failed=%d", verifySummary, len(cands), refreshed, failed), nil
}

// artVerifyStats aggregates one verification sweep for the task's result line.
type artVerifyStats struct {
	checked      int // items whose claimed paths were probed
	cleared      int // items that had at least one dangling path cleared
	unverifiable int // items with a missing path that couldn't be safely classified
}

// verifyArtPaths walks every art-claiming top-level item in keyset pages,
// stats each claimed path against the library roots, and clears the ones
// confirmed dangling. Returns the sweep stats plus the IDs whose art was
// cleared so the caller can re-enrich them this run. Best-effort: any
// error degrades to "verify less", never blocks the classic missing-art
// phase. No-op when WithArtVerification wasn't called.
func (h *RefreshMissingArtHandler) verifyArtPaths(ctx context.Context) (artVerifyStats, []uuid.UUID) {
	var stats artVerifyStats
	if !h.verificationWired() {
		return stats, nil
	}
	roots := h.roots()
	if len(roots) == 0 {
		// No libraries (or the lookup failed) — nothing is resolvable, and
		// clearing on a guess is exactly what the unverifiable guard forbids.
		return stats, nil
	}

	batchSize := h.batchSize
	if batchSize <= 0 {
		batchSize = artVerifyBatchSize
	}

	var healIDs []uuid.UUID
	after := uuid.Nil
	for {
		batch, err := h.artStore.ListItemsWithArt(ctx, after, batchSize)
		if err != nil {
			h.logger.WarnContext(ctx, "refresh_missing_art: list items with art", "err", err)
			return stats, healIDs
		}
		if len(batch) == 0 {
			return stats, healIDs
		}
		for i := range batch {
			it := &batch[i]
			after = it.ID
			if ctx.Err() != nil {
				return stats, healIDs
			}
			stats.checked++
			sawUnverifiable := false
			clearPoster := false
			if it.PosterPath != nil {
				switch h.classifyArtPath(ctx, roots, *it.PosterPath) {
				case artDangling:
					clearPoster = true
				case artUnverifiable:
					sawUnverifiable = true
				}
			}
			clearFanart := false
			if it.FanartPath != nil {
				switch h.classifyArtPath(ctx, roots, *it.FanartPath) {
				case artDangling:
					clearFanart = true
				case artUnverifiable:
					sawUnverifiable = true
				}
			}
			if sawUnverifiable {
				stats.unverifiable++
			}
			if !clearPoster && !clearFanart {
				continue
			}
			if err := h.artStore.ClearItemArtPaths(ctx, it.ID, clearPoster, clearFanart); err != nil {
				h.logger.WarnContext(ctx, "refresh_missing_art: clear dangling art paths",
					"item_id", it.ID, "err", err)
				continue
			}
			stats.cleared++
			healIDs = append(healIDs, it.ID)
			h.logger.InfoContext(ctx, "refresh_missing_art: cleared dangling artwork reference",
				"item_id", it.ID, "type", it.Type,
				"cleared_poster", clearPoster, "cleared_fanart", clearFanart)
		}
		if int32(len(batch)) < batchSize {
			return stats, healIDs
		}
	}
}

// classifyArtPath resolves one stored library-relative artwork path against
// every library root, mirroring how the /artwork/* route serves it. The
// dangling verdict requires positive evidence the file (and not its whole
// folder) is what's missing — see the artPresence constants.
func (h *RefreshMissingArtHandler) classifyArtPath(ctx context.Context, roots []string, rel string) artPresence {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) ||
		(len(clean) >= 2 && clean[1] == ':') {
		// Legacy-bad shapes (absolute paths, traversal, .artwork/ era
		// leftovers) are the scanner's badPosterPath territory — it already
		// forces re-enrichment for those. Not ours to clear.
		return artUnverifiable
	}
	dirSeen := false
	for _, root := range roots {
		abs := filepath.Join(root, clean)
		if err := h.stat(ctx, abs); err == nil {
			return artPresent
		}
		if !dirSeen {
			if err := h.stat(ctx, filepath.Dir(abs)); err == nil {
				dirSeen = true
			}
		}
	}
	if dirSeen {
		return artDangling
	}
	return artUnverifiable
}
