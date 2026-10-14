package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/studios"
)

// enricherExtrasAdapter implements scanner.ItemExtrasStore: extra artwork
// paths and library options straight from gen, studios through the studios
// service.
type enricherExtrasAdapter struct {
	rw      *gen.Queries
	studios *studios.Service
}

func (a *enricherExtrasAdapter) SetItemExtraArt(ctx context.Context, itemID uuid.UUID, logo, banner, disc *string) error {
	return a.rw.SetMediaItemExtraArt(ctx, gen.SetMediaItemExtraArtParams{
		ID:          itemID,
		LogoPath:    logo,
		BannerPath:  banner,
		DiscArtPath: disc,
	})
}

func (a *enricherExtrasAdapter) SetItemStudios(ctx context.Context, itemID uuid.UUID, kind string, list []metadata.Studio) error {
	return a.studios.SetItemStudios(ctx, itemID, kind, list)
}

func (a *enricherExtrasAdapter) ItemStudioNames(ctx context.Context, itemID uuid.UUID) ([]string, error) {
	return a.studios.ItemStudioNames(ctx, itemID)
}

// LibraryNFOOptions reads the primary: an admin who just turned save_nfo on
// and hits Refresh expects it to apply, replication lag or not.
func (a *enricherExtrasAdapter) LibraryNFOOptions(ctx context.Context, libraryID uuid.UUID) (bool, string, error) {
	r, err := a.rw.GetLibraryNFOOptions(ctx, libraryID)
	if err != nil {
		return false, "", err
	}
	return r.SaveNfo, r.Language, nil
}
