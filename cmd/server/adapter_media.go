package main

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

type mediaAdapter struct{ q *gen.Queries }

func (a *mediaAdapter) GetMediaItem(ctx context.Context, id uuid.UUID) (media.Item, error) {
	r, err := a.q.GetMediaItem(ctx, id)
	if err != nil {
		return media.Item{}, err
	}
	return genGetItemRowToItem(r), nil
}

func (a *mediaAdapter) GetMediaItemByTMDBID(ctx context.Context, libraryID uuid.UUID, tmdbID int) (media.Item, error) {
	id32 := int32(tmdbID)
	r, err := a.q.GetMediaItemByTMDBID(ctx, gen.GetMediaItemByTMDBIDParams{
		LibraryID: libraryID,
		TmdbID:    &id32,
	})
	if err != nil {
		return media.Item{}, err
	}
	return genGetItemByTMDBIDRowToItem(r), nil
}

func (a *mediaAdapter) ListMediaItems(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32) ([]media.Item, error) {
	rows, err := a.q.ListMediaItems(ctx, gen.ListMediaItemsParams{
		LibraryID: libraryID,
		Type:      itemType,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]media.Item, len(rows))
	for i, r := range rows {
		out[i] = genListItemsRowToItem(r)
	}
	return out, nil
}

func (a *mediaAdapter) ListMediaItemsMissingArt(ctx context.Context, limit int32) ([]media.Item, error) {
	rows, err := a.q.ListMediaItemsMissingArt(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]media.Item, len(rows))
	for i, r := range rows {
		out[i] = genListMissingArtRowToItem(r)
	}
	return out, nil
}

func (a *mediaAdapter) ListMediaItemChildren(ctx context.Context, parentID uuid.UUID) ([]media.Item, error) {
	rows, err := a.q.ListMediaItemChildren(ctx, pgtype.UUID{Bytes: [16]byte(parentID), Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]media.Item, len(rows))
	for i, r := range rows {
		out[i] = genListChildrenRowToItem(r)
	}
	return out, nil
}

func (a *mediaAdapter) CreateMediaItem(ctx context.Context, p media.CreateItemParams) (media.Item, error) {
	r, err := a.q.CreateMediaItem(ctx, createItemParamsToGen(p))
	if err != nil {
		return media.Item{}, err
	}
	return genCreateItemRowToItem(r), nil
}

func (a *mediaAdapter) UpdateMediaItemMetadata(ctx context.Context, p media.UpdateItemMetadataParams) (media.Item, error) {
	r, err := a.q.UpdateMediaItemMetadata(ctx, updateItemMetadataParamsToGen(p))
	if err != nil {
		return media.Item{}, err
	}
	return genUpdateItemRowToItem(r), nil
}

func (a *mediaAdapter) UpdateMediaItemLyrics(ctx context.Context, id uuid.UUID, plain, synced *string) error {
	return a.q.UpdateMediaItemLyrics(ctx, gen.UpdateMediaItemLyricsParams{
		ID:           id,
		LyricsPlain:  plain,
		LyricsSynced: synced,
	})
}

func (a *mediaAdapter) SoftDeleteMediaItem(ctx context.Context, id uuid.UUID) error {
	return a.q.SoftDeleteMediaItem(ctx, id)
}

func (a *mediaAdapter) SoftDeleteMediaItemIfAllFilesDeleted(ctx context.Context, id uuid.UUID) error {
	return a.q.SoftDeleteMediaItemIfAllFilesDeleted(ctx, id)
}

func (a *mediaAdapter) RestoreMediaItemAncestry(ctx context.Context, id uuid.UUID) error {
	return a.q.RestoreMediaItemAncestry(ctx, id)
}

func (a *mediaAdapter) CountMediaItems(ctx context.Context, libraryID uuid.UUID, itemType string) (int64, error) {
	return a.q.CountMediaItems(ctx, gen.CountMediaItemsParams{
		LibraryID: libraryID,
		Type:      itemType,
	})
}

func (a *mediaAdapter) SearchMediaItems(ctx context.Context, libraryID uuid.UUID, query string, limit int32) ([]media.Item, error) {
	rows, err := a.q.SearchMediaItems(ctx, gen.SearchMediaItemsParams{
		LibraryID:          libraryID,
		WebsearchToTsquery: query,
		Limit:              limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]media.Item, len(rows))
	for i, r := range rows {
		out[i] = genSearchRowToItem(r)
	}
	return out, nil
}

