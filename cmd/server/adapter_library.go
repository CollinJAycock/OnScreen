package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
)

type libraryAdapter struct{ q *gen.Queries }

func (a *libraryAdapter) GetLibrary(ctx context.Context, id uuid.UUID) (library.Library, error) {
	g, err := a.q.GetLibrary(ctx, id)
	if err != nil {
		return library.Library{}, err
	}
	return genLibToLib(g), nil
}

func (a *libraryAdapter) ListLibraries(ctx context.Context) ([]library.Library, error) {
	gs, err := a.q.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]library.Library, len(gs))
	for i, g := range gs {
		out[i] = genLibToLib(g)
	}
	return out, nil
}

func (a *libraryAdapter) CreateLibrary(ctx context.Context, p library.CreateLibraryParams) (library.Library, error) {
	g, err := a.q.CreateLibrary(ctx, libCreateParamsToGen(p))
	if err != nil {
		return library.Library{}, err
	}
	return genLibToLib(g), nil
}

func (a *libraryAdapter) UpdateLibrary(ctx context.Context, p library.UpdateLibraryParams) (library.Library, error) {
	g, err := a.q.UpdateLibrary(ctx, libUpdateParamsToGen(p))
	if err != nil {
		return library.Library{}, err
	}
	return genLibToLib(g), nil
}

func (a *libraryAdapter) SoftDeleteLibrary(ctx context.Context, id uuid.UUID) error {
	return a.q.SoftDeleteLibrary(ctx, id)
}

func (a *libraryAdapter) SoftDeleteMediaItemsByLibrary(ctx context.Context, libraryID uuid.UUID) error {
	return a.q.SoftDeleteMediaItemsByLibrary(ctx, libraryID)
}

func (a *libraryAdapter) RefreshHubRecentlyAdded(ctx context.Context) error {
	return a.q.RefreshHubRecentlyAdded(ctx)
}

func (a *libraryAdapter) MarkLibraryScanCompleted(ctx context.Context, id uuid.UUID) error {
	return a.q.MarkLibraryScanCompleted(ctx, id)
}

func (a *libraryAdapter) MarkLibraryMetadataRefreshed(ctx context.Context, id uuid.UUID) error {
	return a.q.MarkLibraryMetadataRefreshed(ctx, id)
}

func (a *libraryAdapter) ListLibrariesDueForScan(ctx context.Context) ([]library.Library, error) {
	gs, err := a.q.ListLibrariesDueForScan(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]library.Library, len(gs))
	for i, g := range gs {
		out[i] = genLibToLib(g)
	}
	return out, nil
}

func (a *libraryAdapter) ListLibrariesDueForMetadataRefresh(ctx context.Context) ([]library.Library, error) {
	gs, err := a.q.ListLibrariesDueForMetadataRefresh(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]library.Library, len(gs))
	for i, g := range gs {
		out[i] = genLibToLib(g)
	}
	return out, nil
}

func (a *libraryAdapter) CountLibraries(ctx context.Context) (int64, error) {
	return a.q.CountLibraries(ctx)
}
