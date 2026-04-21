package trickplay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// Store is the minimal DB surface the generator needs. It's implemented by
// cmd/server and cmd/worker adapters over *gen.Queries.
type Store interface {
	UpsertPending(ctx context.Context, itemID, fileID uuid.UUID, spec Spec) error
	MarkDone(ctx context.Context, itemID uuid.UUID, spriteCount int) error
	MarkFailed(ctx context.Context, itemID uuid.UUID, reason string) error
}

// MediaLookup returns the primary file for an item along with its duration
// in seconds. Returns os.ErrNotExist semantics (errors.Is ok) when the item
// has no usable file.
type MediaLookup interface {
	PrimaryFile(ctx context.Context, itemID uuid.UUID) (filePath string, fileID uuid.UUID, durationSec int, err error)
}

// ErrNoFile is returned by generators when an item has no playable file to
// extract thumbnails from. Callers typically log and skip.
var ErrNoFile = errors.New("trickplay: item has no playable file")

// Generator renders trickplay sprite sheets + WebVTT indexes for media items
// and records state in the trickplay_status table.
//
// rootDir is the on-disk base for generated artifacts; every item gets its
// own subdirectory (rootDir/<itemID>/). Callers should serve that directory
// via an HTTP route. The generator never writes outside rootDir.
type Generator struct {
	rootDir string
	store   Store
	lookup  MediaLookup
	spec    Spec
	logger  *slog.Logger
}

// New returns a Generator using the Default spec. rootDir is created on
// first call if it doesn't exist.
func New(rootDir string, store Store, lookup MediaLookup, logger *slog.Logger) *Generator {
	return &Generator{
		rootDir: rootDir,
		store:   store,
		lookup:  lookup,
		spec:    Default,
		logger:  logger,
	}
}

// WithSpec returns g mutated to use the given spec. Used by tests and by
// admins overriding the default thumbnail interval or grid.
func (g *Generator) WithSpec(s Spec) *Generator {
	clone := *g
	clone.spec = s
	return &clone
}

// ItemDir is the on-disk directory where sprites + index.vtt for itemID live.
func (g *Generator) ItemDir(itemID uuid.UUID) string {
	return filepath.Join(g.rootDir, itemID.String())
}

// Generate produces trickplay artifacts for a single item. Any failure is
// recorded in trickplay_status and returned to the caller so the API handler
// can surface it; the status row is never left in 'pending' on exit.
func (g *Generator) Generate(ctx context.Context, itemID uuid.UUID) error {
	path, fileID, duration, err := g.lookup.PrimaryFile(ctx, itemID)
	if err != nil {
		return fmt.Errorf("trickplay lookup %s: %w", itemID, err)
	}
	if path == "" {
		return ErrNoFile
	}
	if duration <= 0 {
		return fmt.Errorf("trickplay: item %s has zero duration", itemID)
	}

	if err := g.store.UpsertPending(ctx, itemID, fileID, g.spec); err != nil {
		return fmt.Errorf("trickplay upsert pending: %w", err)
	}

	outDir := g.ItemDir(itemID)
	// Wipe any prior run — a regeneration with different spec would leave
	// stale sprites behind otherwise.
	if err := os.RemoveAll(outDir); err != nil {
		g.markFailed(ctx, itemID, fmt.Sprintf("clear output dir: %v", err))
		return err
	}

	names, err := generateSprites(ctx, path, outDir, g.spec)
	if err != nil {
		g.markFailed(ctx, itemID, err.Error())
		return err
	}

	vtt, err := g.spec.WriteVTT(duration, names)
	if err != nil {
		g.markFailed(ctx, itemID, err.Error())
		return err
	}
	vttPath := filepath.Join(outDir, "index.vtt")
	if err := os.WriteFile(vttPath, []byte(vtt), 0o644); err != nil {
		g.markFailed(ctx, itemID, fmt.Sprintf("write vtt: %v", err))
		return err
	}

	if err := g.store.MarkDone(ctx, itemID, len(names)); err != nil {
		// Artifacts are on disk; a DB write failure here is worth logging
		// but the next Get will miss and trigger a re-run — not fatal.
		g.logger.ErrorContext(ctx, "trickplay mark done failed",
			"item_id", itemID, "error", err)
		return err
	}
	g.logger.InfoContext(ctx, "trickplay generated",
		"item_id", itemID, "sprites", len(names), "duration_sec", duration)
	return nil
}

func (g *Generator) markFailed(ctx context.Context, itemID uuid.UUID, reason string) {
	if err := g.store.MarkFailed(ctx, itemID, reason); err != nil {
		g.logger.ErrorContext(ctx, "trickplay mark failed", "item_id", itemID, "error", err)
	}
}

// compile-time check that the lookup signature matches the expected media
// service method shape; prevents drift if the service interface changes.
var _ = func(svc *media.Service) MediaLookup {
	return mediaServiceLookup{svc: svc}
}

// mediaServiceLookup is a thin adapter that satisfies MediaLookup using the
// domain media.Service. Lives here so cmd/ packages can wire it in one line.
type mediaServiceLookup struct {
	svc *media.Service
}

func (m mediaServiceLookup) PrimaryFile(ctx context.Context, itemID uuid.UUID) (string, uuid.UUID, int, error) {
	files, err := m.svc.GetFiles(ctx, itemID)
	if err != nil {
		return "", uuid.Nil, 0, err
	}
	if len(files) == 0 {
		return "", uuid.Nil, 0, nil
	}
	f := files[0]
	durationSec := 0
	if f.DurationMS != nil {
		durationSec = int(*f.DurationMS / 1000)
	}
	return f.FilePath, f.ID, durationSec, nil
}

// NewWithService is a convenience for the common case where the caller has
// a *media.Service handy; it wires the lookup adapter automatically.
func NewWithService(rootDir string, store Store, svc *media.Service, logger *slog.Logger) *Generator {
	return New(rootDir, store, mediaServiceLookup{svc: svc}, logger)
}
