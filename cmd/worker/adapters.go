// cmd/worker/adapters.go — bridges gen.Queries to domain interfaces for the worker.
// Only the subset needed by the worker is duplicated here; the canonical copy
// lives in cmd/server/adapter.go.
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

func pgtypeDate(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time.UTC()
	return &t
}

func timePtrToPGDate(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *t, Valid: true}
}

func pgtypeUUID(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	id := uuid.UUID(u.Bytes)
	return &id
}

func uuidPtrToPGUUID(u *uuid.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*u), Valid: true}
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

func float64PtrToNumeric(f *float64) pgtype.Numeric {
	if f == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	_ = n.Scan(*f)
	return n
}

func int32PtrToIntPtr(i *int32) *int {
	if i == nil {
		return nil
	}
	v := int(*i)
	return &v
}

func intPtrToInt32Ptr(i *int) *int32 {
	if i == nil {
		return nil
	}
	v := int32(*i)
	return &v
}

// ── Media item conversions ────────────────────────────────────────────────────

func itemFromGenFields(
	id, libraryID uuid.UUID, typ, title, sortTitle string,
	originalTitle *string, year *int32, summary, tagline *string,
	rating, audienceRating pgtype.Numeric, contentRating *string, durationMs *int64,
	genres, tags []string, tmdbID, tvdbID *int32, imdbID *string,
	parentID pgtype.UUID, idx *int32, posterPath, fanartPath, thumbPath *string,
	originallyAvailableAt pgtype.Date,
	createdAt, updatedAt, deletedAt pgtype.Timestamptz,
) media.Item {
	return media.Item{
		ID:                    id,
		LibraryID:             libraryID,
		Type:                  typ,
		Title:                 title,
		SortTitle:             sortTitle,
		OriginalTitle:         originalTitle,
		Year:                  int32PtrToIntPtr(year),
		Summary:               summary,
		Tagline:               tagline,
		Rating:                numericToFloat64Ptr(rating),
		AudienceRating:        numericToFloat64Ptr(audienceRating),
		ContentRating:         contentRating,
		DurationMS:            durationMs,
		Genres:                genres,
		Tags:                  tags,
		TMDBID:                int32PtrToIntPtr(tmdbID),
		TVDBID:                int32PtrToIntPtr(tvdbID),
		IMDBID:                imdbID,
		ParentID:              pgtypeUUID(parentID),
		Index:                 int32PtrToIntPtr(idx),
		PosterPath:            posterPath,
		FanartPath:            fanartPath,
		ThumbPath:             thumbPath,
		OriginallyAvailableAt: pgtypeDate(originallyAvailableAt),
		CreatedAt:             mustTimeTZ(createdAt),
		UpdatedAt:             mustTimeTZ(updatedAt),
		DeletedAt:             pgtimeTZ(deletedAt),
	}
}

