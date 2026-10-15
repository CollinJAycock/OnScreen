package downloads

import (
	"testing"

	"github.com/onscreen/onscreen/internal/domain/media"
)

func fileOf(codec string, w, h int, bps int64) *media.File {
	f := &media.File{VideoCodec: &codec}
	if w > 0 {
		f.ResolutionW, f.ResolutionH = &w, &h
	}
	if bps > 0 {
		f.Bitrate = &bps
	}
	return f
}

func TestOptimizeProfiles_FixedRungs(t *testing.T) {
	src := fileOf("hevc", 3840, 2160, 60_000_000)
	for _, tc := range []struct {
		id         string
		kbps, maxH int
	}{
		{OptimizeMobile, 4000, 720},
		{OptimizeTV, 8000, 1080},
	} {
		p, ok := OptimizeProfileByID(tc.id)
		if !ok {
			t.Fatalf("%s missing", tc.id)
		}
		plan := p.Plan(src)
		if plan.BitrateKbps != tc.kbps || plan.MaxHeight != tc.maxH {
			t.Errorf("%s: plan = %+v", tc.id, plan)
		}
	}
	if _, ok := OptimizeProfileByID("1080p_8mbps"); ok {
		t.Error("a download quality id resolved as an optimize profile")
	}
}

func TestOptimizeProfiles_OriginalKeepsResolution(t *testing.T) {
	p, _ := OptimizeProfileByID(OptimizeOriginal)
	cases := []struct {
		name string
		f    *media.File
		kbps int
	}{
		{"4K HEVC", fileOf("hevc", 3840, 2160, 50_000_000), 40000},
		{"scope 4K", fileOf("hevc", 3840, 1600, 50_000_000), 40000},
		{"1080p HEVC", fileOf("hevc", 1920, 1080, 6_000_000), 8000},
		{"scope 1080p", fileOf("mpeg2video", 1920, 800, 0), 8000},
		{"720p", fileOf("vc1", 1280, 720, 0), 4000},
		{"SD 576", fileOf("mpeg2video", 720, 576, 0), 4000},
		{"SD 480", fileOf("mpeg2video", 640, 480, 0), 1000},
		{"H.264 below its rung keeps its own rate", fileOf("h264", 1920, 1080, 5_000_000), 5000},
		{"H.264 above its rung is capped", fileOf("h264", 1920, 1080, 30_000_000), 8000},
		{"unknown size", fileOf("hevc", 0, 0, 0), 8000},
	}
	for _, tc := range cases {
		plan := p.Plan(tc.f)
		if plan.BitrateKbps != tc.kbps {
			t.Errorf("%s: bitrate = %d, want %d", tc.name, plan.BitrateKbps, tc.kbps)
		}
		if plan.MaxWidth != 3840 || plan.MaxHeight != 2160 {
			t.Errorf("%s: box = %dx%d, want the 4K box (no downscale)", tc.name, plan.MaxWidth, plan.MaxHeight)
		}
	}
}

func TestAverageKbps(t *testing.T) {
	if got := averageKbps(1_000_000, 8_000); got != 1000 { // 1 MB over 8 s
		t.Errorf("averageKbps = %d, want 1000", got)
	}
	if got := averageKbps(1_000_000, 0); got != 0 {
		t.Errorf("unknown duration = %d, want 0", got)
	}
}

func TestSourceChanged(t *testing.T) {
	a, b := "aaa", "bbb"
	if sourceChanged(&a, &a) || !sourceChanged(&a, &b) || sourceChanged(nil, &b) || sourceChanged(&a, nil) {
		t.Error("sourceChanged: only two known, different hashes count as a change")
	}
}

func TestSourceFile_StackedMoviePicksPartOne(t *testing.T) {
	one, two := 1, 2
	vc := "h264"
	files := []media.File{
		{FilePath: "/m/Movie cd2.mkv", PartIndex: &two, VideoCodec: &vc, Status: "active"},
		{FilePath: "/m/Movie cd1.mkv", PartIndex: &one, VideoCodec: &vc, Status: "active"},
	}
	got := SourceFile(files)
	if got == nil || got.PartIndex == nil || *got.PartIndex != 1 {
		t.Fatalf("source = %+v, want part 1", got)
	}
	if SourceFile(nil) != nil {
		t.Fatal("no files: want nil")
	}
}
