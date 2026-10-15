package trickplay

import (
	"context"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// statusReader is the read surface Service needs from the persistence layer.
// PgStore satisfies it; tests can substitute a fake.
type statusReader interface {
	Status(ctx context.Context, itemID uuid.UUID) (gen.TrickplayStatus, bool, error)
}

// Service bundles the Generator and a status reader into a single facade
// suitable for HTTP handlers. It hides the gen.TrickplayStatus DB row type
// so API layers don't pull in internal/db/gen.
type Service struct {
	gen   *Generator
	store statusReader
	// chapters reads chapter-thumbnail state; nil (tests) = none.
	chapters chapterThumbReader
}

// chapterThumbReader is the chapter-thumbnail read surface. PgStore
// satisfies it.
type chapterThumbReader interface {
	ChapterThumbs(ctx context.Context, itemID uuid.UUID) (uuid.UUID, int, error)
	ListChapterThumbCandidates(ctx context.Context, limit int) ([]uuid.UUID, error)
}

// NewService wires a Service around an existing generator and store. Both
// must share the same rootDir/pool; the caller is responsible for that.
func NewService(gen *Generator, store *PgStore) *Service {
	return &Service{gen: gen, store: store, chapters: store}
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

// ChapterThumbs returns the file whose chapters have thumbnails
// (chapter_000.jpg … in ItemDir) and how many; uuid.Nil / 0 when none.
func (s *Service) ChapterThumbs(ctx context.Context, itemID uuid.UUID) (uuid.UUID, int, error) {
	if s.chapters == nil {
		return uuid.Nil, 0, nil
	}
	return s.chapters.ChapterThumbs(ctx, itemID)
}

// ChapterThumbCandidates lists items whose sprites predate chapter
// thumbnails, oldest first (the chapter_thumbnails task's work list).
func (s *Service) ChapterThumbCandidates(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if s.chapters == nil {
		return nil, nil
	}
	return s.chapters.ListChapterThumbCandidates(ctx, limit)
}

// BackfillChapterThumbs generates chapter thumbnails for an item whose
// sprites already exist, from the file those sprites were cut from, without
// touching the sprites.
func (s *Service) BackfillChapterThumbs(ctx context.Context, itemID uuid.UUID) (int, error) {
	row, exists, err := s.store.Status(ctx, itemID)
	if err != nil {
		return 0, err
	}
	if !exists || row.Status != "done" || !row.FileID.Valid {
		return 0, nil
	}
	return s.gen.GenerateChapterThumbs(ctx, itemID, uuid.UUID(row.FileID.Bytes))
}
