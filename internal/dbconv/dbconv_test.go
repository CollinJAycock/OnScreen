package dbconv

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

func i32(v int32) *int32 { return &v }

// TestGenMediaFileToFile_MapsTechnicalMetadata is the drift sentinel for the
// read path. The worker's old hand-maintained copy silently dropped these exact
// fields (bit-depth, duration, channel-layout, sample-rate, ReplayGain) — a
// latent transcode-decision bug. If a future column is added to media_files and
// not threaded through here, this fails.
func TestGenMediaFileToFile_MapsTechnicalMetadata(t *testing.T) {
	var rg pgtype.Numeric
	if err := rg.Scan("-7.5"); err != nil {
		t.Fatalf("seed numeric: %v", err)
	}
	dur := int64(123456)
	ch := "5.1(side)"
	lossless := true
	f := gen.MediaFile{
		ID:                  uuid.New(),
		MediaItemID:         uuid.New(),
		FilePath:            "/media/x.flac",
		DurationMs:          &dur,
		BitDepth:            i32(24),
		VideoBitDepth:       i32(10),
		SampleRate:          i32(48000),
		ChannelLayout:       &ch,
		Lossless:            &lossless,
		ReplaygainTrackGain: rg,
		ReplaygainAlbumPeak: rg,
	}
	out := GenMediaFileToFile(f)

	if out.DurationMS == nil || *out.DurationMS != dur {
		t.Errorf("DurationMS dropped: %v", out.DurationMS)
	}
	if out.BitDepth == nil || *out.BitDepth != 24 {
		t.Errorf("BitDepth dropped: %v", out.BitDepth)
	}
	if out.VideoBitDepth == nil || *out.VideoBitDepth != 10 {
		t.Errorf("VideoBitDepth dropped: %v", out.VideoBitDepth)
	}
	if out.SampleRate == nil || *out.SampleRate != 48000 {
		t.Errorf("SampleRate dropped: %v", out.SampleRate)
	}
	if out.ChannelLayout == nil || *out.ChannelLayout != ch {
		t.Errorf("ChannelLayout dropped: %v", out.ChannelLayout)
	}
	if out.Lossless == nil || !*out.Lossless {
		t.Error("Lossless dropped")
	}
	if out.ReplayGainTrackGain == nil {
		t.Error("ReplayGainTrackGain dropped")
	}
	if out.ReplayGainAlbumPeak == nil {
		t.Error("ReplayGainAlbumPeak dropped")
	}
}

// TestCreateFileParamsToGen_MapsTechnicalMetadata is the drift sentinel for the
// write path, including the float64→Numeric.Scan fix (a raw float64 scans as
// NULL, so ReplayGain/frame-rate must go through the string form).
func TestCreateFileParamsToGen_MapsTechnicalMetadata(t *testing.T) {
	bd, vbd, sr := 24, 10, 48000
	gain := -7.5
	fr := 23.976
	dur := int64(999)
	ch := "7.1"
	lossless := true
	p := media.CreateFileParams{
		MediaItemID:         uuid.New(),
		FilePath:            "/media/x.flac",
		BitDepth:            &bd,
		VideoBitDepth:       &vbd,
		SampleRate:          &sr,
		ChannelLayout:       &ch,
		DurationMS:          &dur,
		Lossless:            &lossless,
		FrameRate:           &fr,
		ReplayGainTrackGain: &gain,
	}
	out := CreateFileParamsToGen(p)

	if out.BitDepth == nil || *out.BitDepth != 24 {
		t.Errorf("BitDepth dropped: %v", out.BitDepth)
	}
	if out.VideoBitDepth == nil || *out.VideoBitDepth != 10 {
		t.Errorf("VideoBitDepth dropped: %v", out.VideoBitDepth)
	}
	if out.SampleRate == nil || *out.SampleRate != 48000 {
		t.Errorf("SampleRate dropped: %v", out.SampleRate)
	}
	if out.DurationMs == nil || *out.DurationMs != dur {
		t.Errorf("DurationMs dropped: %v", out.DurationMs)
	}
	if out.ChannelLayout == nil || *out.ChannelLayout != ch {
		t.Errorf("ChannelLayout dropped: %v", out.ChannelLayout)
	}
	if out.Lossless == nil || !*out.Lossless {
		t.Error("Lossless dropped")
	}
	if !out.FrameRate.Valid {
		t.Error("FrameRate scanned as NULL — the float64→Numeric.Scan bug regressed")
	}
	if !out.ReplaygainTrackGain.Valid {
		t.Error("ReplaygainTrackGain scanned as NULL — the float64→Numeric.Scan bug regressed")
	}
}

// TestGenGetItemRowToItem_MapsMusicFields guards the item read path the worker
// copy used to drop (MusicBrainz IDs, disc/track totals, reading direction).
func TestGenGetItemRowToItem_MapsMusicFields(t *testing.T) {
	mbid := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	rd := "rtl"
	r := gen.GetMediaItemRow{
		ID:               uuid.New(),
		LibraryID:        uuid.New(),
		Type:             "album",
		Title:            "X",
		MusicbrainzID:    mbid,
		DiscTotal:        i32(2),
		TrackTotal:       i32(12),
		Compilation:      true,
		ReadingDirection: &rd,
	}
	out := GenGetItemRowToItem(r)

	if out.MusicBrainzID == nil {
		t.Error("MusicBrainzID dropped")
	}
	if out.DiscTotal == nil || *out.DiscTotal != 2 {
		t.Errorf("DiscTotal dropped: %v", out.DiscTotal)
	}
	if out.TrackTotal == nil || *out.TrackTotal != 12 {
		t.Errorf("TrackTotal dropped: %v", out.TrackTotal)
	}
	if !out.Compilation {
		t.Error("Compilation dropped")
	}
	if out.ReadingDirection == nil || *out.ReadingDirection != rd {
		t.Errorf("ReadingDirection dropped: %v", out.ReadingDirection)
	}
}
