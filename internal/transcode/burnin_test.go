package transcode

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// argAfter is the value that follows flag in args, "" when flag is absent.
func argAfter(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

// burnArgs is a 4K → 1080p re-encode burning absolute stream 5, with every
// GPU-memory flag the worker could have set switched on, so each test sees
// that the burn turns the ones that matter off.
func burnArgs(enc Encoder) BuildArgs {
	return BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       enc,
		IsVAAPI:       IsVAAPIEncoder(enc),
		IsHEVC:        true,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		CudaVRAM:      true,
		QSVVRAM:       true,
		VAAPIVRAM:     true,
		BurnSubtitle:  &SubtitleBurn{StreamIndex: 5},
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	}
}

// TestBuildHLS_BurnIn_GraphPerEncoder pins the burn-in pipeline on every
// encoder family: the video goes through -filter_complex (never -vf) and out
// of [vout], the subtitle stream is picked by its ABSOLUTE index, the overlay
// paints on the scaled output frame, and no full-GPU-memory decode or scale
// is left in (the overlay is a CPU filter).
func TestBuildHLS_BurnIn_GraphPerEncoder(t *testing.T) {
	const swScale = "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2"
	cases := []struct {
		enc Encoder
		// tail is what follows the overlay.
		tail string
	}{
		{EncoderSoftware, "format=yuv420p"},
		{EncoderHEVCSoftware, "format=yuv420p"},
		{EncoderNVENC, "format=yuv420p"},
		{EncoderHEVCNVENC, "format=yuv420p"},
		{EncoderAV1NVENC, "format=yuv420p"},
		{EncoderQSV, "format=yuv420p"},
		{EncoderHEVCQSV, "format=yuv420p"},
		{EncoderAMF, "format=yuv420p"},
		{EncoderAV1Software, "format=yuv420p"},
		// VAAPI uploads after the overlay, and scales on the CPU before it.
		{EncoderVAAPI, "format=nv12,hwupload"},
		{EncoderHEVCVAAPI, "format=nv12,hwupload"},
	}
	for _, tc := range cases {
		t.Run(string(tc.enc), func(t *testing.T) {
			args := BuildHLS(burnArgs(tc.enc))
			argStr := strings.Join(args, " ")
			if argAfter(args, "-vf") != "" {
				t.Fatalf("burn-in must not use -vf: %s", argStr)
			}
			want := "[0:v:0]" + swScale + "[v];" +
				"[1:5]" + swScale + ":color=black@0[s];" +
				"[v][s]overlay=eof_action=pass:repeatlast=0," + tc.tail + "[vout]"
			if got := argAfter(args, "-filter_complex"); got != want {
				t.Errorf("-filter_complex\n got %s\nwant %s", got, want)
			}
			if argAfter(args, "-map") != "[vout]" {
				t.Errorf("first -map must be the graph output [vout]: %s", argStr)
			}
			if strings.Contains(argStr, "-map 0:v:0") {
				t.Errorf("the source video must not be mapped past the graph: %s", argStr)
			}
			for _, gpuOnly := range []string{
				"-hwaccel_output_format", "scale_cuda", "vpp_qsv", "scale_vaapi", "tonemap_cuda", "tonemap_vaapi",
			} {
				if strings.Contains(argStr, gpuOnly) {
					t.Errorf("burn-in left the GPU-memory path %q in: %s", gpuOnly, argStr)
				}
			}
			if IsVAAPIEncoder(tc.enc) && !strings.Contains(argStr, "-vaapi_device") {
				t.Errorf("VAAPI still needs its device for the upload: %s", argStr)
			}
		})
	}
}

// The system-memory decode offloads stay under a burn-in: NVDEC HEVC decode
// without -hwaccel_output_format hands frames back to the CPU, which is all
// the overlay needs.
func TestBuildHLS_BurnIn_KeepsSystemMemoryDecode(t *testing.T) {
	a := burnArgs(EncoderHEVCNVENC)
	a.CudaVRAM = false
	a.CudaHevcDecode = true
	argStr := strings.Join(BuildHLS(a), " ")
	if !strings.Contains(argStr, "-c:v hevc_cuvid") {
		t.Errorf("want the system-memory hevc_cuvid decode kept: %s", argStr)
	}
	if strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("want no CUDA-memory decode: %s", argStr)
	}
}

