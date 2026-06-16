// Package dbconv holds the pure gen.Queries ↔ domain-type converters shared by
// the server and worker DB adapters (cmd/server/adapter.go and
// cmd/worker/adapters.go).
//
// These conversions previously lived in full, hand-maintained copies in both
// adapter files. They drifted: the worker copy silently dropped bit-depth,
// duration, channel-layout and ReplayGain on the file read/write path and the
// MusicBrainz IDs / disc+track totals / reading-direction on the item path —
// a latent bug waiting for scanning/enrichment to move onto the worker. Both
// adapters now delegate here so the field mapping has exactly one home and can
// never diverge again.
//
// The converters take and return pgtype/gen values, so domain packages stay
// free of pgx imports — the adapters are still the only place that bridges the
// two, they just no longer each carry their own copy of the mapping.
package dbconv

import (
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// ── Low-level pgtype helpers ──────────────────────────────────────────────────

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

func Float64PtrToNumeric(f *float64) pgtype.Numeric {
	if f == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	// pgtype.Numeric.Scan rejects a raw float64 ("cannot scan float64") and
	// leaves the value invalid → the column is written NULL. Scan the decimal
	// string form, which it parses into its big.Int/Exp representation. Every
	// Numeric column we populate from a float (frame_rate, replaygain_*) was
	// silently NULL until this.
	if err := n.Scan(strconv.FormatFloat(*f, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}
	}
	return n
}

func int32PtrToIntPtr(i *int32) *int {
	if i == nil {
		return nil
	}
	v := int(*i)
	return &v
}

func IntPtrToInt32Ptr(i *int) *int32 {
	if i == nil {
		return nil
	}
	if *i < math.MinInt32 || *i > math.MaxInt32 {
		slog.Warn("IntPtrToInt32Ptr: value out of int32 range, returning nil", "value", *i)
		return nil
	}
	v := int32(*i)
	return &v
}

// ── Media item conversions ────────────────────────────────────────────────────

// ItemFromGenFields converts the common field set shared by all item row types.
func ItemFromGenFields(
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
// them. Used by the Get and Create row converters; list/search rows don't carry
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

func GenGetItemRowToItem(r gen.GetMediaItemRow) media.Item {
	item := ItemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
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
	item.AniListID = int32PtrToIntPtr(r.AnilistID)
	item.MalID = int32PtrToIntPtr(r.MalID)
	item.Kind = r.Kind
	item.ReadingDirection = r.ReadingDirection
	item.FranchiseID = int32PtrToIntPtr(r.FranchiseID)
	return item
}

func GenGetItemByTMDBIDRowToItem(r gen.GetMediaItemByTMDBIDRow) media.Item {
	return ItemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func GenCreateItemRowToItem(r gen.CreateMediaItemRow) media.Item {
	item := ItemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
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

func GenListItemsRowToItem(r gen.ListMediaItemsRow) media.Item {
	return ItemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func GenListMissingArtRowToItem(r gen.ListMediaItemsMissingArtRow) media.Item {
	return ItemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func GenListChildrenRowToItem(r gen.ListMediaItemChildrenRow) media.Item {
	item := ItemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
	item.AniListID = int32PtrToIntPtr(r.AnilistID)
	item.MalID = int32PtrToIntPtr(r.MalID)
	item.Kind = r.Kind
	return item
}

func GenSearchRowToItem(r gen.SearchMediaItemsRow) media.Item {
	return ItemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func GenUpdateItemRowToItem(r gen.UpdateMediaItemMetadataRow) media.Item {
	return ItemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
		r.OriginalTitle, r.Year, r.Summary, r.Tagline,
		r.Rating, r.AudienceRating, r.ContentRating, r.DurationMs,
		r.Genres, r.Tags, r.TmdbID, r.TvdbID, r.ImdbID,
		r.ParentID, r.Index, r.PosterPath, r.FanartPath, r.ThumbPath,
		r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, r.DeletedAt)
}

func CreateItemParamsToGen(p media.CreateItemParams) gen.CreateMediaItemParams {
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
		Year:                      IntPtrToInt32Ptr(p.Year),
		Summary:                   p.Summary,
		Tagline:                   p.Tagline,
		Rating:                    Float64PtrToNumeric(p.Rating),
		AudienceRating:            Float64PtrToNumeric(p.AudienceRating),
		ContentRating:             p.ContentRating,
		DurationMs:                p.DurationMS,
		Genres:                    p.Genres,
		Tags:                      p.Tags,
		TmdbID:                    IntPtrToInt32Ptr(p.TMDBID),
		TvdbID:                    IntPtrToInt32Ptr(p.TVDBID),
		ImdbID:                    p.IMDBID,
		MusicbrainzID:             uuidPtrToPGUUID(p.MusicBrainzID),
		MusicbrainzReleaseID:      uuidPtrToPGUUID(p.MusicBrainzReleaseID),
		MusicbrainzReleaseGroupID: uuidPtrToPGUUID(p.MusicBrainzReleaseGroupID),
		MusicbrainzArtistID:       uuidPtrToPGUUID(p.MusicBrainzArtistID),
		MusicbrainzAlbumArtistID:  uuidPtrToPGUUID(p.MusicBrainzAlbumArtistID),
		DiscTotal:                 IntPtrToInt32Ptr(p.DiscTotal),
		TrackTotal:                IntPtrToInt32Ptr(p.TrackTotal),
		OriginalYear:              IntPtrToInt32Ptr(p.OriginalYear),
		Compilation:               p.Compilation,
		ReleaseType:               releaseType,
		ParentID:                  uuidPtrToPGUUID(p.ParentID),
		Index:                     IntPtrToInt32Ptr(p.Index),
		PosterPath:                p.PosterPath,
		FanartPath:                p.FanartPath,
		ThumbPath:                 p.ThumbPath,
		OriginallyAvailableAt:     timePtrToPGDate(p.OriginallyAvailableAt),
		AnilistID:                 IntPtrToInt32Ptr(p.AniListID),
	}
}

func UpdateItemMetadataParamsToGen(p media.UpdateItemMetadataParams) gen.UpdateMediaItemMetadataParams {
	return gen.UpdateMediaItemMetadataParams{
		ID:                    p.ID,
		Title:                 p.Title,
		SortTitle:             p.SortTitle,
		OriginalTitle:         p.OriginalTitle,
		Year:                  IntPtrToInt32Ptr(p.Year),
		Summary:               p.Summary,
		Tagline:               p.Tagline,
		Rating:                Float64PtrToNumeric(p.Rating),
		AudienceRating:        Float64PtrToNumeric(p.AudienceRating),
		ContentRating:         p.ContentRating,
		DurationMs:            p.DurationMS,
		Genres:                p.Genres,
		Tags:                  p.Tags,
		PosterPath:            p.PosterPath,
		FanartPath:            p.FanartPath,
		ThumbPath:             p.ThumbPath,
		OriginallyAvailableAt: timePtrToPGDate(p.OriginallyAvailableAt),
		TmdbID:                IntPtrToInt32Ptr(p.TMDBID),
		TvdbID:                IntPtrToInt32Ptr(p.TVDBID),
		AnilistID:             IntPtrToInt32Ptr(p.AniListID),
		MalID:                 IntPtrToInt32Ptr(p.MALID),
		ReadingDirection:      p.ReadingDirection,
		FranchiseID:           IntPtrToInt32Ptr(p.FranchiseID),
	}
}

// ── Media file conversions ────────────────────────────────────────────────────

func GenMediaFileToFile(f gen.MediaFile) media.File {
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
		VideoBitDepth:       int32PtrToIntPtr(f.VideoBitDepth),
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

func CreateFileParamsToGen(p media.CreateFileParams) gen.CreateMediaFileParams {
	return gen.CreateMediaFileParams{
		MediaItemID:         p.MediaItemID,
		FilePath:            p.FilePath,
		FileSize:            p.FileSize,
		Container:           p.Container,
		VideoCodec:          p.VideoCodec,
		AudioCodec:          p.AudioCodec,
		ResolutionW:         IntPtrToInt32Ptr(p.ResolutionW),
		ResolutionH:         IntPtrToInt32Ptr(p.ResolutionH),
		Bitrate:             p.Bitrate,
		HdrType:             p.HDRType,
		FrameRate:           Float64PtrToNumeric(p.FrameRate),
		AudioStreams:        p.AudioStreams,
		SubtitleStreams:     p.SubtitleStreams,
		Chapters:            p.Chapters,
		FileHash:            p.FileHash,
		DurationMs:          p.DurationMS,
		BitDepth:            IntPtrToInt32Ptr(p.BitDepth),
		VideoBitDepth:       IntPtrToInt32Ptr(p.VideoBitDepth),
		SampleRate:          IntPtrToInt32Ptr(p.SampleRate),
		ChannelLayout:       p.ChannelLayout,
		Lossless:            p.Lossless,
		ReplaygainTrackGain: Float64PtrToNumeric(p.ReplayGainTrackGain),
		ReplaygainTrackPeak: Float64PtrToNumeric(p.ReplayGainTrackPeak),
		ReplaygainAlbumGain: Float64PtrToNumeric(p.ReplayGainAlbumGain),
		ReplaygainAlbumPeak: Float64PtrToNumeric(p.ReplayGainAlbumPeak),
	}
}
