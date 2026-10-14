package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/scanner"
)

// photoScanStore adapts the generated queries to scanner.PhotoStore:
// path-keyed photo items and the per-library RAW + JPEG pairing option
// (migration 00057).
type photoScanStore struct{ q *gen.Queries }

func (s photoScanStore) RawJPEGPairing(ctx context.Context, libraryID uuid.UUID) (bool, error) {
	row, err := s.q.GetLibraryPhotoSettings(ctx, libraryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil // no settings row: the default, pairing on
	}
	if err != nil {
		return true, err
	}
	return row.RawJpegPairing, nil
}

func (s photoScanStore) PhotoFilesByPaths(ctx context.Context, libraryID uuid.UUID, paths []string) ([]scanner.PhotoFileRef, error) {
	rows, err := s.q.ListPhotoFilesByPaths(ctx, gen.ListPhotoFilesByPathsParams{LibraryID: libraryID, Paths: paths})
	if err != nil {
		return nil, err
	}
	out := make([]scanner.PhotoFileRef, len(rows))
	for i, r := range rows {
		out[i] = scanner.PhotoFileRef{FileID: r.ID, ItemID: r.MediaItemID, Path: r.FilePath}
	}
	return out, nil
}

func (s photoScanStore) CreatePhotoItem(ctx context.Context, libraryID uuid.UUID, title, sortTitle string) (uuid.UUID, error) {
	return s.q.CreatePhotoItem(ctx, gen.CreatePhotoItemParams{LibraryID: libraryID, Title: title, SortTitle: sortTitle})
}
