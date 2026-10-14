package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/loudness"
)

type stubLoudnessStore struct {
	albums   []uuid.UUID
	files    map[uuid.UUID][]LoudnessFile
	recorded map[uuid.UUID]loudness.Measurement
	written  map[uuid.UUID]loudness.ReplayGain
	tagged   map[uuid.UUID]bool
}

func (s *stubLoudnessStore) ListAlbumsNeedingLoudness(_ context.Context, _ *uuid.UUID, limit int32) ([]uuid.UUID, error) {
	if int(limit) < len(s.albums) {
		return s.albums[:limit], nil
	}
	return s.albums, nil
}

func (s *stubLoudnessStore) CountAlbumsNeedingLoudness(context.Context, *uuid.UUID) (int64, error) {
	return 0, nil
}

func (s *stubLoudnessStore) ListAlbumLoudnessFiles(_ context.Context, albumID uuid.UUID) ([]LoudnessFile, error) {
	return append([]LoudnessFile(nil), s.files[albumID]...), nil
}

func (s *stubLoudnessStore) RecordLoudness(_ context.Context, f LoudnessFile, m loudness.Measurement) error {
	s.recorded[f.ID] = m
	return nil
}

func (s *stubLoudnessStore) SetAnalyzedReplayGain(_ context.Context, id uuid.UUID, rg loudness.ReplayGain) (bool, error) {
	// Mirrors the SQL guard: a file whose values came from tags is refused.
	if s.tagged[id] {
		return false, nil
	}
	s.written[id] = rg
	return true, nil
}

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

func newLoudnessStore() *stubLoudnessStore {
	return &stubLoudnessStore{
		files:    map[uuid.UUID][]LoudnessFile{},
		recorded: map[uuid.UUID]loudness.Measurement{},
		written:  map[uuid.UUID]loudness.ReplayGain{},
		tagged:   map[uuid.UUID]bool{},
	}
}

func TestLoudnessAnalysis_WritesTrackAndAlbumGainNeverTags(t *testing.T) {
	store := newLoudnessStore()
	album := uuid.New()
	untagged1 := LoudnessFile{ID: uuid.New(), TrackID: uuid.New(), Path: "/m/1.flac", Size: 10, DurationMS: i64(180000)}
	untagged2 := LoudnessFile{ID: uuid.New(), TrackID: uuid.New(), Path: "/m/2.flac", Size: 10, DurationMS: i64(180000)}
	// A tagged track of the same album: measured (it counts towards the
	// album) but never written.
	tagged := LoudnessFile{ID: uuid.New(), TrackID: uuid.New(), Path: "/m/3.flac", Size: 10, DurationMS: i64(180000), FromTags: true}
	store.tagged[tagged.ID] = true
	store.albums = []uuid.UUID{album}
	store.files[album] = []LoudnessFile{untagged1, untagged2, tagged}

	lufs := map[string]float64{"/m/1.flac": -10, "/m/2.flac": -20, "/m/3.flac": -15}
	peaks := map[string]float64{"/m/1.flac": 0.9, "/m/2.flac": 1.05, "/m/3.flac": 0.5}
	var measured []string
	measure := func(_ context.Context, path string) (loudness.Measurement, error) {
		measured = append(measured, path)
		return loudness.Measurement{IntegratedLUFS: f64(lufs[path]), TruePeak: f64(peaks[path])}, nil
	}
	h := NewLoudnessAnalysisHandler(store, measure, slog.New(slog.NewTextHandler(io.Discard, nil)))
	summary, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(measured) != 3 {
		t.Fatalf("measured %v, want all three files", measured)
	}
	if _, ok := store.written[tagged.ID]; ok {
		t.Fatal("a tagged file's ReplayGain must never be written")
	}
	rg1 := store.written[untagged1.ID]
	if rg1.TrackGain == nil || *rg1.TrackGain != -8 || *rg1.TrackPeak != 0.9 {
		t.Errorf("track 1: %+v", rg1)
	}
	if rg2 := store.written[untagged2.ID]; rg2.TrackGain == nil || *rg2.TrackGain != 2 {
		t.Errorf("track 2: %+v", rg2)
	}
	// Album: energy mean of −10, −20, −15 LUFS over equal durations.
	want, _ := loudness.AlbumLoudness([]loudness.Track{{IntegratedLUFS: -10, DurationMS: 180000}, {IntegratedLUFS: -20, DurationMS: 180000}, {IntegratedLUFS: -15, DurationMS: 180000}})
	if rg1.AlbumGain == nil || math.Abs(*rg1.AlbumGain-loudness.TrackGain(want)) > 1e-9 {
		t.Errorf("album gain = %v, want %v", rg1.AlbumGain, loudness.TrackGain(want))
	}
	if rg1.AlbumPeak == nil || *rg1.AlbumPeak != 1.05 {
		t.Errorf("album peak = %v, want the album's highest true peak 1.05", rg1.AlbumPeak)
	}
	if !strings.Contains(summary, "written=2") {
		t.Errorf("summary %q", summary)
	}
}

