package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/loudness"
)

// LoudnessFile is one active track file of an album, as the loudness
// analysis needs it: where it is, what it is now (size + hash), where its
// ReplayGain came from, and its stored measurement if that is of the
// file's current bytes.
type LoudnessFile struct {
	ID          uuid.UUID
	TrackID     uuid.UUID
	Path        string
	Size        int64
	Hash        *string
	DurationMS  *int64
	FromTags    bool // replaygain_source = 'tags': never written here
	Measured    bool // a current measurement (or recorded failure) exists
	Measurement loudness.Measurement
}

// LoudnessStore is the database surface of the loudness_analysis task.
// Kept local so the scheduler doesn't drag in db/gen.
type LoudnessStore interface {
	ListAlbumsNeedingLoudness(ctx context.Context, libraryID *uuid.UUID, limit int32) ([]uuid.UUID, error)
	CountAlbumsNeedingLoudness(ctx context.Context, libraryID *uuid.UUID) (int64, error)
	ListAlbumLoudnessFiles(ctx context.Context, albumID uuid.UUID) ([]LoudnessFile, error)
	// RecordLoudness stores a measurement of the file as it is now.
	RecordLoudness(ctx context.Context, f LoudnessFile, m loudness.Measurement) error
	// SetAnalyzedReplayGain writes measured values unless the file's
	// values came from tags (written=false then).
	SetAnalyzedReplayGain(ctx context.Context, fileID uuid.UUID, rg loudness.ReplayGain) (written bool, err error)
}

// LoudnessMeasure measures one file. Wired to loudness.Measure in main
// (behind the media store's signed URL for object storage); a func type so
// tests can stand in for ffmpeg. An error wrapping loudness.ErrToolFailed
// leaves the file queued.
type LoudnessMeasure func(ctx context.Context, path string) (loudness.Measurement, error)

// LoudnessAnalysisConfig is the optional JSON payload for the
// loudness_analysis task.
type LoudnessAnalysisConfig struct {
	// MaxFiles caps how many files one run measures. ebur128 decodes each
	// file in full (a few seconds for a CD-length track), so the default
	// keeps a nightly run to tens of minutes; an album that has started is
	// always finished, so a run can go a little over.
	MaxFiles int `json:"max_files"`
	// LibraryID optionally scopes the run to one library (UUID string).
	LibraryID string `json:"library_id"`
}

const (
	defaultLoudnessMaxFiles = 500
	maxLoudnessMaxFiles     = 5000
)

// LoudnessAnalysisHandler gives music files without ReplayGain tags
// measured ReplayGain: EBU R128 integrated loudness and true peak per track
// (ffmpeg ebur128), converted to ReplayGain 2.0 (−18 LUFS reference), and
// an album gain from the album's tracks together. The values go into the
// same media_files columns tag values do, marked 'analyzed', so every
// client's normalisation starts working without a client change. Files
// whose values came from tags are measured when they share an album with
// untagged files (their loudness counts towards the album gain) but are
// never written.
//
// Seeded DISABLED like the integrity probe: decoding every track costs real
// CPU and I/O on a large library. It runs on this server, not the transcode
// fleet: the files are already reachable here and the work is a plain
// decode. Each run picks up where the last stopped; a file whose bytes
// change is measured again.
type LoudnessAnalysisHandler struct {
	store   LoudnessStore
	measure LoudnessMeasure
	logger  *slog.Logger
}

// NewLoudnessAnalysisHandler constructs the handler.
func NewLoudnessAnalysisHandler(store LoudnessStore, measure LoudnessMeasure, logger *slog.Logger) *LoudnessAnalysisHandler {
	return &LoudnessAnalysisHandler{store: store, measure: measure, logger: logger}
}

// Run is the scheduler entry point.
func (h *LoudnessAnalysisHandler) Run(ctx context.Context, rawCfg json.RawMessage) (string, error) {
	cfg := LoudnessAnalysisConfig{MaxFiles: defaultLoudnessMaxFiles}
	if len(rawCfg) > 0 {
		if err := json.Unmarshal(rawCfg, &cfg); err != nil {
			return "", fmt.Errorf("parse config: %w", err)
		}
	}
	if cfg.MaxFiles <= 0 {
		cfg.MaxFiles = defaultLoudnessMaxFiles
	}
	if cfg.MaxFiles > maxLoudnessMaxFiles {
		cfg.MaxFiles = maxLoudnessMaxFiles
	}
	var libraryID *uuid.UUID
	if cfg.LibraryID != "" {
		id, err := uuid.Parse(cfg.LibraryID)
		if err != nil {
			return "", fmt.Errorf("parse library_id: %w", err)
		}
		libraryID = &id
	}

	// Each album needs at least one measurement, so MaxFiles albums is
	// always enough to fill the budget.
	albums, err := h.store.ListAlbumsNeedingLoudness(ctx, libraryID, int32(cfg.MaxFiles))
	if err != nil {
		return "", fmt.Errorf("list albums needing loudness: %w", err)
	}
	if len(albums) == 0 {
		return "no music files awaiting loudness analysis", nil
	}

	var st loudnessStats
	for _, albumID := range albums {
		if ctx.Err() != nil || st.measured >= cfg.MaxFiles {
			break
		}
		if err := h.analyzeAlbum(ctx, albumID, &st); err != nil {
			h.logger.WarnContext(ctx, "loudness_analysis: album failed", "album_id", albumID, "err", err)
			st.failed++
		}
	}

	remaining, err := h.store.CountAlbumsNeedingLoudness(ctx, libraryID)
	if err != nil {
		remaining = -1
	}
	return fmt.Sprintf("albums=%d measured=%d written=%d silent=%d unmeasurable=%d failed=%d remaining_albums=%d",
		st.albums, st.measured, st.written, st.silent, st.unmeasurable, st.failed, remaining), nil
}

