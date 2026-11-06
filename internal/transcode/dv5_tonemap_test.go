package transcode

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// Dolby Vision profile 5 → SDR (docs/dolby-vision.md): a re-encode whose
// tone-map applies each frame's Dolby Vision metadata, on a worker that has
// one (WorkerFeatureDVTonemap).

func dv5File() func() media.File {
	return func() media.File {
		f := baseFile()
		f.VideoCodec = strPtr("hevc")
		f.HDRType = strPtr("dolby_vision")
		p := 5
		f.DVProfile = &p
		return f
	}
}

func TestDecide_DV5Tonemap(t *testing.T) {
	const noDV = "videoDecoder=h264:h265,audioDecoder=aac,protocols=mkv:mp4,maxBitDepth=10"
	tonemap := defaultServerCaps
	tonemap.DVTonemap = true
	for _, tc := range []struct {
		name   string
		caps   string
		server ServerCaps
		want   Decision
	}{
		{"no worker tone-maps it", noDV, defaultServerCaps, DecisionUnsupported},
		{"a worker tone-maps it", noDV, tonemap, DecisionTranscode},
		{"a client that lists 5 plays it", noDV + ",dovi=5", tonemap, DecisionDirectPlay},
		{"a client that lists only 8.1", noDV + ",dovi=8.1", tonemap, DecisionTranscode},
		{"over a profile 5 client's size, no worker", noDV + ",dovi=5,maxWidth=1280,maxHeight=720", defaultServerCaps, DecisionUnsupported},
		{"over a profile 5 client's size, a worker", noDV + ",dovi=5,maxWidth=1280,maxHeight=720", tonemap, DecisionTranscode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := dv5File()()
			f.ResolutionW, f.ResolutionH = intPtr(1920), intPtr(1080)
			if got := Decide(f, ParseCapabilities(tc.caps), tc.server); got != tc.want {
				t.Errorf("want %s, got %s", tc.want, got)
			}
		})
	}
}

func TestJobNeeds_DV5Tonemap(t *testing.T) {
	reencode := TranscodeJob{DVProfileKey: "5", NeedsToneMap: true}
	if !reencode.ReshapesDolbyVision() {
		t.Fatal("a profile 5 re-encode must reshape")
	}
	if got := jobNeeds(reencode).Features; !reflect.DeepEqual(got, []string{WorkerFeatureDVTonemap}) {
		t.Errorf("features = %v, want only %s", got, WorkerFeatureDVTonemap)
	}
	for name, j := range map[string]TranscodeJob{
		"profile 5 copy":         {DVProfileKey: "5", DolbyVision: DVKeep},
		"profile 8.1 re-encode":  {DVProfileKey: "8.1", NeedsToneMap: true},
		"profile 5, no tone-map": {DVProfileKey: "5"},
	} {
		if j.ReshapesDolbyVision() {
			t.Errorf("%s must not reshape", name)
		}
		for _, f := range jobNeeds(j).Features {
			if f == WorkerFeatureDVTonemap {
				t.Errorf("%s must not need %s", name, f)
			}
		}
	}
}

func dv5Args(enc Encoder) BuildArgs {
	return BuildArgs{
		InputPath:     "/media/p5.mp4",
		Encoder:       enc,
		IsVAAPI:       IsVAAPIEncoder(enc),
		IsHEVC:        true,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		NeedsToneMap:  true,
		HasZscale:     true,
		DVReshape:     true,
		DVProfileKey:  "5",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
		// Every hardware decode and VRAM chain the worker might have on: a
		// profile 5 re-encode must use none of them.
		CudaHevcDecode: true,
		CudaVRAM:       true,
		CudaHDRTonemap: true,
		QSVDecode:      true,
		QSVVRAM:        true,
		VAAPIVRAM:      true,
		VAAPITonemap:   true,
	}
}

// noHardwareDecode fails the test if args decode anywhere but in software:
// only ffmpeg's own HEVC decoder hands the Dolby Vision metadata on.
func noHardwareDecode(t *testing.T, args []string) {
	t.Helper()
	joined := strings.Join(args, " ")
	for _, bad := range []string{"-hwaccel ", "hevc_cuvid", "hevc_qsv", "-c:v hevc_"} {
		if i := strings.Index(joined, bad); i >= 0 && i < strings.Index(joined, " -i ") {
			t.Errorf("profile 5 must decode in software, found %q before the input: %s", bad, joined)
		}
	}
}

