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
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
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
	if *i < math.MinInt32 || *i > math.MaxInt32 {
		slog.Warn("intPtrToInt32Ptr: value out of int32 range, returning nil", "value", *i)
		return nil
	}
	v := int32(*i)
	return &v
}

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
	}
}

// ── Media item conversions ────────────────────────────────────────────────────

// itemFromGenFields converts the common field set shared by all item row types.
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

// applyItemMusicFields fills the music-only columns from a row that returns
// them. Used by GetMediaItem and CreateMediaItem; list/search rows don't carry
// these fields and leave them zero.
func applyItemMusicFields(
	item *media.Item,
	mbID, mbReleaseID, mbReleaseGroupID, mbArtistID, mbAlbumArtistID pgtype.UUID,
	discTotal, trackTotal, originalYear *int32,
	compilation bool, releaseType *string,
) {
	item.MusicBrainzID = pgtypeUUID(mbID)
	item.MusicBrainzReleaseID = pgtypeUUID(mbReleaseID)
	item.MusicBrainzReleaseGroupID = pgtypeUUID(mbReleaseGroupID)
	item.MusicBrainzArtistID = pgtypeUUID(mbArtistID)
	item.MusicBrainzAlbumArtistID = pgtypeUUID(mbAlbumArtistID)
	item.DiscTotal = int32PtrToIntPtr(discTotal)
	item.TrackTotal = int32PtrToIntPtr(trackTotal)
	item.OriginalYear = int32PtrToIntPtr(originalYear)
	item.Compilation = compilation
	item.ReleaseType = releaseType
}

func genGetItemRowToItem(r gen.GetMediaItemRow) media.Item {
	item := itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
	applyItemMusicFields(&item,
		r.MusicbrainzID, r.MusicbrainzReleaseID, r.MusicbrainzReleaseGroupID,
		r.MusicbrainzArtistID, r.MusicbrainzAlbumArtistID,
		r.DiscTotal, r.TrackTotal, r.OriginalYear,
		r.Compilation, r.ReleaseType)
	return item
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
	item := itemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
	applyItemMusicFields(&item,
		r.MusicbrainzID, r.MusicbrainzReleaseID, r.MusicbrainzReleaseGroupID,
		r.MusicbrainzArtistID, r.MusicbrainzAlbumArtistID,
		r.DiscTotal, r.TrackTotal, r.OriginalYear,
		r.Compilation, r.ReleaseType)
	return item
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
	var releaseType *string
	if p.ReleaseType != "" {
		rt := p.ReleaseType
		releaseType = &rt
	}
	return gen.CreateMediaItemParams{
		LibraryID:                 p.LibraryID,
		Type:                      p.Type,
		Title:                     p.Title,
		SortTitle:                 p.SortTitle,
		OriginalTitle:             p.OriginalTitle,
		Year:                      intPtrToInt32Ptr(p.Year),
		Summary:                   p.Summary,
		Tagline:                   p.Tagline,
		Rating:                    float64PtrToNumeric(p.Rating),
		AudienceRating:            float64PtrToNumeric(p.AudienceRating),
		ContentRating:             p.ContentRating,
		DurationMs:                p.DurationMS,
		Genres:                    p.Genres,
		Tags:                      p.Tags,
		TmdbID:                    intPtrToInt32Ptr(p.TMDBID),
		TvdbID:                    intPtrToInt32Ptr(p.TVDBID),
		ImdbID:                    p.IMDBID,
		MusicbrainzID:             uuidPtrToPGUUID(p.MusicBrainzID),
		MusicbrainzReleaseID:      uuidPtrToPGUUID(p.MusicBrainzReleaseID),
		MusicbrainzReleaseGroupID: uuidPtrToPGUUID(p.MusicBrainzReleaseGroupID),
		MusicbrainzArtistID:       uuidPtrToPGUUID(p.MusicBrainzArtistID),
		MusicbrainzAlbumArtistID:  uuidPtrToPGUUID(p.MusicBrainzAlbumArtistID),
		DiscTotal:                 intPtrToInt32Ptr(p.DiscTotal),
		TrackTotal:                intPtrToInt32Ptr(p.TrackTotal),
		OriginalYear:              intPtrToInt32Ptr(p.OriginalYear),
		Compilation:               p.Compilation,
		ReleaseType:               releaseType,
		ParentID:                  uuidPtrToPGUUID(p.ParentID),
		Index:                     intPtrToInt32Ptr(p.Index),
		PosterPath:                p.PosterPath,
		FanartPath:                p.FanartPath,
		ThumbPath:                 p.ThumbPath,
		OriginallyAvailableAt:     timePtrToPGDate(p.OriginallyAvailableAt),
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
		ID:                  f.ID,
		MediaItemID:         f.MediaItemID,
		FilePath:            f.FilePath,
		FileSize:            f.FileSize,
		Container:           f.Container,
		VideoCodec:          f.VideoCodec,
		AudioCodec:          f.AudioCodec,
		ResolutionW:         int32PtrToIntPtr(f.ResolutionW),
		ResolutionH:         int32PtrToIntPtr(f.ResolutionH),
		Bitrate:             f.Bitrate,
		HDRType:             f.HdrType,
		FrameRate:           frameRate,
		AudioStreams:        f.AudioStreams,
		SubtitleStreams:     f.SubtitleStreams,
		Chapters:            f.Chapters,
		FileHash:            f.FileHash,
		DurationMS:          f.DurationMs,
		BitDepth:            int32PtrToIntPtr(f.BitDepth),
		SampleRate:          int32PtrToIntPtr(f.SampleRate),
		ChannelLayout:       f.ChannelLayout,
		Lossless:            f.Lossless,
		ReplayGainTrackGain: numericToFloat64Ptr(f.ReplaygainTrackGain),
		ReplayGainTrackPeak: numericToFloat64Ptr(f.ReplaygainTrackPeak),
		ReplayGainAlbumGain: numericToFloat64Ptr(f.ReplaygainAlbumGain),
		ReplayGainAlbumPeak: numericToFloat64Ptr(f.ReplaygainAlbumPeak),
		Status:              f.Status,
		MissingSince:        pgtimeTZ(f.MissingSince),
		ScannedAt:           mustTimeTZ(f.ScannedAt),
		CreatedAt:           mustTimeTZ(f.CreatedAt),
	}
}