func (a *mediaAdapter) FindTopLevelItemByTitleYear(ctx context.Context, libraryID uuid.UUID, itemType, title string, year *int) (*media.Item, error) {
	row, err := a.q.FindTopLevelItemByTitleYear(ctx, gen.FindTopLevelItemByTitleYearParams{
		LibraryID: libraryID,
		Type:      itemType,
		Title:     title,
		Year:      intPtrToInt32Ptr(year),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	item := itemFromGenFields(row.ID, row.LibraryID, row.Type, row.Title, row.SortTitle,
		row.OriginalTitle, row.Year, row.Summary, row.Tagline,
		row.Rating, row.AudienceRating, row.ContentRating, row.DurationMs,
		row.Genres, row.Tags, row.TmdbID, row.TvdbID, row.ImdbID,
		row.ParentID, row.Index, row.PosterPath, row.FanartPath, row.ThumbPath,
		row.OriginallyAvailableAt, row.CreatedAt, row.UpdatedAt, row.DeletedAt)
	return &item, nil
}

func (a *mediaAdapter) FindTopLevelItemsByTitleFlexible(ctx context.Context, libraryID uuid.UUID, itemType, title string) ([]media.Item, error) {
	rows, err := a.q.FindTopLevelItemsByTitleFlexible(ctx, gen.FindTopLevelItemsByTitleFlexibleParams{
		LibraryID: libraryID,
		Type:      itemType,
		Lower:     title,
	})
	if err != nil {
		return nil, err
	}
	out := make([]media.Item, len(rows))
	for i, r := range rows {
		out[i] = itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
			r.OriginalTitle, r.Year, r.Summary, r.Tagline,
			r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
			r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
			r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
			r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
	}
	return out, nil
}

func (a *mediaAdapter) ListDuplicateTopLevelItems(ctx context.Context, itemType string, libraryID *uuid.UUID) ([]media.DuplicatePair, error) {
	var libParam pgtype.UUID
	if libraryID != nil {
		libParam = pgtype.UUID{Bytes: [16]byte(*libraryID), Valid: true}
	}
	rows, err := a.q.ListDuplicateTopLevelItems(ctx, gen.ListDuplicateTopLevelItemsParams{
		Type:      itemType,
		LibraryID: libParam,
	})
	if err != nil {
		return nil, err
	}
	out := make([]media.DuplicatePair, len(rows))
	for i, r := range rows {
		out[i] = media.DuplicatePair{LoserID: r.LoserID, SurvivorID: r.SurvivorID}
	}
	return out, nil
}

func (a *mediaAdapter) ListPrefixDuplicateTopLevelItems(ctx context.Context, itemType string, libraryID *uuid.UUID) ([]media.DuplicatePair, error) {
	var libParam pgtype.UUID
	if libraryID != nil {
		libParam = pgtype.UUID{Bytes: [16]byte(*libraryID), Valid: true}
	}
	rows, err := a.q.ListPrefixDuplicateTopLevelItems(ctx, gen.ListPrefixDuplicateTopLevelItemsParams{
		Type:      itemType,
		LibraryID: libParam,
	})
	if err != nil {
		return nil, err
	}
	out := make([]media.DuplicatePair, len(rows))
	for i, r := range rows {
		out[i] = media.DuplicatePair{LoserID: r.LoserID, SurvivorID: r.SurvivorID}
	}
	return out, nil
}

func (a *mediaAdapter) ListDuplicateChildItems(ctx context.Context, itemType string, parentID *uuid.UUID) ([]media.DuplicatePair, error) {
	var parentParam pgtype.UUID
	if parentID != nil {
		parentParam = pgtype.UUID{Bytes: [16]byte(*parentID), Valid: true}
	}
	rows, err := a.q.ListDuplicateChildItems(ctx, gen.ListDuplicateChildItemsParams{
		Type:     itemType,
		ParentID: parentParam,
	})
	if err != nil {
		return nil, err
	}
	out := make([]media.DuplicatePair, len(rows))
	for i, r := range rows {
		out[i] = media.DuplicatePair{LoserID: r.LoserID, SurvivorID: r.SurvivorID}
	}
	return out, nil
}

func (a *mediaAdapter) ListLibraryAudiobookDuplicates(ctx context.Context, libraryID uuid.UUID) ([]media.DuplicatePair, error) {
	rows, err := a.q.ListLibraryAudiobookDuplicates(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	out := make([]media.DuplicatePair, len(rows))
	for i, r := range rows {
		out[i] = media.DuplicatePair{LoserID: r.LoserID, SurvivorID: r.SurvivorID}
	}
	return out, nil
}

func (a *mediaAdapter) ListPhantomAudiobooks(ctx context.Context, libraryID uuid.UUID) ([]uuid.UUID, error) {
	return a.q.ListPhantomAudiobooks(ctx, libraryID)
}

func (a *mediaAdapter) ListEmptyBookAuthors(ctx context.Context, libraryID uuid.UUID) ([]uuid.UUID, error) {
	return a.q.ListEmptyBookAuthors(ctx, libraryID)
}

func (a *mediaAdapter) ListCollabArtistMerges(ctx context.Context, libraryID *uuid.UUID) ([]media.DuplicatePair, error) {
	var libParam pgtype.UUID
	if libraryID != nil {
		libParam = pgtype.UUID{Bytes: [16]byte(*libraryID), Valid: true}
	}
	rows, err := a.q.ListCollabArtistMerges(ctx, libParam)
	if err != nil {
		return nil, err
	}
	out := make([]media.DuplicatePair, len(rows))
	for i, r := range rows {
		out[i] = media.DuplicatePair{LoserID: r.LoserID, SurvivorID: r.SurvivorID}
	}
	return out, nil
}

func (a *mediaAdapter) ReparentMediaItem(ctx context.Context, id uuid.UUID, newParent *uuid.UUID) error {
	var p pgtype.UUID
	if newParent != nil {
		p = pgtype.UUID{Bytes: [16]byte(*newParent), Valid: true}
	}
	return a.q.ReparentMediaItem(ctx, gen.ReparentMediaItemParams{ID: id, ParentID: p})
}

func (a *mediaAdapter) ReparentMediaFilesByItem(ctx context.Context, fromItemID, toItemID uuid.UUID) error {
	return a.q.ReparentMediaFilesByItem(ctx, gen.ReparentMediaFilesByItemParams{
		MediaItemID:   fromItemID,
		MediaItemID_2: toItemID,
	})
}

func (a *mediaAdapter) GetMediaFile(ctx context.Context, id uuid.UUID) (media.File, error) {
	f, err := a.q.GetMediaFile(ctx, id)
	if err != nil {
		return media.File{}, err
	}
	return genMediaFileToFile(f), nil
}

func (a *mediaAdapter) GetMediaFileByPath(ctx context.Context, path string) (media.File, error) {
	f, err := a.q.GetMediaFileByPath(ctx, path)
	if err != nil {
		return media.File{}, err
	}
	return genMediaFileToFile(f), nil
}

func (a *mediaAdapter) GetMediaFileByHash(ctx context.Context, hash string) (media.File, error) {
	f, err := a.q.GetMediaFileByHash(ctx, &hash)
	if err != nil {
		return media.File{}, err
	}
	return genMediaFileToFile(f), nil
}

func (a *mediaAdapter) ListMediaFilesForItem(ctx context.Context, itemID uuid.UUID) ([]media.File, error) {
	fs, err := a.q.ListMediaFilesForItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]media.File, len(fs))
	for i, f := range fs {
		out[i] = genMediaFileToFile(f)
	}
	return out, nil
}

func (a *mediaAdapter) CreateMediaFile(ctx context.Context, p media.CreateFileParams) (media.File, error) {
	f, err := a.q.CreateMediaFile(ctx, createFileParamsToGen(p))
	if err != nil {
		return media.File{}, err
	}
	return genMediaFileToFile(f), nil
}

func (a *mediaAdapter) UpdateMediaFilePath(ctx context.Context, id uuid.UUID, newPath string) error {
	return a.q.UpdateMediaFilePath(ctx, gen.UpdateMediaFilePathParams{ID: id, FilePath: newPath})
}

func (a *mediaAdapter) UpdateMediaFileTechnicalMetadata(ctx context.Context, id uuid.UUID, p media.CreateFileParams) error {
	var frameRate pgtype.Numeric
	if p.FrameRate != nil {
		_ = frameRate.Scan(*p.FrameRate)
	}
	return a.q.UpdateMediaFileTechnicalMetadata(ctx, gen.UpdateMediaFileTechnicalMetadataParams{
		ID:              id,
		Container:       p.Container,
		VideoCodec:      p.VideoCodec,
		AudioCodec:      p.AudioCodec,
		ResolutionW:     intPtrToInt32Ptr(p.ResolutionW),
		ResolutionH:     intPtrToInt32Ptr(p.ResolutionH),
		Bitrate:         p.Bitrate,
		HdrType:         p.HDRType,
		FrameRate:       frameRate,
		AudioStreams:    p.AudioStreams,
		SubtitleStreams: p.SubtitleStreams,
		Chapters:        p.Chapters,
		DurationMs:      p.DurationMS,
	})
}

func (a *mediaAdapter) MarkMediaFileMissing(ctx context.Context, id uuid.UUID) error {
	return a.q.MarkMediaFileMissing(ctx, id)
}

func (a *mediaAdapter) MarkMediaFileActive(ctx context.Context, id uuid.UUID) error {
	return a.q.MarkMediaFileActive(ctx, id)
}

func (a *mediaAdapter) MarkMediaFileDeleted(ctx context.Context, id uuid.UUID) error {
	return a.q.MarkMediaFileDeleted(ctx, id)
}

func (a *mediaAdapter) UpdateMediaFileHash(ctx context.Context, id uuid.UUID, hash string) error {
	return a.q.UpdateMediaFileHash(ctx, gen.UpdateMediaFileHashParams{ID: id, FileHash: &hash})
}

func (a *mediaAdapter) UpdateMediaFileItemID(ctx context.Context, id uuid.UUID, itemID uuid.UUID) error {
	return a.q.UpdateMediaFileItemID(ctx, gen.UpdateMediaFileItemIDParams{ID: id, MediaItemID: itemID})
}

func (a *mediaAdapter) ListActiveFilesForLibrary(ctx context.Context, libraryID uuid.UUID) ([]media.File, error) {
	fs, err := a.q.ListActiveFilesForLibrary(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	out := make([]media.File, len(fs))
	for i, f := range fs {
		out[i] = genMediaFileToFile(f)
	}
	return out, nil
}

func (a *mediaAdapter) DeleteMissingFilesByLibrary(ctx context.Context, libraryID uuid.UUID) error {
	return a.q.DeleteMissingFilesByLibrary(ctx, libraryID)
}

func (a *mediaAdapter) HardDeleteSoftDeletedFilesByLibrary(ctx context.Context, libraryID uuid.UUID) (int64, error) {
	return a.q.HardDeleteSoftDeletedFilesByLibrary(ctx, libraryID)
}

func (a *mediaAdapter) GetMediaItemEnrichAttemptedAt(ctx context.Context, id uuid.UUID) (*time.Time, error) {
	ts, err := a.q.GetMediaItemEnrichAttemptedAt(ctx, id)
	if err != nil {
		return nil, err
	}
	return pgtimeTZ(ts), nil
}

func (a *mediaAdapter) TouchMediaItemEnrichAttempt(ctx context.Context, id uuid.UUID) error {
	return a.q.TouchMediaItemEnrichAttempt(ctx, id)
}

func (a *mediaAdapter) SoftDeleteItemsWithNoActiveFiles(ctx context.Context, libraryID uuid.UUID) error {
	return a.q.SoftDeleteItemsWithNoActiveFiles(ctx, libraryID)
}

func (a *mediaAdapter) SoftDeleteEmptyContainerItems(ctx context.Context, libraryID uuid.UUID) error {
	return a.q.SoftDeleteEmptyContainerItems(ctx, libraryID)
}

func (a *mediaAdapter) UpsertEventCollection(ctx context.Context, libraryID uuid.UUID, name string) (uuid.UUID, error) {
	row, err := a.q.UpsertEventCollection(ctx, gen.UpsertEventCollectionParams{
		LibraryID: pgtype.UUID{Bytes: [16]byte(libraryID), Valid: true},
		Name:      name,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return row.ID, nil
}

func (a *mediaAdapter) AddItemToCollection(ctx context.Context, collectionID, mediaItemID uuid.UUID) error {
	_, err := a.q.AddCollectionItem(ctx, gen.AddCollectionItemParams{
		CollectionID: collectionID,
		MediaItemID:  mediaItemID,
	})
	// AddCollectionItem uses ON CONFLICT DO NOTHING, so the RETURNING
	// clause yields no row when the pair already exists. pgx surfaces
	// that as ErrNoRows; treat it as a successful no-op since the
	// caller's intent ("ensure this item is in the collection") is
	// already satisfied.
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func (a *mediaAdapter) ListEventCollectionsForLibrary(ctx context.Context, libraryID uuid.UUID) ([]media.EventCollection, error) {
	rows, err := a.q.ListEventCollectionsForLibrary(ctx, pgtype.UUID{Bytes: [16]byte(libraryID), Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]media.EventCollection, 0, len(rows))
	for _, r := range rows {
		var libID uuid.UUID
		if r.LibraryID.Valid {
			libID = uuid.UUID(r.LibraryID.Bytes)
		}
		out = append(out, media.EventCollection{
			ID:         r.ID,
			LibraryID:  libID,
			Name:       r.Name,
			PosterPath: r.PosterPath,
		})
	}
	return out, nil
}

func (a *mediaAdapter) UpsertPhotoMetadata(ctx context.Context, p media.PhotoMetadataParams) error {
	return a.q.UpsertPhotoMetadata(ctx, photoMetadataParamsToGen(p))
}

func (a *mediaAdapter) GetPhotoMetadata(ctx context.Context, itemID uuid.UUID) (*media.PhotoMetadata, error) {
	row, err := a.q.GetPhotoMetadata(ctx, itemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, media.ErrNotFound
		}
		return nil, err
	}
	out := &media.PhotoMetadata{
		ItemID:        row.ItemID,
		CameraMake:    row.CameraMake,
		CameraModel:   row.CameraModel,
		LensModel:     row.LensModel,
		FocalLengthMM: row.FocalLengthMm,
		Aperture:      row.Aperture,
		ShutterSpeed:  row.ShutterSpeed,
		ISO:           row.Iso,
		Flash:         row.Flash,
		Orientation:   row.Orientation,
		Width:         row.Width,
		Height:        row.Height,
		GPSLat:        row.GpsLat,
		GPSLon:        row.GpsLon,
		GPSAlt:        row.GpsAlt,
	}
	if row.TakenAt.Valid {
		t := row.TakenAt.Time
		out.TakenAt = &t
	}
	return out, nil
}

func (a *mediaAdapter) ListPhotosByLibrary(ctx context.Context, p media.ListPhotosParams) ([]media.PhotoListItem, error) {
	rows, err := a.q.ListPhotosByLibrary(ctx, gen.ListPhotosByLibraryParams{
		LibraryID: p.LibraryID,
		Limit:     p.Limit,
		Offset:    p.Offset,
		From:      tsOrNull(p.From),
		To:        tsOrNull(p.To),
	})
	if err != nil {
		return nil, err
	}
	out := make([]media.PhotoListItem, len(rows))
	for i, r := range rows {
		item := media.PhotoListItem{
			ID:          r.ID,
			LibraryID:   r.LibraryID,
			Title:       r.Title,
			PosterPath:  r.PosterPath,
			CameraMake:  r.CameraMake,
			CameraModel: r.CameraModel,
			Width:       r.Width,
			Height:      r.Height,
			Orientation: r.Orientation,
		}
		if r.CreatedAt.Valid {
			item.CreatedAt = r.CreatedAt.Time
		}
		if r.UpdatedAt.Valid {
			item.UpdatedAt = r.UpdatedAt.Time
		}
		if r.TakenAt.Valid {
			t := r.TakenAt.Time
			item.TakenAt = &t
		}
		out[i] = item
	}
	return out, nil
}

func (a *mediaAdapter) CountPhotosByLibrary(ctx context.Context, p media.ListPhotosParams) (int64, error) {
	return a.q.CountPhotosByLibrary(ctx, gen.CountPhotosByLibraryParams{
		LibraryID: p.LibraryID,
		From:      tsOrNull(p.From),
		To:        tsOrNull(p.To),
	})
}

func (a *mediaAdapter) ListPhotoTimelineBuckets(ctx context.Context, libraryID uuid.UUID) ([]media.PhotoTimelineBucket, error) {
	rows, err := a.q.ListPhotoTimelineBuckets(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	out := make([]media.PhotoTimelineBucket, len(rows))
	for i, r := range rows {
		out[i] = media.PhotoTimelineBucket{
			Year:  r.Year,
			Month: r.Month,
			Count: r.Count,
		}
	}
	return out, nil
}

func (a *mediaAdapter) ListPhotoMapPoints(ctx context.Context, p media.ListPhotoMapPointsParams) ([]media.PhotoMapPoint, error) {
	rows, err := a.q.ListPhotoMapPoints(ctx, gen.ListPhotoMapPointsParams{
		LibraryID: p.LibraryID,
		Limit:     p.Limit,
		MinLat:    p.MinLat,
		MaxLat:    p.MaxLat,
		MinLon:    p.MinLon,
		MaxLon:    p.MaxLon,
	})
	if err != nil {
		return nil, err
	}
	out := make([]media.PhotoMapPoint, 0, len(rows))
	for _, r := range rows {
		// gps_lat/lon are NOT NULL filtered in SQL — but the generated row
		// still types them as *float64 because the column is nullable. Skip
		// the row defensively if either is unexpectedly nil rather than
		// dereferencing a nil pointer.
		if r.GpsLat == nil || r.GpsLon == nil {
			continue
		}
		var takenAt *time.Time
		if r.TakenAt.Valid {
			t := r.TakenAt.Time
			takenAt = &t
		}
		out = append(out, media.PhotoMapPoint{
			ID:         r.ID,
			LibraryID:  r.LibraryID,
			Title:      r.Title,
			PosterPath: r.PosterPath,
			Lat:        *r.GpsLat,
			Lon:        *r.GpsLon,
			TakenAt:    takenAt,
			CreatedAt:  r.CreatedAt.Time,
		})
	}
	return out, nil
}

func (a *mediaAdapter) CountPhotoMapPoints(ctx context.Context, libraryID uuid.UUID) (int64, error) {
	return a.q.CountPhotoMapPoints(ctx, libraryID)
}

func (a *mediaAdapter) SearchPhotosByExif(ctx context.Context, p media.SearchPhotosByExifParams) ([]media.PhotoSearchResult, error) {
	rows, err := a.q.SearchPhotosByExif(ctx, gen.SearchPhotosByExifParams{
		LibraryID:   p.LibraryID,
		Limit:       p.Limit,
		Offset:      p.Offset,
		CameraMake:  p.CameraMake,
		CameraModel: p.CameraModel,
		LensModel:   p.LensModel,
		ApertureMin: p.ApertureMin,
		ApertureMax: p.ApertureMax,
		IsoMin:      p.ISOMin,
		IsoMax:      p.ISOMax,
		FocalMin:    p.FocalMin,
		FocalMax:    p.FocalMax,
		From:        tsOrNull(p.From),
		To:          tsOrNull(p.To),
		HasGps:      p.HasGPS,
	})
	if err != nil {
		return nil, err
	}
	out := make([]media.PhotoSearchResult, len(rows))
	for i, r := range rows {
		item := media.PhotoSearchResult{
			ID:            r.ID,
			LibraryID:     r.LibraryID,
			Title:         r.Title,
			PosterPath:    r.PosterPath,
			CameraMake:    r.CameraMake,
			CameraModel:   r.CameraModel,
			LensModel:     r.LensModel,
			FocalLengthMM: r.FocalLengthMm,
			Aperture:      r.Aperture,
			ISO:           r.Iso,
			Width:         r.Width,
			Height:        r.Height,
			Orientation:   r.Orientation,
			GPSLat:        r.GpsLat,
			GPSLon:        r.GpsLon,
		}
		if r.CreatedAt.Valid {
			item.CreatedAt = r.CreatedAt.Time
		}
		if r.UpdatedAt.Valid {
			item.UpdatedAt = r.UpdatedAt.Time
		}
		if r.TakenAt.Valid {
			t := r.TakenAt.Time
			item.TakenAt = &t
		}
		out[i] = item
	}
	return out, nil
}

func (a *mediaAdapter) CountPhotosByExif(ctx context.Context, p media.SearchPhotosByExifParams) (int64, error) {
	return a.q.CountPhotosByExif(ctx, gen.CountPhotosByExifParams{
		LibraryID:   p.LibraryID,
		CameraMake:  p.CameraMake,
		CameraModel: p.CameraModel,
		LensModel:   p.LensModel,
		ApertureMin: p.ApertureMin,
		ApertureMax: p.ApertureMax,
		IsoMin:      p.ISOMin,
		IsoMax:      p.ISOMax,
		FocalMin:    p.FocalMin,
		FocalMax:    p.FocalMax,
		From:        tsOrNull(p.From),
		To:          tsOrNull(p.To),
		HasGps:      p.HasGPS,
	})
}

// tsOrNull converts an optional time pointer into the pgtype.Timestamptz
// that sqlc.narg generates as the bind parameter. nil → invalid (NULL).
func tsOrNull(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func photoMetadataParamsToGen(p media.PhotoMetadataParams) gen.UpsertPhotoMetadataParams {
	out := gen.UpsertPhotoMetadataParams{
		ItemID:        p.ItemID,
		CameraMake:    p.CameraMake,
		CameraModel:   p.CameraModel,
		LensModel:     p.LensModel,
		FocalLengthMm: p.FocalLengthMM,
		Aperture:      p.Aperture,
		ShutterSpeed:  p.ShutterSpeed,
		Iso:           p.ISO,
		Flash:         p.Flash,
		Orientation:   p.Orientation,
		Width:         p.Width,
		Height:        p.Height,
		GpsLat:        p.GPSLat,
		GpsLon:        p.GPSLon,
		GpsAlt:        p.GPSAlt,
		RawExif:       p.RawEXIF,
	}
	if p.TakenAt != nil {
		out.TakenAt = pgtype.Timestamptz{Time: *p.TakenAt, Valid: true}
	}
	return out
}

func (a *mediaAdapter) ListMissingFilesOlderThan(ctx context.Context, before time.Time) ([]media.File, error) {
	fs, err := a.q.ListMissingFilesOlderThan(ctx, pgtype.Timestamptz{Time: before, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]media.File, len(fs))
	for i, f := range fs {
		out[i] = genMediaFileToFile(f)
	}
	return out, nil
}

func (a *mediaAdapter) ListMediaItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32, f media.FilterParams) ([]media.Item, error) {
	p := gen.ListMediaItemsByTitleParams{
		LibraryID:     libraryID,
		Type:          itemType,
		Limit:         limit,
		Offset:        offset,
		Genre:         f.Genre,
		YearMin:       intPtrToInt32Ptr(f.YearMin),
		YearMax:       intPtrToInt32Ptr(f.YearMax),
		RatingMin:     float64PtrToNumeric(f.RatingMin),
		MaxRatingRank: intPtrToInt32Ptr(f.MaxRatingRank),
	}

	sort := f.Sort
	if sort == "" {
		sort = "title"
	}

	switch sort + "_" + boolDir(f.SortAsc) {
	case "title_asc":
		rows, err := a.q.ListMediaItemsByTitle(ctx, p)
		if err != nil {
			return nil, err
		}
		return convertFilteredRows(rows, genFilteredTitleRowToItem), nil

	case "title_desc":
		dp := gen.ListMediaItemsByTitleDescParams(p)
		rows, err := a.q.ListMediaItemsByTitleDesc(ctx, dp)
		if err != nil {
			return nil, err
		}
		return convertFilteredRows(rows, genFilteredTitleDescRowToItem), nil

	case "year_asc":
		yp := gen.ListMediaItemsByYearParams(p)
		rows, err := a.q.ListMediaItemsByYear(ctx, yp)
		if err != nil {
			return nil, err
		}
		return convertFilteredRows(rows, genFilteredYearRowToItem), nil

	case "year_desc":
		ydp := gen.ListMediaItemsByYearDescParams(p)
		rows, err := a.q.ListMediaItemsByYearDesc(ctx, ydp)
		if err != nil {
			return nil, err
		}
		return convertFilteredRows(rows, genFilteredYearDescRowToItem), nil

	case "rating_desc":
		rp := gen.ListMediaItemsByRatingParams(p)
		rows, err := a.q.ListMediaItemsByRating(ctx, rp)
		if err != nil {
			return nil, err
		}
		return convertFilteredRows(rows, genFilteredRatingRowToItem), nil

	case "rating_asc":
		rap := gen.ListMediaItemsByRatingAscParams(p)
		rows, err := a.q.ListMediaItemsByRatingAsc(ctx, rap)
		if err != nil {
			return nil, err
		}
		return convertFilteredRows(rows, genFilteredRatingAscRowToItem), nil

	case "created_at_desc":
		dap := gen.ListMediaItemsByDateAddedParams(p)
		rows, err := a.q.ListMediaItemsByDateAdded(ctx, dap)
		if err != nil {
			return nil, err
		}
		return convertFilteredRows(rows, genFilteredDateAddedRowToItem), nil

	case "created_at_asc":
		daap := gen.ListMediaItemsByDateAddedAscParams(p)
		rows, err := a.q.ListMediaItemsByDateAddedAsc(ctx, daap)
		if err != nil {
			return nil, err
		}
		return convertFilteredRows(rows, genFilteredDateAddedAscRowToItem), nil

	case "taken_at_desc":
		tap := gen.ListMediaItemsByTakenAtParams(p)
		rows, err := a.q.ListMediaItemsByTakenAt(ctx, tap)
		if err != nil {
			return nil, err
		}
		return convertFilteredRows(rows, genFilteredTakenAtRowToItem), nil

	case "taken_at_asc":
		taap := gen.ListMediaItemsByTakenAtAscParams(p)
		rows, err := a.q.ListMediaItemsByTakenAtAsc(ctx, taap)
		if err != nil {
			return nil, err
		}
		return convertFilteredRows(rows, genFilteredTakenAtAscRowToItem), nil

	default:
		rows, err := a.q.ListMediaItemsByTitle(ctx, p)
		if err != nil {
			return nil, err
		}
		return convertFilteredRows(rows, genFilteredTitleRowToItem), nil
	}
}

func boolDir(asc bool) string {
	if asc {
		return "asc"
	}
	return "desc"
}

func convertFilteredRows[T any](rows []T, conv func(T) media.Item) []media.Item {
	out := make([]media.Item, len(rows))
	for i, r := range rows {
		out[i] = conv(r)
	}
	return out
}

func genFilteredTitleRowToItem(r gen.ListMediaItemsByTitleRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}
func genFilteredTitleDescRowToItem(r gen.ListMediaItemsByTitleDescRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}
func genFilteredYearRowToItem(r gen.ListMediaItemsByYearRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}
func genFilteredYearDescRowToItem(r gen.ListMediaItemsByYearDescRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}
func genFilteredRatingRowToItem(r gen.ListMediaItemsByRatingRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}
func genFilteredRatingAscRowToItem(r gen.ListMediaItemsByRatingAscRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}
func genFilteredDateAddedRowToItem(r gen.ListMediaItemsByDateAddedRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}
func genFilteredDateAddedAscRowToItem(r gen.ListMediaItemsByDateAddedAscRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}
func genFilteredTakenAtRowToItem(r gen.ListMediaItemsByTakenAtRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}
func genFilteredTakenAtAscRowToItem(r gen.ListMediaItemsByTakenAtAscRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func (a *mediaAdapter) CountMediaItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, f media.FilterParams) (int64, error) {
	return a.q.CountMediaItemsFiltered(ctx, gen.CountMediaItemsFilteredParams{
		LibraryID:     libraryID,
		Type:          itemType,
		Genre:         f.Genre,
		YearMin:       intPtrToInt32Ptr(f.YearMin),
		YearMax:       intPtrToInt32Ptr(f.YearMax),
		RatingMin:     float64PtrToNumeric(f.RatingMin),
		MaxRatingRank: intPtrToInt32Ptr(f.MaxRatingRank),
	})
}

func (a *mediaAdapter) ListDistinctGenres(ctx context.Context, libraryID uuid.UUID) ([]string, error) {
	return a.q.ListDistinctGenres(ctx, libraryID)
}

func (a *mediaAdapter) ListGenresWithCounts(ctx context.Context, libraryID uuid.UUID, itemType string) ([]media.GenreCount, error) {
	rows, err := a.q.ListGenresWithCounts(ctx, gen.ListGenresWithCountsParams{LibraryID: libraryID, Type: itemType})
	if err != nil {
		return nil, err
	}
	out := make([]media.GenreCount, len(rows))
	for i, r := range rows {
		out[i] = media.GenreCount{Genre: r.Genre, Count: r.Count}
	}
	return out, nil
}

func (a *mediaAdapter) ListYearsWithCounts(ctx context.Context, libraryID uuid.UUID, itemType string) ([]media.YearCount, error) {
	rows, err := a.q.ListYearsWithCounts(ctx, gen.ListYearsWithCountsParams{LibraryID: libraryID, Type: itemType})
	if err != nil {
		return nil, err
	}
	out := make([]media.YearCount, len(rows))
	for i, r := range rows {
		out[i] = media.YearCount{Year: r.Year, Count: r.Count}
	}
	return out, nil
}