func TestBuildHLS_DV5_CudaTonemap(t *testing.T) {
	a := dv5Args(EncoderHEVCNVENC)
	a.CudaTonemap = true
	args := BuildHLS(a)
	noHardwareDecode(t, args)
	if !hasArgPair(args, "-init_hw_device", "cuda=dvcuda") || !hasArgPair(args, "-filter_hw_device", "dvcuda") {
		t.Errorf("the CUDA chain needs a filter device: %s", strings.Join(args, " "))
	}
	want := "format=p010le,hwupload_cuda,scale_cuda=w=1920:h=1080:force_original_aspect_ratio=decrease," +
		"tonemap_cuda=tonemap=bt2390:t=bt709:m=bt709:p=bt709:r=tv:format=nv12"
	if got := argAfter(args, "-vf"); got != want {
		t.Errorf("-vf\n got %s\nwant %s", got, want)
	}
	// libplacebo present too: tonemap_cuda still wins.
	a.HasLibplacebo = true
	if got := argAfter(BuildHLS(a), "-vf"); got != want {
		t.Errorf("with libplacebo\n got %s\nwant %s", got, want)
	}
}

func TestBuildHLS_DV5_CudaTonemap_BurnIn(t *testing.T) {
	a := dv5Args(EncoderHEVCNVENC)
	a.CudaTonemap = true
	a.BurnSubtitle = &SubtitleBurn{StreamIndex: 3}
	args := BuildHLS(a)
	noHardwareDecode(t, args)
	graph := argAfter(args, "-filter_complex")
	if !strings.HasPrefix(graph, "[0:v:0]format=p010le,hwupload_cuda,scale_cuda=") ||
		!strings.Contains(graph, "tonemap_cuda=") {
		t.Errorf("burn-in graph must upload, scale and tone-map on the GPU: %s", graph)
	}
}

// A copy keeps Dolby Vision (or the base layer) as before: the flag is ignored.
func TestBuildHLS_DV5_CopyIgnoresReshape(t *testing.T) {
	a := dv5Args(EncoderCopy)
	a.NeedsToneMap = false
	a.CudaTonemap = true
	got := BuildHLS(a)
	b := a
	b.DVReshape = false
	if want := BuildHLS(b); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("copy args changed by DVReshape:\n got %s\nwant %s", strings.Join(got, " "), strings.Join(want, " "))
	}
}

func TestWorker_CanTonemapDV5(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		encoders              []Encoder
		cudaFilter, cudaWorks bool
		libplacebo            bool
		want                  bool
	}{
		{"tonemap_cuda with apply_dovi, warmed, NVENC", []Encoder{EncoderHEVCNVENC, EncoderNVENC}, true, true, false, true},
		{"tonemap_cuda not warmed yet", []Encoder{EncoderNVENC}, true, false, false, false},
		{"tonemap_cuda without apply_dovi", []Encoder{EncoderNVENC}, false, true, false, false},
		{"tonemap_cuda but no NVENC", []Encoder{EncoderSoftware}, true, true, false, false},
		// libplacebo isn't checked on a real Vulkan GPU, so it isn't offered.
		{"libplacebo only", []Encoder{EncoderSoftware}, false, false, true, false},
		{"nothing", []Encoder{EncoderSoftware}, false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &Worker{encoders: tc.encoders, dvTonemapCuda: tc.cudaFilter, hasLibplacebo: tc.libplacebo}
			w.cudaTonemap.Store(tc.cudaWorks)
			if got := w.canTonemapDV5(); got != tc.want {
				t.Errorf("canTonemapDV5 = %v, want %v", got, tc.want)
			}
			has := false
			for _, f := range w.features() {
				has = has || f == WorkerFeatureDVTonemap
			}
			if has != tc.want {
				t.Errorf("advertises %s = %v, want %v", WorkerFeatureDVTonemap, has, tc.want)
			}
		})
	}
}