type loudnessStats struct {
	albums, measured, written, silent, unmeasurable, failed int
}

// analyzeAlbum measures the album's files that have no current measurement,
// then writes track and album gain to every file whose values aren't tags.
// The album gain is written only when every file of the album has been
// measured: a file the tool couldn't measure this run leaves the album for
// the next.
func (h *LoudnessAnalysisHandler) analyzeAlbum(ctx context.Context, albumID uuid.UUID, st *loudnessStats) error {
	files, err := h.store.ListAlbumLoudnessFiles(ctx, albumID)
	if err != nil {
		return err
	}
	st.albums++
	complete := true
	for i := range files {
		f := &files[i]
		if f.Measured {
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		m, err := h.measure(ctx, f.Path)
		if err != nil {
			// Tool failure (ffmpeg missing, timeout, shutdown): the file
			// stays queued and the album's values wait for it.
			if !errors.Is(err, context.Canceled) {
				h.logger.WarnContext(ctx, "loudness_analysis: measure failed",
					"file_id", f.ID, "path", f.Path, "err", err)
			}
			st.failed++
			complete = false
			continue
		}
		if err := h.store.RecordLoudness(ctx, *f, m); err != nil {
			h.logger.WarnContext(ctx, "loudness_analysis: record failed", "file_id", f.ID, "err", err)
			st.failed++
			complete = false
			continue
		}
		st.measured++
		switch {
		case m.Error != "":
			st.unmeasurable++
			h.logger.InfoContext(ctx, "loudness_analysis: file could not be measured",
				"file_id", f.ID, "path", f.Path, "detail", m.Error)
		case m.IntegratedLUFS == nil:
			st.silent++
		}
		f.Measured, f.Measurement = true, m
	}

	values := AlbumReplayGain(files, complete)
	for _, f := range files {
		rg, ok := values[f.ID]
		if !ok || f.FromTags {
			continue
		}
		written, err := h.store.SetAnalyzedReplayGain(ctx, f.ID, rg)
		if err != nil {
			h.logger.WarnContext(ctx, "loudness_analysis: write replaygain failed", "file_id", f.ID, "err", err)
			st.failed++
			continue
		}
		if written {
			st.written++
		}
	}
	return nil
}

// AlbumReplayGain computes the ReplayGain values of an album's measured
// files: each file's own track gain and peak, and — when withAlbum is set —
// the album gain and peak shared by all of them. Files with no usable
// measurement (unmeasured, failed, silent) get no entry. A track held by
// several files (the same song as FLAC and MP3) counts once towards the
// album, by its first file.
func AlbumReplayGain(files []LoudnessFile, withAlbum bool) map[uuid.UUID]loudness.ReplayGain {
	out := map[uuid.UUID]loudness.ReplayGain{}
	var tracks []loudness.Track
	seenTrack := map[uuid.UUID]bool{}
	var albumPeak *float64
	usable := 0
	for _, f := range files {
		m := f.Measurement
		if !f.Measured || m.Error != "" || m.IntegratedLUFS == nil {
			continue
		}
		usable++
		gain := loudness.TrackGain(*m.IntegratedLUFS)
		rg := loudness.ReplayGain{TrackGain: &gain}
		if m.TruePeak != nil {
			p := *m.TruePeak
			rg.TrackPeak = &p
			if albumPeak == nil || p > *albumPeak {
				albumPeak = &p
			}
		}
		out[f.ID] = rg
		if !seenTrack[f.TrackID] && f.DurationMS != nil {
			seenTrack[f.TrackID] = true
			tracks = append(tracks, loudness.Track{IntegratedLUFS: *m.IntegratedLUFS, DurationMS: *f.DurationMS})
		}
	}
	if !withAlbum || usable == 0 {
		return out
	}
	lufs, ok := loudness.AlbumLoudness(tracks)
	if !ok {
		return out
	}
	albumGain := loudness.TrackGain(lufs)
	for id, rg := range out {
		g := albumGain
		rg.AlbumGain = &g
		if albumPeak != nil {
			p := *albumPeak
			rg.AlbumPeak = &p
		}
		out[id] = rg
	}
	return out
}
