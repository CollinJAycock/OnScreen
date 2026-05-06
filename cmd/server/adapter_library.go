package main

import (
	"context"

	"github.com/google/uuid"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
)

// userLibraryAccessAdapter bridges the library service to v1.UserLibraryAccessService.
// It looks up the target user's is_admin flag so admins always report every
// library as enabled.
type userLibraryAccessAdapter struct {
	lib *library.Service
	q   *gen.Queries
}

func (a *userLibraryAccessAdapter) ListAccessForUser(ctx context.Context, userID uuid.UUID) ([]v1.UserLibraryAccessEntry, error) {
	u, err := a.q.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	accesses, err := a.lib.ListAccessForUser(ctx, userID, u.IsAdmin)
	if err != nil {
		return nil, err
	}
	out := make([]v1.UserLibraryAccessEntry, len(accesses))
	for i, acc := range accesses {
		out[i] = v1.UserLibraryAccessEntry{
			LibraryID: acc.Library.ID,
			Name:      acc.Library.Name,
			Type:      acc.Library.Type,
			Enabled:   acc.Enabled,
		}
	}
	return out, nil
}

func (a *userLibraryAccessAdapter) ReplaceAccessForUser(ctx context.Context, userID uuid.UUID, libraryIDs []uuid.UUID) error {
	return a.lib.ReplaceAccessForUser(ctx, userID, libraryIDs)
}

func (a *libraryAdapter) ListLibraryAccessByUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return a.q.ListLibraryAccessByUser(ctx, userID)
}

func (a *libraryAdapter) ListAllowedLibraryIDsForUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return a.q.ListAllowedLibraryIDsForUser(ctx, userID)
}

func (a *libraryAdapter) HasLibraryAccess(ctx context.Context, userID, libraryID uuid.UUID) (bool, error) {
	return a.q.HasLibraryAccess(ctx, gen.HasLibraryAccessParams{UserID: userID, LibraryID: libraryID})
}

func (a *libraryAdapter) GrantAutoLibrariesToUser(ctx context.Context, userID uuid.UUID) error {
	return a.q.GrantAutoLibrariesToUser(ctx, userID)
}

func (a *libraryAdapter) GrantLibraryAccess(ctx context.Context, userID, libraryID uuid.UUID) error {
	return a.q.GrantLibraryAccess(ctx, gen.GrantLibraryAccessParams{UserID: userID, LibraryID: libraryID})
}

func (a *libraryAdapter) RevokeAllLibraryAccessForUser(ctx context.Context, userID uuid.UUID) error {
	return a.q.RevokeAllLibraryAccessForUser(ctx, userID)
}

type libraryAdapter struct{ q *gen.Queries }

func (a *libraryAdapter) GetLibrary(ctx context.Context, id uuid.UUID) (library.Library, error) {
	g, err := a.q.GetLibrary(ctx, id)
	if err != nil {
		return library.Library{}, err
	}
	return genLibToLib(g), nil
}

// IsLibraryAnime implements scanner.LibraryAnimeChecker. Single-bool
// lookup the show-enricher uses to decide whether AniList runs
// primary or fallback.
func (a *libraryAdapter) IsLibraryAnime(ctx context.Context, libraryID uuid.UUID) (bool, error) {
	return a.q.IsLibraryAnime(ctx, libraryID)
}

// IsLibraryManga is the same shape as IsLibraryAnime but for the
// book / manga split. The book enricher reads it to flip AniList from
// "fallback for manga rows" to "primary for everything in this
// library" — the operator's library-type pick is the single source
// of truth.
func (a *libraryAdapter) IsLibraryManga(ctx context.Context, libraryID uuid.UUID) (bool, error) {
	return a.q.IsLibraryManga(ctx, libraryID)
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