func genGetItemRowToItem(r gen.GetMediaItemRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func genGetItemByTMDBIDRowToItem(r gen.GetMediaItemByTMDBIDRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func genCreateItemRowToItem(r gen.CreateMediaItemRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func genListItemsRowToItem(r gen.ListMediaItemsRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func genListMissingArtRowToItem(r gen.ListMediaItemsMissingArtRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func genListChildrenRowToItem(r gen.ListMediaItemChildrenRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func genSearchRowToItem(r gen.SearchMediaItemsRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func genUpdateItemRowToItem(r gen.UpdateMediaItemMetadataRow) media.Item {
	return itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func createItemParamsToGen(p media.CreateItemParams) gen.CreateMediaItemParams {
	return gen.CreateMediaItemParams{
		LibraryID:             p.LibraryID,
		Type:                  p.Type,
		Title:                 p.Title,
		SortTitle:             p.SortTitle,
		OriginalTitle:         p.OriginalTitle,
		Year:                  intPtrToInt32Ptr(p.Year),
		Summary:               p.Summary,
		Tagline:               p.Tagline,
		Rating:                float64PtrToNumeric(p.Rating),
		AudienceRating:        float64PtrToNumeric(p.AudienceRating),
		ContentRating:         p.ContentRating,
		DurationMs:            p.DurationMS,
		Genres:                p.Genres,
		Tags:                  p.Tags,
		TmdbID:                intPtrToInt32Ptr(p.TMDBID),
		TvdbID:                intPtrToInt32Ptr(p.TVDBID),
		ImdbID:                p.IMDBID,
		ParentID:              uuidPtrToPGUUID(p.ParentID),
		Index:                 intPtrToInt32Ptr(p.Index),
		PosterPath:            p.PosterPath,
		FanartPath:            p.FanartPath,
		ThumbPath:             p.ThumbPath,
		OriginallyAvailableAt: timePtrToPGDate(p.OriginallyAvailableAt),
	}
}

func updateItemMetadataParamsToGen(p media.UpdateItemMetadataParams) gen.UpdateMediaItemMetadataParams {
	return gen.UpdateMediaItemMetadataParams{
		ID:                    p.ID,
		Title:                 p.Title,
		SortTitle:             p.SortTitle,
		OriginalTitle:         p.OriginalTitle,
		Year:                  intPtrToInt32Ptr(p.Year),
		Summary:               p.Summary,
		Tagline:               p.Tagline,
		Rating:                float64PtrToNumeric(p.Rating),
		AudienceRating:        float64PtrToNumeric(p.AudienceRating),
		ContentRating:         p.ContentRating,
		DurationMs:            p.DurationMS,
		Genres:                p.Genres,
		Tags:                  p.Tags,
		PosterPath:            p.PosterPath,
		FanartPath:            p.FanartPath,
		ThumbPath:             p.ThumbPath,
		OriginallyAvailableAt: timePtrToPGDate(p.OriginallyAvailableAt),
		TmdbID:                intPtrToInt32Ptr(p.TMDBID),
		TvdbID:                intPtrToInt32Ptr(p.TVDBID),
	}
}

// ── Media file conversions ────────────────────────────────────────────────────

func genMediaFileToFile(f gen.MediaFile) media.File {
	var frameRate *float64
	if f8, err := f.FrameRate.Float64Value(); err == nil && f8.Valid {
		fr := f8.Float64
		frameRate = &fr
	}
	return media.File{
		ID:              f.ID,
		MediaItemID:     f.MediaItemID,
		FilePath:        f.FilePath,
		FileSize:        f.FileSize,
		Container:       f.Container,
		VideoCodec:      f.VideoCodec,
		AudioCodec:      f.AudioCodec,
		ResolutionW:     int32PtrToIntPtr(f.ResolutionW),
		ResolutionH:     int32PtrToIntPtr(f.ResolutionH),
		Bitrate:         f.Bitrate,
		HDRType:         f.HdrType,
		FrameRate:       frameRate,
		AudioStreams:    f.AudioStreams,
		SubtitleStreams: f.SubtitleStreams,
		Chapters:        f.Chapters,
		FileHash:        f.FileHash,
		Status:          f.Status,
		MissingSince:    pgtimeTZ(f.MissingSince),
		ScannedAt:       mustTimeTZ(f.ScannedAt),
		CreatedAt:       mustTimeTZ(f.CreatedAt),
	}
}

func createFileParamsToGen(p media.CreateFileParams) gen.CreateMediaFileParams {
	var frameRate pgtype.Numeric
	if p.FrameRate != nil {
		_ = frameRate.Scan(*p.FrameRate)
	}
	return gen.CreateMediaFileParams{
		MediaItemID:     p.MediaItemID,
		FilePath:        p.FilePath,
		FileSize:        p.FileSize,
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
		FileHash:        p.FileHash,
	}
}

// ── mediaAdapter ──────────────────────────────────────────────────────────────

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
	// AddCollectionItem uses ON CONFLICT DO NOTHING; pgx surfaces the
	// no-row case as ErrNoRows. Treat as a successful no-op.
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
	return a.q.UpsertPhotoMetadata(ctx, out)
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

// Stub methods for media.Querier — worker doesn't need filtered listing.
func (a *mediaAdapter) ListMediaItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32, f media.FilterParams) ([]media.Item, error) {
	return a.ListMediaItems(ctx, libraryID, itemType, limit, offset)
}
func (a *mediaAdapter) CountMediaItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, f media.FilterParams) (int64, error) {
	return a.CountMediaItems(ctx, libraryID, itemType)
}
func (a *mediaAdapter) ListDistinctGenres(ctx context.Context, libraryID uuid.UUID) ([]string, error) {
	return nil, nil
}

func (a *mediaAdapter) ListGenresWithCounts(ctx context.Context, libraryID uuid.UUID, itemType string) ([]media.GenreCount, error) {
	return nil, nil
}

func (a *mediaAdapter) ListYearsWithCounts(ctx context.Context, libraryID uuid.UUID, itemType string) ([]media.YearCount, error) {
	return nil, nil
}

// Photo browse queries — worker doesn't surface photo lists, but the
// interface is shared with the server adapter so we stub them.
func (a *mediaAdapter) ListPhotosByLibrary(ctx context.Context, _ media.ListPhotosParams) ([]media.PhotoListItem, error) {
	return nil, nil
}
func (a *mediaAdapter) CountPhotosByLibrary(ctx context.Context, _ media.ListPhotosParams) (int64, error) {
	return 0, nil
}
func (a *mediaAdapter) ListPhotoTimelineBuckets(ctx context.Context, _ uuid.UUID) ([]media.PhotoTimelineBucket, error) {
	return nil, nil
}
func (a *mediaAdapter) ListPhotoMapPoints(ctx context.Context, _ media.ListPhotoMapPointsParams) ([]media.PhotoMapPoint, error) {
	return nil, nil
}
func (a *mediaAdapter) CountPhotoMapPoints(ctx context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (a *mediaAdapter) SearchPhotosByExif(ctx context.Context, _ media.SearchPhotosByExifParams) ([]media.PhotoSearchResult, error) {
	return nil, nil
}
func (a *mediaAdapter) CountPhotosByExif(ctx context.Context, _ media.SearchPhotosByExifParams) (int64, error) {
	return 0, nil
}

// ── sessionCleanupAdapter ─────────────────────────────────────────────────────

// sessionCleanupAdapter wraps gen.Queries to implement worker.SessionCleanupService.
type sessionCleanupAdapter struct{ q *gen.Queries }

func (a *sessionCleanupAdapter) DeleteExpiredSessions(ctx context.Context) error {
	return a.q.DeleteExpiredSessions(ctx)
}