func createFileParamsToGen(p media.CreateFileParams) gen.CreateMediaFileParams {
	var frameRate pgtype.Numeric
	if p.FrameRate != nil {
		_ = frameRate.Scan(*p.FrameRate)
	}
	return gen.CreateMediaFileParams{
		MediaItemID:         p.MediaItemID,
		FilePath:            p.FilePath,
		FileSize:            p.FileSize,
		Container:           p.Container,
		VideoCodec:          p.VideoCodec,
		AudioCodec:          p.AudioCodec,
		ResolutionW:         intPtrToInt32Ptr(p.ResolutionW),
		ResolutionH:         intPtrToInt32Ptr(p.ResolutionH),
		Bitrate:             p.Bitrate,
		HdrType:             p.HDRType,
		FrameRate:           frameRate,
		AudioStreams:        p.AudioStreams,
		SubtitleStreams:     p.SubtitleStreams,
		Chapters:            p.Chapters,
		FileHash:            p.FileHash,
		DurationMs:          p.DurationMS,
		BitDepth:            intPtrToInt32Ptr(p.BitDepth),
		SampleRate:          intPtrToInt32Ptr(p.SampleRate),
		ChannelLayout:       p.ChannelLayout,
		Lossless:            p.Lossless,
		ReplaygainTrackGain: float64PtrToNumeric(p.ReplayGainTrackGain),
		ReplaygainTrackPeak: float64PtrToNumeric(p.ReplayGainTrackPeak),
		ReplaygainAlbumGain: float64PtrToNumeric(p.ReplayGainAlbumGain),
		ReplaygainAlbumPeak: float64PtrToNumeric(p.ReplayGainAlbumPeak),
	}
}