// An AV1 source on NVENC takes the NVDEC full-VRAM path unconditionally —
// except under a burn-in.
func TestBuildHLS_BurnIn_AV1NVDECOff(t *testing.T) {
	a := burnArgs(EncoderNVENC)
	a.IsHEVC, a.IsAV1, a.CudaVRAM = false, true, false
	without := a
	without.BurnSubtitle = nil
	if !strings.Contains(strings.Join(BuildHLS(without), " "), "-hwaccel cuda") {
		t.Fatal("precondition: an AV1 NVENC transcode decodes into CUDA memory")
	}
	if argStr := strings.Join(BuildHLS(a), " "); strings.Contains(argStr, "-hwaccel cuda") || strings.Contains(argStr, "scale_cuda") {
		t.Errorf("burn-in left the AV1 CUDA path in: %s", argStr)
	}
}

// HDR: the overlay goes after the tonemap, on SDR frames. The CUDA tonemap
// stays on the GPU and downloads its finished frame, padded to the output box
// like the subtitle canvas; decode then lands in CUDA memory.
func TestBuildHLS_BurnIn_CudaTonemap(t *testing.T) {
	a := burnArgs(EncoderHEVCNVENC)
	a.NeedsToneMap, a.HasZscale = true, true
	a.CudaTonemap, a.CudaHDRTonemap = true, true
	args := BuildHLS(a)
	graph := argAfter(args, "-filter_complex")
	want := "[0:v:0]scale_cuda=w=1920:h=1080:force_original_aspect_ratio=decrease," +
		"tonemap_cuda=tonemap=bt2390:t=bt709:m=bt709:p=bt709:r=tv:format=nv12," +
		"hwdownload,format=nv12,pad=1920:1080:(ow-iw)/2:(oh-ih)/2[v];" +
		"[1:5]scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:color=black@0[s];" +
		"[v][s]overlay=eof_action=pass:repeatlast=0,format=yuv420p[vout]"
	if graph != want {
		t.Errorf("graph\n got %s\nwant %s", graph, want)
	}
	if argAfter(args, "-hwaccel_output_format") != "cuda" {
		t.Errorf("CUDA tonemap needs the decode in CUDA memory: %s", strings.Join(args, " "))
	}
	// libplacebo present too: tonemap_cuda still wins, so the subtitle canvas
	// is fitted and padded like the picture, not stretched.
	a.HasLibplacebo = true
	if got := argAfter(BuildHLS(a), "-filter_complex"); got != want {
		t.Errorf("with libplacebo available\n got %s\nwant %s", got, want)
	}
}

// HDR without the CUDA tonemap: the software zscale path scales first, then
// tonemaps, then paints; the GPU-assisted fallback (CPU tonemap after a
// download) is off.
func TestBuildHLS_BurnIn_AfterTonemap(t *testing.T) {
	a := burnArgs(EncoderHEVCNVENC)
	a.NeedsToneMap, a.HasZscale = true, true
	a.CudaHDRTonemap = true
	graph := argAfter(BuildHLS(a), "-filter_complex")
	if strings.Contains(graph, "tonemap_cuda") || strings.Contains(graph, "scale_cuda") {
		t.Fatalf("GPU path left in: %s", graph)
	}
	tm := strings.Index(graph, "tonemap=tonemap=hable")
	ov := strings.Index(graph, "overlay=")
	sc := strings.Index(graph, "[0:v:0]scale=1920:1080")
	if sc < 0 || tm < 0 || ov < 0 || !(sc < tm && tm < ov) {
		t.Errorf("want scale, then tonemap, then overlay: %s", graph)
	}

	// libplacebo stretches to the box, so the subtitle canvas is stretched
	// the same way rather than fitted and padded.
	a.HasLibplacebo = true
	graph = argAfter(BuildHLS(a), "-filter_complex")
	if !strings.Contains(graph, "[1:5]scale=1920:1080[s]") || !strings.Contains(graph, "libplacebo=w=1920:h=1080") {
		t.Errorf("libplacebo burn-in: %s", graph)
	}
}

