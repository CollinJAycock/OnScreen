// cmd/server/adapter.go — bridges gen.Queries to domain Querier interfaces.
// Type conversions live here so domain packages stay free of pgtype/pgx imports.
//
// Adapter implementations are split by domain into sibling files:
//   - adapter_library.go    — libraryAdapter
//   - adapter_media.go      — mediaAdapter (items + files + filtered listings)
//   - adapter_watchevent.go — watchEventAdapter
//   - adapter_match.go      — matchSearchAdapter, favoritesChecker
package main

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/dbconv"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// ── Type conversion helpers ───────────────────────────────────────────────────

func pgtimeTZ(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

func mustTimeTZ(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

func uuidPtrToPgtype(u *uuid.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *u, Valid: true}
}

func numericToFloat64Ptr(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	f8, err := n.Float64Value()
	if err != nil || !f8.Valid {
		return nil
	}
	v := f8.Float64
	return &v
}

func int32PtrToIntPtr(i *int32) *int {
	if i == nil {
		return nil
	}
	v := int(*i)
	return &v
}

// float64PtrToNumeric / intPtrToInt32Ptr delegate to dbconv so the (guarded)
// scalar conversions have a single home — the worker previously kept its own
// copy of intPtrToInt32Ptr that lacked the int32 range guard. See dbconv.
func float64PtrToNumeric(f *float64) pgtype.Numeric { return dbconv.Float64PtrToNumeric(f) }
func intPtrToInt32Ptr(i *int) *int32                { return dbconv.IntPtrToInt32Ptr(i) }

func durationToPtr(d time.Duration) *time.Duration {
	return &d
}

// ── Library conversions ───────────────────────────────────────────────────────

func genLibToLib(g gen.Library) library.Library {
	return library.Library{
		ID:                      g.ID,
		Name:                    g.Name,
		Type:                    g.Type,
		Paths:                   g.ScanPaths,
		Agent:                   g.Agent,
		Lang:                    g.Language,
		IsPrivate:               g.IsPrivate,
		AutoGrantNewUsers:       g.AutoGrantNewUsers,
		ScanInterval:            durationToPtr(g.ScanInterval),
		ScanLastCompletedAt:     pgtimeTZ(g.ScanLastCompletedAt),
		MetadataRefreshInterval: durationToPtr(g.MetadataRefreshInterval),
		MetadataLastRefreshedAt: pgtimeTZ(g.MetadataLastRefreshedAt),
		CreatedAt:               mustTimeTZ(g.CreatedAt),
		UpdatedAt:               mustTimeTZ(g.UpdatedAt),
		DeletedAt:               pgtimeTZ(g.DeletedAt),
	}
}

func libCreateParamsToGen(p library.CreateLibraryParams) gen.CreateLibraryParams {
	return gen.CreateLibraryParams{
		Name:                    p.Name,
		Type:                    p.Type,
		ScanPaths:               p.Paths,
		Agent:                   p.Agent,
		Language:                p.Lang,
		ScanInterval:            p.ScanInterval,
		MetadataRefreshInterval: p.MetadataRefreshInterval,
		IsPrivate:               p.IsPrivate,
		AutoGrantNewUsers:       p.AutoGrantNewUsers,
	}
}

func libUpdateParamsToGen(p library.UpdateLibraryParams) gen.UpdateLibraryParams {
	return gen.UpdateLibraryParams{
		ID:                      p.ID,
		Name:                    p.Name,
		ScanPaths:               p.Paths,
		Agent:                   p.Agent,
		Language:                p.Lang,
		ScanInterval:            p.ScanInterval,
		MetadataRefreshInterval: p.MetadataRefreshInterval,
		IsPrivate:               p.IsPrivate,
		AutoGrantNewUsers:       p.AutoGrantNewUsers,
	}
}

// ── Media item / file conversions ─────────────────────────────────────────────
//
// These delegate to internal/dbconv, the single source of truth for the
// gen<->domain field mapping. Do NOT inline a copy here: the server and worker
// adapters each used to carry their own and they drifted (the worker silently
// dropped music + technical-metadata fields). Keep these as thin forwarders so
// they can never diverge again.

func itemFromGenFields(
	id, libraryID uuid.UUID, typ, title, sortTitle string,
	originalTitle *string, year *int32, summary, tagline *string,
	rating, audienceRating pgtype.Numeric, contentRating *string, durationMs *int64,
	genres, tags []string, tmdbID, tvdbID *int32, imdbID *string,
	parentID pgtype.UUID, idx *int32, posterPath, fanartPath, thumbPath *string,
	originallyAvailableAt pgtype.Date,
	createdAt, updatedAt, deletedAt pgtype.Timestamptz,
) media.Item {
	return dbconv.ItemFromGenFields(id, libraryID, typ, title, sortTitle,
		originalTitle, year, summary, tagline,
		rating, audienceRating, contentRating, durationMs,
		genres, tags, tmdbID, tvdbID, imdbID,
		parentID, idx, posterPath, fanartPath, thumbPath,
		originallyAvailableAt, createdAt, updatedAt, deletedAt)
}

func genGetItemRowToItem(r gen.GetMediaItemRow) media.Item { return dbconv.GenGetItemRowToItem(r) }
func genGetItemByTMDBIDRowToItem(r gen.GetMediaItemByTMDBIDRow) media.Item {
	return dbconv.GenGetItemByTMDBIDRowToItem(r)
}
func genCreateItemRowToItem(r gen.CreateMediaItemRow) media.Item {
	return dbconv.GenCreateItemRowToItem(r)
}
func genListItemsRowToItem(r gen.ListMediaItemsRow) media.Item {
	return dbconv.GenListItemsRowToItem(r)
}
func genListMissingArtRowToItem(r gen.ListMediaItemsMissingArtRow) media.Item {
	return dbconv.GenListMissingArtRowToItem(r)
}
func genListChildrenRowToItem(r gen.ListMediaItemChildrenRow) media.Item {
	return dbconv.GenListChildrenRowToItem(r)
}
func genSearchRowToItem(r gen.SearchMediaItemsRow) media.Item { return dbconv.GenSearchRowToItem(r) }
func genUpdateItemRowToItem(r gen.UpdateMediaItemMetadataRow) media.Item {
	return dbconv.GenUpdateItemRowToItem(r)
}
func createItemParamsToGen(p media.CreateItemParams) gen.CreateMediaItemParams {
	return dbconv.CreateItemParamsToGen(p)
}
func updateItemMetadataParamsToGen(p media.UpdateItemMetadataParams) gen.UpdateMediaItemMetadataParams {
	return dbconv.UpdateItemMetadataParamsToGen(p)
}
func genMediaFileToFile(f gen.MediaFile) media.File { return dbconv.GenMediaFileToFile(f) }
func createFileParamsToGen(p media.CreateFileParams) gen.CreateMediaFileParams {
	return dbconv.CreateFileParamsToGen(p)
}
