package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/dbconv"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/loudness"
	"github.com/onscreen/onscreen/internal/scanner"
	"github.com/onscreen/onscreen/internal/scheduler"
)

// musicCreditStore backs scanner.ArtistCreditStore with the generated
// queries (track_artists / track_credit_scans, migration 00048).
type musicCreditStore struct{ q *gen.Queries }

func (s musicCreditStore) FindArtistByMusicBrainzID(ctx context.Context, libraryID, mbid uuid.UUID) (uuid.UUID, bool, error) {
	row, err := s.q.FindArtistByMusicBrainzID(ctx, gen.FindArtistByMusicBrainzIDParams{LibraryID: libraryID, Mbid: mbid})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return row.ID, true, nil
}

func (s musicCreditStore) SetTrackArtists(ctx context.Context, trackID uuid.UUID, credits []scanner.TrackArtist) error {
	keep := make([]uuid.UUID, 0, len(credits))
	for _, c := range credits {
		keep = append(keep, c.ArtistID)
	}
	if err := s.q.DeleteTrackArtistsExcept(ctx, gen.DeleteTrackArtistsExceptParams{TrackID: trackID, Keep: keep}); err != nil {
		return err
	}
	for _, c := range credits {
		if err := s.q.UpsertTrackArtist(ctx, gen.UpsertTrackArtistParams{
			TrackID:  trackID,
			ArtistID: c.ArtistID,
			Role:     c.Role,
			Position: int16(c.Position),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s musicCreditStore) CreditsScanned(ctx context.Context, fileID uuid.UUID) (bool, error) {
	return s.q.TrackCreditsScanned(ctx, fileID)
}

func (s musicCreditStore) MarkCreditsScanned(ctx context.Context, fileID uuid.UUID) error {
	return s.q.MarkTrackCreditsScanned(ctx, fileID)
}

// loudnessStore backs scheduler.LoudnessStore (media_file_loudness and the
// media_files replaygain columns).
type loudnessStore struct{ q *gen.Queries }

func optLibraryID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

func (s loudnessStore) ListAlbumsNeedingLoudness(ctx context.Context, libraryID *uuid.UUID, limit int32) ([]uuid.UUID, error) {
	return s.q.ListAlbumsNeedingLoudness(ctx, gen.ListAlbumsNeedingLoudnessParams{LibraryID: optLibraryID(libraryID), Lim: limit})
}

func (s loudnessStore) CountAlbumsNeedingLoudness(ctx context.Context, libraryID *uuid.UUID) (int64, error) {
	return s.q.CountAlbumsNeedingLoudness(ctx, optLibraryID(libraryID))
}

func (s loudnessStore) ListAlbumLoudnessFiles(ctx context.Context, albumID uuid.UUID) ([]scheduler.LoudnessFile, error) {
	rows, err := s.q.ListAlbumLoudnessFiles(ctx, pgtype.UUID{Bytes: albumID, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]scheduler.LoudnessFile, 0, len(rows))
	for _, r := range rows {
		f := scheduler.LoudnessFile{
			ID:         r.ID,
			TrackID:    r.MediaItemID,
			Path:       r.FilePath,
			Size:       r.FileSize,
			Hash:       r.FileHash,
			DurationMS: r.DurationMs,
			FromTags:   r.ReplaygainSource != nil && *r.ReplaygainSource == media.ReplayGainFromTags,
			Measured:   r.Measured,
		}
		if r.Measured {
			f.Measurement = loudness.Measurement{
				IntegratedLUFS: dbconv.NumericToFloat64Ptr(r.IntegratedLufs),
				TruePeak:       dbconv.NumericToFloat64Ptr(r.TruePeak),
			}
			if r.LoudnessError != nil {
				f.Measurement.Error = *r.LoudnessError
			}
		}
		out = append(out, f)
	}
	return out, nil
}

func (s loudnessStore) RecordLoudness(ctx context.Context, f scheduler.LoudnessFile, m loudness.Measurement) error {
	var errText *string
	if m.Error != "" {
		e := m.Error
		errText = &e
	}
	return s.q.UpsertFileLoudness(ctx, gen.UpsertFileLoudnessParams{
		FileID:         f.ID,
		IntegratedLufs: dbconv.Float64PtrToNumeric(m.IntegratedLUFS),
		TruePeak:       dbconv.Float64PtrToNumeric(m.TruePeak),
		DurationMs:     f.DurationMS,
		FileSize:       f.Size,
		FileHash:       f.Hash,
		Error:          errText,
	})
}

func (s loudnessStore) SetAnalyzedReplayGain(ctx context.Context, fileID uuid.UUID, rg loudness.ReplayGain) (bool, error) {
	n, err := s.q.SetAnalyzedReplayGain(ctx, gen.SetAnalyzedReplayGainParams{
		ID:                  fileID,
		ReplaygainTrackGain: dbconv.Float64PtrToNumeric(rg.TrackGain),
		ReplaygainTrackPeak: dbconv.Float64PtrToNumeric(rg.TrackPeak),
		ReplaygainAlbumGain: dbconv.Float64PtrToNumeric(rg.AlbumGain),
		ReplaygainAlbumPeak: dbconv.Float64PtrToNumeric(rg.AlbumPeak),
	})
	return n > 0, err
}