// The stream's timing offset shifts the subtitle branch only.
func TestBuildHLS_BurnIn_Offset(t *testing.T) {
	for _, tc := range []struct {
		off  int64
		want string
	}{
		{1500, "[1:5]setpts=PTS+1.500/TB,scale="},
		{-250, "[1:5]setpts=PTS-0.250/TB,scale="},
	} {
		a := burnArgs(EncoderSoftware)
		a.BurnSubtitle.OffsetMS = tc.off
		if graph := argAfter(BuildHLS(a), "-filter_complex"); !strings.Contains(graph, tc.want) {
			t.Errorf("offset %d: want %q in %s", tc.off, tc.want, graph)
		}
	}
}

// The subtitle stream is read through a second input of the same source: same
// seek and guards as the first, but not paced, so it can run ahead to the next
// cue (see burnInSubtitleInput). Without a burn-in there is one input.
func TestBuildHLS_BurnIn_SubtitleInput(t *testing.T) {
	inputs := func(args []string) (idx []int) {
		for i, s := range args {
			if s == "-i" {
				idx = append(idx, i)
			}
		}
		return idx
	}
	a := burnArgs(EncoderHEVCNVENC)
	a.StartOffset = 1500
	a.ReadRate, a.ReadRateInitialBurst = 1, 60
	args := BuildHLS(a)
	in := inputs(args)
	if len(in) != 2 || args[in[0]+1] != a.InputPath || args[in[1]+1] != a.InputPath {
		t.Fatalf("want the source twice as -i: %s", strings.Join(args, " "))
	}
	second := args[in[0]+2 : in[1]]
	if got := argAfter(second, "-ss"); got != "1500.000" {
		t.Errorf("second input -ss %q, want 1500.000: %v", got, second)
	}
	for _, flag := range []string{"-protocol_whitelist", "-analyzeduration", "-probesize"} {
		if argAfter(second, flag) == "" {
			t.Errorf("second input lacks %s: %v", flag, second)
		}
	}
	if argAfter(second, "-readrate") != "" {
		t.Errorf("second input must not be paced: %v", second)
	}
	if argAfter(args[:in[0]], "-readrate") != "1.00" {
		t.Errorf("first input lost its pacing: %s", strings.Join(args, " "))
	}

	a.InputPath = "http://primary:7070/api/v1/worker/source/x"
	args = BuildHLS(a)
	in = inputs(args)
	if argAfter(args[in[0]+2:in[1]], "-reconnect") != "1" {
		t.Errorf("HTTP source: second input lacks -reconnect: %s", strings.Join(args, " "))
	}

	a = burnArgs(EncoderHEVCNVENC)
	a.BurnSubtitle = nil
	if n := len(inputs(BuildHLS(a))); n != 1 {
		t.Errorf("no burn-in: %d inputs, want 1", n)
	}
}

// A burn-in has nothing to paint on in a copy or an audio-only session; the
// request is dropped there rather than producing a graph ffmpeg rejects.
func TestBuildHLS_BurnIn_IgnoredOnCopyAndAudioOnly(t *testing.T) {
	a := burnArgs(EncoderCopy)
	if argStr := strings.Join(BuildHLS(a), " "); strings.Contains(argStr, "-filter_complex") || !strings.Contains(argStr, "-map 0:v:0") {
		t.Errorf("remux: %s", argStr)
	}
	a = burnArgs(EncoderSoftware)
	a.AudioOnly = true
	if argStr := strings.Join(BuildHLS(a), " "); strings.Contains(argStr, "-filter_complex") || strings.Contains(argStr, "[vout]") {
		t.Errorf("audio-only: %s", argStr)
	}
}

