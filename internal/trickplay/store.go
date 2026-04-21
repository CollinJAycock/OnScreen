package trickplay

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// PgStore persists trickplay generation state in the trickplay_status table.
// It satisfies the Store interface the Generator depends on and also exposes
// read helpers used by HTTP handlers.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewStore returns a PgStore backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

func (s *PgStore) UpsertPending(ctx context.Context, itemID, fileID uuid.UUID, spec Spec) error {
	err := gen.New(s.pool).UpsertTrickplayPending(ctx, gen.UpsertTrickplayPendingParams{
		ItemID:      itemID,
		FileID:      pgtype.UUID{Bytes: fileID, Valid: fileID != uuid.Nil},
		IntervalSec: int32(spec.IntervalSec),
		ThumbWidth:  int32(spec.ThumbWidth),
		ThumbHeight: int32(spec.ThumbHeight),
		GridCols:    int32(spec.GridCols),
		GridRows:    int32(spec.GridRows),
	})
	if err != nil {
		return fmt.Errorf("upsert trickplay pending: %w", err)
	}
	return nil
}

func (s *PgStore) MarkDone(ctx context.Context, itemID uuid.UUID, spriteCount int) error {
	return gen.New(s.pool).MarkTrickplayDone(ctx, gen.MarkTrickplayDoneParams{
		ItemID:      itemID,
		SpriteCount: int32(spriteCount),
	})
}

func (s *PgStore) MarkFailed(ctx context.Context, itemID uuid.UUID, reason string) error {
	// Cap reason to keep a runaway ffmpeg stderr from bloating the row.
	if len(reason) > 1000 {
		reason = reason[:1000]
	}
	return gen.New(s.pool).MarkTrickplayFailed(ctx, gen.MarkTrickplayFailedParams{
		ItemID:    itemID,
		LastError: &reason,
	})
}

// Status reports the current persisted state for an item; returns (_, false,
// nil) when no row exists yet. Used by the API handler to tell clients
// whether trickplay is available.
func (s *PgStore) Status(ctx context.Context, itemID uuid.UUID) (gen.TrickplayStatus, bool, error) {
	row, err := gen.New(s.pool).GetTrickplayStatus(ctx, itemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.TrickplayStatus{}, false, nil
		}
		return gen.TrickplayStatus{}, false, err
	}
	return row, true, nil
}
