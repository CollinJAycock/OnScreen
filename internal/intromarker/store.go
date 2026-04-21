package intromarker

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// Marker is the domain representation of an intro/credits marker.
type Marker struct {
	ID          uuid.UUID
	MediaItemID uuid.UUID
	Kind        string
	StartMS     int64
	EndMS       int64
	Source      string
}

// Store provides CRUD access to the intro_markers table. The Detector writes
// auto-sourced markers directly via gen.Queries; this type handles manual
// admin edits and API reads.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// List returns all markers for mediaItemID, ordered by start_ms.
func (s *Store) List(ctx context.Context, mediaItemID uuid.UUID) ([]Marker, error) {
	rows, err := gen.New(s.pool).ListIntroMarkersByMedia(ctx, mediaItemID)
	if err != nil {
		return nil, fmt.Errorf("list markers: %w", err)
	}
	out := make([]Marker, 0, len(rows))
	for _, r := range rows {
		out = append(out, Marker{
			ID:          r.ID,
			MediaItemID: r.MediaItemID,
			Kind:        r.Kind,
			StartMS:     r.StartMs,
			EndMS:       r.EndMs,
			Source:      r.Source,
		})
	}
	return out, nil
}

// Upsert writes a manual marker, overwriting any existing marker (auto or
// manual) for the same (mediaItemID, kind). Callers must validate admin
// authorisation before invoking this.
func (s *Store) Upsert(ctx context.Context, mediaItemID uuid.UUID, kind string, startMS, endMS int64) (Marker, error) {
	if kind != "intro" && kind != "credits" {
		return Marker{}, fmt.Errorf("invalid kind %q", kind)
	}
	if startMS < 0 || endMS <= startMS {
		return Marker{}, errors.New("invalid range: end_ms must be greater than start_ms")
	}
	row, err := gen.New(s.pool).UpsertIntroMarker(ctx, gen.UpsertIntroMarkerParams{
		MediaItemID: mediaItemID,
		Kind:        kind,
		StartMs:     startMS,
		EndMs:       endMS,
		Source:      "manual",
	})
	if err != nil {
		return Marker{}, fmt.Errorf("upsert marker: %w", err)
	}
	return Marker{
		ID:          row.ID,
		MediaItemID: row.MediaItemID,
		Kind:        row.Kind,
		StartMS:     row.StartMs,
		EndMS:       row.EndMs,
		Source:      row.Source,
	}, nil
}

// Delete removes a single (mediaItemID, kind) marker. Returns nil whether
// or not a matching row existed.
func (s *Store) Delete(ctx context.Context, mediaItemID uuid.UUID, kind string) error {
	err := gen.New(s.pool).DeleteIntroMarker(ctx, gen.DeleteIntroMarkerParams{
		MediaItemID: mediaItemID,
		Kind:        kind,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("delete marker: %w", err)
	}
	return nil
}