// Without a burn-in the -vf chain is exactly what it was: the split into a
// chain and a tail only moves the 8-bit strip after an overlay.
func TestBuildVideoFilter_NoBurnUnchanged(t *testing.T) {
	a := burnArgs(EncoderVAAPI)
	a.BurnSubtitle = nil
	a.VAAPIVRAM = false
	if got, want := buildVideoFilter(a), "format=nv12,hwupload,scale_vaapi=w=1920:h=1080:force_original_aspect_ratio=decrease:format=nv12"; got != want {
		t.Errorf("VAAPI -vf = %q, want %q", got, want)
	}
	a = burnArgs(EncoderSoftware)
	a.BurnSubtitle = nil
	if got, want := buildVideoFilter(a), "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,format=yuv420p"; got != want {
		t.Errorf("software -vf = %q, want %q", got, want)
	}
}

// The job carries the burn-in through Valkey to the worker intact, and an old
// job without one decodes to none.
func TestTranscodeJob_BurnSubtitleRoundTrip(t *testing.T) {
	job := TranscodeJob{SessionID: "s", BurnSubtitle: &SubtitleBurn{StreamIndex: 7, OffsetMS: -400}, AudioPassthrough: true}
	b, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"burn_subtitle":{"stream_index":7,"offset_ms":-400}`) ||
		!strings.Contains(string(b), `"audio_passthrough":true`) {
		t.Errorf("job JSON: %s", b)
	}
	var back TranscodeJob
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.BurnSubtitle, job.BurnSubtitle) || !back.AudioPassthrough {
		t.Errorf("round trip: got %+v / %v", back.BurnSubtitle, back.AudioPassthrough)
	}
	var old TranscodeJob
	if err := json.Unmarshal([]byte(`{"session_id":"s","encoder":"libx264"}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.BurnSubtitle != nil || old.AudioPassthrough {
		t.Errorf("a job without the fields must decode to none: %+v", old)
	}
	plain, _ := json.Marshal(TranscodeJob{SessionID: "s"})
	if strings.Contains(string(plain), "burn_subtitle") || strings.Contains(string(plain), "audio_passthrough") {
		t.Errorf("unset fields must be omitted for older workers: %s", plain)
	}
}

// A job that relies on a worker feature only goes to a worker that names
// it; every other job goes anywhere, as before.
func TestJobNeeds_Features(t *testing.T) {
	if f := jobNeeds(TranscodeJob{}).Features; len(f) != 0 {
		t.Errorf("plain job needs %v, want nothing", f)
	}
	got := jobNeeds(TranscodeJob{BurnSubtitle: &SubtitleBurn{StreamIndex: 3}, AudioPassthrough: true}).Features
	want := []string{WorkerFeatureBurnSubtitles, WorkerFeatureAudioPassthrough}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("features = %v, want %v", got, want)
	}

	oldGPU := WorkerRegistration{Addr: "old", Capabilities: []string{"h264_nvenc"}, MaxSessions: 8}
	newCPU := WorkerRegistration{Addr: "new", Capabilities: []string{"libx264"}, MaxSessions: 8, Features: workerFeatures}
	all := []WorkerRegistration{oldGPU, newCPU}

	burnNeeds := jobNeeds(TranscodeJob{BurnSubtitle: &SubtitleBurn{StreamIndex: 3}})
	capable := workersWithFeatures(all, burnNeeds.Features)
	if len(capable) != 1 || capable[0].Addr != "new" {
		t.Fatalf("capable workers = %v, want only the new one", capable)
	}
	// The GPU bonus would pick the old worker; the hard filter keeps it out.
	if got := selectWorker(capable, burnNeeds); got.Addr != "new" {
		t.Errorf("burn job went to %s", got.Addr)
	}
	if got := selectWorker(workersWithFeatures(all, nil), JobNeeds{}); got.Addr != "old" {
		t.Errorf("plain job went to %s, want the GPU worker as before", got.Addr)
	}
	if len(workersWithFeatures([]WorkerRegistration{oldGPU}, burnNeeds.Features)) != 0 {
		t.Error("an all-old fleet has no worker for a burn-in")
	}
}

func TestBurnInCostCenti(t *testing.T) {
	if got := BurnInCostCenti(JobCostCenti(1920, 1080, "transcode")); got != 150 {
		t.Errorf("1080p burn-in cost = %d, want 150", got)
	}
}