func TestLoudnessAnalysis_ToolFailureHoldsAlbumGain(t *testing.T) {
	store := newLoudnessStore()
	album := uuid.New()
	ok := LoudnessFile{ID: uuid.New(), TrackID: uuid.New(), Path: "/m/ok.flac", DurationMS: i64(1000)}
	broken := LoudnessFile{ID: uuid.New(), TrackID: uuid.New(), Path: "/m/tool.flac", DurationMS: i64(1000)}
	store.albums = []uuid.UUID{album}
	store.files[album] = []LoudnessFile{ok, broken}
	measure := func(_ context.Context, path string) (loudness.Measurement, error) {
		if path == broken.Path {
			return loudness.Measurement{}, fmt.Errorf("%w: ffmpeg missing", loudness.ErrToolFailed)
		}
		return loudness.Measurement{IntegratedLUFS: f64(-14), TruePeak: f64(0.8)}, nil
	}
	h := NewLoudnessAnalysisHandler(store, measure, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := h.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, rec := store.recorded[broken.ID]; rec {
		t.Error("a tool failure must not be recorded against the file")
	}
	rg := store.written[ok.ID]
	if rg.TrackGain == nil || *rg.TrackGain != -4 {
		t.Errorf("measured file still gets its track gain: %+v", rg)
	}
	if rg.AlbumGain != nil {
		t.Error("album gain must wait until every track is measured")
	}
}

func TestLoudnessAnalysis_SilenceAndUnmeasurable(t *testing.T) {
	store := newLoudnessStore()
	album := uuid.New()
	loud := LoudnessFile{ID: uuid.New(), TrackID: uuid.New(), Path: "/m/loud.flac", DurationMS: i64(1000)}
	silent := LoudnessFile{ID: uuid.New(), TrackID: uuid.New(), Path: "/m/silent.flac", DurationMS: i64(60000)}
	junk := LoudnessFile{ID: uuid.New(), TrackID: uuid.New(), Path: "/m/junk.flac", DurationMS: i64(1000)}
	store.albums = []uuid.UUID{album}
	store.files[album] = []LoudnessFile{loud, silent, junk}
	measure := func(_ context.Context, path string) (loudness.Measurement, error) {
		switch path {
		case silent.Path:
			return loudness.Measurement{}, nil
		case junk.Path:
			return loudness.Measurement{Error: "Invalid data found when processing input"}, nil
		}
		return loudness.Measurement{IntegratedLUFS: f64(-12), TruePeak: f64(1)}, nil
	}
	h := NewLoudnessAnalysisHandler(store, measure, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := h.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	// Both are recorded (so they aren't retried until they change) but get
	// no values; the silent track doesn't drag the album gain up.
	if _, ok := store.recorded[silent.ID]; !ok {
		t.Error("silence must be recorded")
	}
	if store.recorded[junk.ID].Error == "" {
		t.Error("an unmeasurable file must be recorded with its error")
	}
	if _, ok := store.written[silent.ID]; ok {
		t.Error("a silent file gets no gain")
	}
	if _, ok := store.written[junk.ID]; ok {
		t.Error("an unmeasurable file gets no gain")
	}
	rg := store.written[loud.ID]
	if rg.AlbumGain == nil || *rg.AlbumGain != -6 {
		t.Errorf("album gain = %v, want -6 (the silent track left out)", rg.AlbumGain)
	}
}

func TestLoudnessAnalysis_BudgetAndConfig(t *testing.T) {
	store := newLoudnessStore()
	for i := 0; i < 3; i++ {
		a := uuid.New()
		store.albums = append(store.albums, a)
		store.files[a] = []LoudnessFile{
			{ID: uuid.New(), TrackID: uuid.New(), Path: fmt.Sprintf("/m/%d-a", i), DurationMS: i64(1000)},
			{ID: uuid.New(), TrackID: uuid.New(), Path: fmt.Sprintf("/m/%d-b", i), DurationMS: i64(1000)},
		}
	}
	n := 0
	measure := func(context.Context, string) (loudness.Measurement, error) {
		n++
		return loudness.Measurement{IntegratedLUFS: f64(-18)}, nil
	}
	h := NewLoudnessAnalysisHandler(store, measure, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cfg, _ := json.Marshal(LoudnessAnalysisConfig{MaxFiles: 3})
	if _, err := h.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	// The budget stops between albums: the second album started under it
	// is finished (4 files), the third is left for the next run.
	if n != 4 {
		t.Errorf("measured %d files, want 4", n)
	}
	if _, err := h.Run(context.Background(), json.RawMessage(`{"library_id":"nope"}`)); err == nil {
		t.Error("a bad library_id must fail the run")
	}
}

func TestAlbumReplayGain_DuplicateFilesCountOnce(t *testing.T) {
	track := uuid.New()
	other := uuid.New()
	files := []LoudnessFile{
		{ID: uuid.New(), TrackID: track, DurationMS: i64(1000), Measured: true, Measurement: loudness.Measurement{IntegratedLUFS: f64(-10)}},
		// The same song as a second file: counted once towards the album.
		{ID: uuid.New(), TrackID: track, DurationMS: i64(1000), Measured: true, Measurement: loudness.Measurement{IntegratedLUFS: f64(-10)}},
		{ID: uuid.New(), TrackID: other, DurationMS: i64(1000), Measured: true, Measurement: loudness.Measurement{IntegratedLUFS: f64(-20)}},
	}
	got := AlbumReplayGain(files, true)
	want, _ := loudness.AlbumLoudness([]loudness.Track{{IntegratedLUFS: -10, DurationMS: 1000}, {IntegratedLUFS: -20, DurationMS: 1000}})
	for _, f := range files {
		if g := got[f.ID].AlbumGain; g == nil || *g != loudness.TrackGain(want) {
			t.Errorf("album gain %v, want %v", g, loudness.TrackGain(want))
		}
	}
}
