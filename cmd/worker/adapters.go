// cmd/worker/adapters.go — bridges gen.Queries to domain interfaces for the worker.
// Only the subset needed by the worker is duplicated here; the canonical copy
// lives in cmd/server/adapter.go.
package main

import (
	"context"
	"time"

	"github.com/google/uuid"
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

func timePtrToPGTZ(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
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

func durationToPtr(d time.Duration) *time.Duration {
	return &d
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

func (a *mediaAdapter) SoftDeleteMediaItem(ctx context.Context, id uuid.UUID) error {
	return a.q.SoftDeleteMediaItem(ctx, id)
}

func (a *mediaAdapter) SoftDeleteMediaItemIfAllFilesDeleted(ctx context.Context, id uuid.UUID) error {
	return a.q.SoftDeleteMediaItemIfAllFilesDeleted(ctx, id)
}

func (a *mediaAdapter) CountMediaItems(ctx context.Context, libraryID uuid.UUID, itemType string) (int64, error) {
	return a.q.CountMediaItems(ctx, gen.CountMediaItemsParams{
		LibraryID: libraryID,
		Type:      itemType,
	})
}

func (a *mediaAdapter) SearchMediaItems(ctx context.Context, libraryID uuid.UUID, query string, limit int32) ([]media.Item, error) {
	rows, err := a.q.SearchMediaItems(ctx, gen.SearchMediaItemsParams{
		LibraryID:      libraryID,
		PlaintoTsquery: query,
		Limit:          limit,
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

// ── sessionCleanupAdapter ─────────────────────────────────────────────────────

// sessionCleanupAdapter wraps gen.Queries to implement worker.SessionCleanupService.
type sessionCleanupAdapter struct{ q *gen.Queries }

func (a *sessionCleanupAdapter) DeleteExpiredSessions(ctx context.Context) error {
	return a.q.DeleteExpiredSessions(ctx)
}
