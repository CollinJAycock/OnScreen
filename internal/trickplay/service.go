package trickplay

import (
	"context"

	"github.com/google/uuid"
)

// Service bundles the Generator and PgStore into a single facade suitable
// for HTTP handlers. It hides the gen.TrickplayStatus DB row type so API
// layers don't pull in internal/db/gen.
type Service struct {
	gen   *Generator
	store *PgStore
}

// NewService wires a Service around an existing generator and store. Both
// must share the same rootDir/pool; the caller is responsible for that.
func NewService(gen *Generator, store *PgStore) *Service {
	return &Service{gen: gen, store: store}
}

// Generate delegates to the underlying generator.
func (s *Service) Generate(ctx context.Context, itemID uuid.UUID) error {
	return s.gen.Generate(ctx, itemID)
}

// ItemDir delegates to the underlying generator.
func (s *Service) ItemDir(itemID uuid.UUID) string { return s.gen.ItemDir(itemID) }

// Status returns a trimmed view of the trickplay_status row: the spec used
// to generate, the textual status, sprite count, and an exists flag that's
// false when no row exists yet (callers render "not_started" in that case).
func (s *Service) Status(ctx context.Context, itemID uuid.UUID) (Spec, string, int, bool, error) {
	row, exists, err := s.store.Status(ctx, itemID)
	if err != nil || !exists {
		return Spec{}, "", 0, exists, err
	}
	spec := Spec{
		IntervalSec: int(row.IntervalSec),
		ThumbWidth:  int(row.ThumbWidth),
		ThumbHeight: int(row.ThumbHeight),
		GridCols:    int(row.GridCols),
		GridRows:    int(row.GridRows),
	}
	return spec, row.Status, int(row.SpriteCount), true, nil
}
