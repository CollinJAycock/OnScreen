package transcode

import (
	"strings"
	"testing"
)

func TestBuildHLS_ContainsRequiredArgs(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderSoftware,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/onscreen/sessions/abc",
		SegmentPrefix: "seg",
	})

	argStr := strings.Join(args, " ")

	required := []string{
		"-hide_banner",
		"-i /media/movie.mkv",
		"-c:v libx264",
		"-b:v 8000k",
		"-c:a aac",
		"-f hls",
		"-hls_time 4",
		"seg%05d.ts",
		"index.m3u8",
	}
	for _, r := range required {
		if !strings.Contains(argStr, r) {
			t.Errorf("expected arg %q in: %s", r, argStr)
		}
	}
}

func TestBuildHLS_StartOffset(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		StartOffset:   30.5,
		Encoder:       EncoderSoftware,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-ss 30.500") {
		t.Errorf("expected -ss 30.500 in args: %s", argStr)
	}
}

func TestBuildHLS_NoStartOffset(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderSoftware,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if strings.Contains(argStr, "-ss") {
		t.Errorf("expected no -ss when StartOffset=0, got: %s", argStr)
	}
}

func TestBuildHLS_NVENC_Flags(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderNVENC,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-preset p4") {
		t.Errorf("expected NVENC -preset p4 in args: %s", argStr)
	}
	if !strings.Contains(argStr, "-tune hq") {
		t.Errorf("expected NVENC -tune hq in args: %s", argStr)
	}
	if !strings.Contains(argStr, "-rc vbr") {
		t.Errorf("expected NVENC -rc vbr in args: %s", argStr)
	}
}

func TestBuildHLS_VAAPI_Filter(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderVAAPI,
		IsVAAPI:       true,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-vaapi_device") {
		t.Errorf("expected -vaapi_device in VAAPI args: %s", argStr)
	}
	if !strings.Contains(argStr, "hwupload") {
		t.Errorf("expected hwupload filter in VAAPI args: %s", argStr)
	}
	if !strings.Contains(argStr, "scale_vaapi") {
		t.Errorf("expected scale_vaapi in VAAPI args: %s", argStr)
	}
}

func TestBuildHLS_ToneMap_Software(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/hdr.mkv",
		Encoder:       EncoderSoftware,
		NeedsToneMap:  true,
		HasZscale:     true,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "zscale") {
		t.Errorf("expected zscale tonemap filter in args: %s", argStr)
	}
	if !strings.Contains(argStr, "tonemap") {
		t.Errorf("expected tonemap in filter args: %s", argStr)
	}
}

func TestBuildHLS_AudioCopy_NoChannelArgs(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderSoftware,
		AudioCodec:    "copy",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if strings.Contains(argStr, "-ac ") {
		t.Errorf("expected no -ac for copy audio, got: %s", argStr)
	}
	if !strings.Contains(argStr, "-c:a copy") {
		t.Errorf("expected -c:a copy in args: %s", argStr)
	}
}

func TestBuildHLS_Subtitles(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:       "/media/movie.mkv",
		Encoder:         EncoderSoftware,
		AudioCodec:      "aac",
		SubtitleStreams: []int{0, 2},
		SessionDir:      "/tmp/sessions/x",
		SegmentPrefix:   "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-c:s webvtt") {
		t.Errorf("expected WebVTT subtitle codec: %s", argStr)
	}
	if !strings.Contains(argStr, "sub0.vtt") {
		t.Errorf("expected sub0.vtt output: %s", argStr)
	}
	if !strings.Contains(argStr, "sub1.vtt") {
		t.Errorf("expected sub1.vtt output: %s", argStr)
	}
}

func TestBuildHLS_KeyframeForcing(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderSoftware,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-force_key_frames") {
		t.Errorf("expected -force_key_frames in args: %s", argStr)
	}
	if !strings.Contains(argStr, "-sc_threshold 0") {
		t.Errorf("expected -sc_threshold 0 in args: %s", argStr)
	}
}

func TestBuildDirectStream_RequiredArgs(t *testing.T) {
	args := BuildDirectStream("/media/movie.mkv", "/tmp/sessions/abc", 0)
	argStr := strings.Join(args, " ")

	required := []string{
		"-hide_banner",
		"-i /media/movie.mkv",
		"-c copy",
		"-f hls",
		"seg%05d.ts",
		"index.m3u8",
	}
	for _, r := range required {
		if !strings.Contains(argStr, r) {
			t.Errorf("BuildDirectStream: expected %q in: %s", r, argStr)
		}
	}
}

func TestBuildDirectStream_StartOffset(t *testing.T) {
	args := BuildDirectStream("/media/movie.mkv", "/tmp/sessions/abc", 120.0)
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-ss 120.000") {
		t.Errorf("expected -ss 120.000 in args: %s", argStr)
	}
}

func TestBuildVideoFilter_Scale_Software(t *testing.T) {
	vf := buildVideoFilter(BuildArgs{
		Encoder: EncoderSoftware,
		Width:   1280,
		Height:  720,
	})
	if !strings.Contains(vf, "scale=1280:720") {
		t.Errorf("expected scale filter, got: %s", vf)
	}
	if !strings.Contains(vf, "pad=1280:720") {
		t.Errorf("expected pad filter, got: %s", vf)
	}
}

func TestBuildHLS_CustomAudioBitrate(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:        "/media/movie.mkv",
		Encoder:          EncoderSoftware,
		AudioCodec:       "aac",
		AudioBitrateKbps: 192,
		SessionDir:       "/tmp/sessions/x",
		SegmentPrefix:    "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-b:a 192k") {
		t.Errorf("expected custom audio bitrate -b:a 192k in args: %s", argStr)
	}
}

// TestBuildHLS_AACAudioSyncFilter guards the resample filter that fixes
// the "audio is delayed at the start" bug on remux sessions starting
// from the beginning of file. Guards: (a) async=1 is present at file
// start, (b) the filter is OMITTED for mid-stream -ss seeks because
// it swallows segment 0's audio packets, (c) first_pts=0 never
// appears (it aborts the HLS muxer).
func TestBuildHLS_AACAudioSyncFilter(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       "copy", // remux — the case where the bug was worst
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-af aresample=async=1") {
		t.Errorf("expected -af aresample=async=1 at file start: %s", argStr)
	}
	if strings.Contains(argStr, "first_pts=0") {
		t.Errorf("first_pts=0 must not appear — it aborts the muxer on non-zero-start sources: %s", argStr)
	}
}

// TestBuildHLS_AACAudioSyncFilter_MidStream confirms the resampler is
// skipped when -ss > 0. With mid-stream seek the filter buffers
// initial samples for its resync computation and never flushes them,
// shipping segment 0 with zero audio packets and stalling MSE.
func TestBuildHLS_AACAudioSyncFilter_MidStream(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		StartOffset:   3542.5,
		Encoder:       "copy",
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if strings.Contains(argStr, "aresample") {
		t.Errorf("aresample filter must NOT appear with mid-stream -ss — it strips seg 0 audio: %s", argStr)
	}
}

// TestBuildHLS_AudioCopy_NoResampleFilter guards the inverse: the
// resample filter must only apply when we're re-encoding to AAC.
// Applying it to audio-copy would force FFmpeg to decode+re-encode
// the source audio, defeating the copy.
func TestBuildHLS_AudioCopy_NoResampleFilter(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       "copy",
		AudioCodec:    "copy",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if strings.Contains(argStr, "-af aresample") {
		t.Errorf("audio-copy should not set -af aresample: %s", argStr)
	}
}

// TestBuildHLS_BurnSubtitle confirms the `subtitles` filter lands in
// the -vf chain when BurnSubtitleStream is set, and that the input
// path is single-quoted so colons / spaces / paths with parens
// don't break the filter parser.
func TestBuildHLS_BurnSubtitle(t *testing.T) {
	si := 1
	args := BuildHLS(BuildArgs{
		InputPath:          "/media/Movies/Foo (2024)/Foo.mkv",
		Encoder:            EncoderSoftware,
		Width:              1920,
		Height:             1080,
		BitrateKbps:        4000,
		AudioCodec:         "aac",
		BurnSubtitleStream: &si,
		SessionDir:         "/tmp/sessions/x",
		SegmentPrefix:      "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "subtitles='/media/Movies/Foo (2024)/Foo.mkv':si=1") {
		t.Errorf("expected single-quoted subtitles filter with si=1, got: %s", argStr)
	}
}

// TestBuildHLS_NoBurnSubtitle_WhenNil confirms the default path
// stays clean — a nil BurnSubtitleStream must not add any
// subtitles filter, even when other filters (scale / tonemap) run.
func TestBuildHLS_NoBurnSubtitle_WhenNil(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderSoftware,
		Width:         1280,
		Height:        720,
		BitrateKbps:   2000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	if strings.Contains(strings.Join(args, " "), "subtitles=") {
		t.Errorf("subtitles filter must not appear with nil BurnSubtitleStream: %v", args)
	}
}

// TestSubtitleBurnFilter_EscapesSingleQuote checks the rare-but-real
// case of a path with an apostrophe in it (`/media/Bob's Movies/...`).
// Without escape the filter parser treats the apostrophe as a string
// terminator and the encode aborts with "Invalid argument."
func TestSubtitleBurnFilter_EscapesSingleQuote(t *testing.T) {
	got := subtitleBurnFilter("/media/Bob's Movies/x.mkv", 0)
	if !strings.Contains(got, `Bob\'s Movies`) {
		t.Errorf("expected escaped apostrophe, got: %s", got)
	}
}

// TestBuildHLS_Software_StripsTo8Bit guards the libx264 10-bit-input
// fix surfaced by the v2.1 libx264 live matrix run against Chainsaw Man
// (10-bit AV1 source). Without format=yuv420p in the filter chain,
// libx264 emits 10-bit High 10 profile H.264 — a valid bitstream but
// browsers (Chromium especially) have no 10-bit H.264 decoder, so the
// player falls back to no-video. Same fix shape as the existing AMF /
// QSV / NVENC strip; libx264 had been overlooked.
func TestBuildHLS_Software_StripsTo8Bit(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/anime.mkv",
		Encoder:       EncoderSoftware,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "format=yuv420p") {
		t.Errorf("libx264 must strip to 8-bit: %s", argStr)
	}
}

// TestSubtitleBurnFilter_WindowsPath guards the Windows path-handling
// fix surfaced by the v2.1 burn-in integration test against Goodfellas.
// Backslashes get stripped by the filter parser as escape introducers
// and the drive-letter colon (`C:`) is otherwise parsed as a filter
// key=value separator — both paths in `C:\movies\Foo (1990)\bar.mkv`
// would crash ffmpeg with "Unable to parse 'original_size' option
// value 'moviesFoo (1990)bar.mkv'". The fix flips backslashes to
// forward slashes and escapes every colon in the path.
func TestSubtitleBurnFilter_WindowsPath(t *testing.T) {
	got := subtitleBurnFilter(`C:\movies\Foo (1990)\bar.mkv`, 2)
	want := `subtitles='C\:/movies/Foo (1990)/bar.mkv':si=2`
	if got != want {
		t.Errorf("Windows path filter: got %q, want %q", got, want)
	}
}

// TestIsHEVCEncoder_AllVariants confirms every HEVC encoder we
// support is recognized — the segment-format selector and the
// HEVC-output codec tag depend on this flag being right.
func TestIsHEVCEncoder_AllVariants(t *testing.T) {
	hevc := []Encoder{EncoderHEVCNVENC, EncoderHEVCQSV, EncoderHEVCVAAPI, EncoderHEVCAMF, EncoderHEVCSoftware}
	for _, e := range hevc {
		if !IsHEVCEncoder(e) {
			t.Errorf("IsHEVCEncoder(%q) = false, want true", e)
		}
	}
	notHEVC := []Encoder{EncoderNVENC, EncoderQSV, EncoderVAAPI, EncoderAMF, EncoderSoftware,
		EncoderAV1Software, EncoderAV1NVENC, EncoderAV1QSV}
	for _, e := range notHEVC {
		if IsHEVCEncoder(e) {
			t.Errorf("IsHEVCEncoder(%q) = true, want false", e)
		}
	}
}

// TestIsAV1Encoder covers the same matrix for AV1 — used to gate the
// av01 tag and the fMP4 segment format.
func TestIsAV1Encoder(t *testing.T) {
	av1 := []Encoder{EncoderAV1Software, EncoderAV1NVENC, EncoderAV1QSV}
	for _, e := range av1 {
		if !IsAV1Encoder(e) {
			t.Errorf("IsAV1Encoder(%q) = false, want true", e)
		}
	}
	if IsAV1Encoder(EncoderHEVCNVENC) {
		t.Error("HEVC encoder must not register as AV1")
	}
}

// TestBuildHLS_HEVCQSV_FMP4 verifies the new HEVC encoders trigger
// the fMP4 segment format + hvc1 tag, not the default mpegts.
func TestBuildHLS_HEVCQSV_FMP4(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderHEVCQSV,
		Width:         3840,
		Height:        2160,
		BitrateKbps:   12000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-c:v hevc_qsv") {
		t.Errorf("expected -c:v hevc_qsv, got: %s", argStr)
	}
	if !strings.Contains(argStr, "-hls_segment_type fmp4") {
		t.Errorf("HEVC must use fMP4 segments: %s", argStr)
	}
	if !strings.Contains(argStr, "-tag:v hvc1") {
		t.Errorf("HEVC must carry hvc1 tag: %s", argStr)
	}
	if !strings.Contains(argStr, "-profile:v main") {
		t.Errorf("HEVC QSV must set Main profile: %s", argStr)
	}
}

// TestBuildHLS_AV1Source_Remux_FMP4 verifies AV1 source passthrough
// (`-c:v copy`) routes through fMP4 with av01 tag. mpegts has no
// stream type for AV1, so an AV1 source remuxed into mpegts segments
// crashes the muxer ("Could not find tag for codec av1 in stream #0")
// and the session sits idle until the playlist times out.
func TestBuildHLS_AV1Source_Remux_FMP4(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/anime.mkv",
		Encoder:       "copy",
		IsAV1:         true,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")

	if !strings.Contains(argStr, "-c:v copy") {
		t.Errorf("expected -c:v copy: %s", argStr)
	}
	if !strings.Contains(argStr, "-hls_segment_type fmp4") {
		t.Errorf("AV1 source remux must use fMP4 (mpegts has no AV1 stream type): %s", argStr)
	}
	if !strings.Contains(argStr, "-tag:v av01") {
		t.Errorf("AV1 remux must carry av01 tag: %s", argStr)
	}
	if !strings.Contains(argStr, ".m4s") {
		t.Errorf("AV1 remux must use .m4s segments: %s", argStr)
	}
	if !strings.Contains(argStr, "-hls_fmp4_init_filename init.mp4") {
		t.Errorf("AV1 remux must emit fMP4 init: %s", argStr)
	}
	if strings.Contains(argStr, "-hls_segment_type mpegts") {
		t.Errorf("AV1 remux must NOT use mpegts: %s", argStr)
	}
}

// TestBuildHLS_ReadRate_PacingFlags verifies the input-rate pacing
// flags appear before `-i` when ReadRate is set. Production worker
// sets ReadRate=1.0 so ffmpeg stays alive for the full playback
// duration; without pacing, NVDEC + NVENC encodes a 24 min episode
// in ~65 s and the post-completion cleanup wipes segments before
// the player has finished.
func TestBuildHLS_ReadRate_PacingFlags(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:            "/media/movie.mkv",
		Encoder:              EncoderNVENC,
		Width:                1920,
		Height:               1080,
		BitrateKbps:           8000,
		AudioCodec:           "aac",
		ReadRate:             1.0,
		ReadRateInitialBurst: 30,
		SessionDir:           "/tmp/sessions/x",
		SegmentPrefix:        "seg",
	})
	argStr := strings.Join(args, " ")

	if !strings.Contains(argStr, "-readrate 1.00") {
		t.Errorf("expected -readrate 1.00 with ReadRate=1.0: %s", argStr)
	}
	if !strings.Contains(argStr, "-readrate_initial_burst 30") {
		t.Errorf("expected -readrate_initial_burst 30: %s", argStr)
	}
	// readrate must come BEFORE -i to apply to input reading.
	rrIdx := strings.Index(argStr, "-readrate")
	iIdx := strings.Index(argStr, " -i ")
	if rrIdx < 0 || iIdx < 0 || rrIdx > iIdx {
		t.Errorf("readrate must precede -i: %s", argStr)
	}
}

// TestBuildHLS_ReadRate_ZeroSkips verifies the pacing flag is omitted
// entirely when ReadRate is unset. Integration tests rely on this so
// `-t 8` can bound a multi-output run without the WebVTT extraction
// context waiting real-time for a 2.5 h subtitle stream.
func TestBuildHLS_ReadRate_ZeroSkips(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderSoftware,
		Width:         1280,
		Height:        720,
		BitrateKbps:   2000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if strings.Contains(argStr, "-readrate") {
		t.Errorf("ReadRate=0 must omit -readrate flag: %s", argStr)
	}
}

// TestBuildHLS_AV1Source_NVENC_UsesCUDADecode verifies AV1 source +
// NVENC encode pins the decode path to NVDEC (`-hwaccel cuda
// -hwaccel_output_format cuda`) and routes scaling through scale_cuda
// so frames stay in VRAM. dav1d software decode runs at 3-5× real-time
// on 1080p and falls behind the encoder after ~60-70 s of output —
// surfaces as ffmpeg producing seg 0-13 then stalling.
func TestBuildHLS_AV1Source_NVENC_UsesCUDADecode(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/sololeveling.mkv",
		Encoder:       EncoderNVENC,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		IsAV1:         true,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")

	if !strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("AV1 + NVENC must use -hwaccel cuda for NVDEC: %s", argStr)
	}
	if !strings.Contains(argStr, "-hwaccel_output_format cuda") {
		t.Errorf("AV1 + NVENC must keep frames in VRAM (-hwaccel_output_format cuda): %s", argStr)
	}
	if !strings.Contains(argStr, "scale_cuda=w=1920:h=1080") {
		t.Errorf("AV1 + NVENC must use scale_cuda to stay in VRAM: %s", argStr)
	}
	// scale_cuda emits nv12 (8-bit) directly — the CPU `format=yuv420p`
	// strip would force a hwdownload that defeats the VRAM pipeline.
	if strings.Contains(argStr, "format=yuv420p") {
		t.Errorf("AV1 + NVENC CUDA path must not append CPU format=yuv420p strip: %s", argStr)
	}
	if !strings.Contains(argStr, "-c:v h264_nvenc") {
		t.Errorf("expected -c:v h264_nvenc: %s", argStr)
	}
}

// TestBuildHLS_AV1Source_NVENC_HDRSkipsCUDADecode verifies the CUDA
// decode carve-out does NOT activate when HDR tonemapping is required.
// scale_cuda + the zscale software tonemap chain don't compose without
// extra hwdownload/hwupload plumbing the broader pipeline doesn't yet
// guarantee — HDR + AV1 stays on the software-decode path.
func TestBuildHLS_AV1Source_NVENC_HDRSkipsCUDADecode(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/hdr_av1.mkv",
		Encoder:       EncoderNVENC,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		IsAV1:         true,
		NeedsToneMap:  true,
		HasZscale:     true,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")

	if strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("AV1 + NVENC + HDR must stay on software decode: %s", argStr)
	}
	if strings.Contains(argStr, "scale_cuda") {
		t.Errorf("AV1 + NVENC + HDR must not use scale_cuda: %s", argStr)
	}
	if !strings.Contains(argStr, "zscale") {
		t.Errorf("AV1 + NVENC + HDR should still tonemap via zscale: %s", argStr)
	}
}

// TestBuildHLS_H264_NVENC_NoCUDADecode guards the inverse: H.264 source
// + NVENC must NOT trigger the CUDA decode carve-out (the AV1 fix
// should not regress the uniform software-decode pipeline that NVENC
// uses for HEVC / H.264 sources to dodge mainline-ffmpeg + driver
// fragility).
func TestBuildHLS_H264_NVENC_NoCUDADecode(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderNVENC,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		IsAV1:         false,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")

	if strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("H.264 + NVENC must not activate CUDA decode (AV1-only carve-out): %s", argStr)
	}
	if strings.Contains(argStr, "scale_cuda") {
		t.Errorf("H.264 + NVENC must use software scale: %s", argStr)
	}
}

// TestBuildHLS_H264_Remux_StaysMpegTS guards the inverse: regular
// H.264 source remux still uses mpegts (the AV1 fix should not flip
// every video-copy session into fMP4).
func TestBuildHLS_H264_Remux_StaysMpegTS(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       "copy",
		IsAV1:         false,
		IsHEVC:        false,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-hls_segment_type mpegts") {
		t.Errorf("H.264 remux should stay on mpegts: %s", argStr)
	}
	if strings.Contains(argStr, "-tag:v av01") {
		t.Errorf("H.264 remux must not carry av01 tag: %s", argStr)
	}
}

// TestBuildHLS_AV1Software_FMP4 verifies AV1 picks the av01 tag and
// fMP4 segments. SVT-AV1 preset 8 should be present (live-stream
// sweet spot per the SVT maintainer guidance).
func TestBuildHLS_AV1Software_FMP4(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderAV1Software,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   3000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-c:v libsvtav1") {
		t.Errorf("expected libsvtav1: %s", argStr)
	}
	if !strings.Contains(argStr, "-hls_segment_type fmp4") {
		t.Errorf("AV1 must use fMP4: %s", argStr)
	}
	if !strings.Contains(argStr, "-tag:v av01") {
		t.Errorf("AV1 must carry av01 tag: %s", argStr)
	}
	if !strings.Contains(argStr, "-preset 8") {
		t.Errorf("SVT-AV1 must use preset 8 for live: %s", argStr)
	}
}

// TestBuildHLS_HEVCVAAPI_HwUploadFilter confirms the HEVC VAAPI path
// inherits the same hwupload + scale_vaapi treatment as plain VAAPI
// — without this the encoder fails fast with "Impossible to convert
// between the formats supported by the filter graph_0 and h264_vaapi".
func TestBuildHLS_HEVCVAAPI_HwUploadFilter(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderHEVCVAAPI,
		IsVAAPI:       true,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   4000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "hwupload") {
		t.Errorf("HEVC VAAPI must add hwupload filter: %s", argStr)
	}
	if !strings.Contains(argStr, "scale_vaapi") {
		t.Errorf("HEVC VAAPI must use scale_vaapi: %s", argStr)
	}
	if !strings.Contains(argStr, "-vaapi_device") {
		t.Errorf("HEVC VAAPI must init vaapi device: %s", argStr)
	}
}

func TestBuildHLS_AMF_Flags(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderAMF,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")

	// AMF must NOT auto-pick a D3D11 device for input — on dual-adapter
	// hosts (NVIDIA dGPU + AMD iGPU on Ryzen X-series) `-hwaccel d3d11va`
	// grabs the NVIDIA card and the AMF encoder then ENODEVs trying to
	// use the same device. Software input decode is the only safe default
	// without a per-host adapter-index probe.
	if strings.Contains(argStr, "-hwaccel d3d11va") {
		t.Errorf("AMF should NOT use -hwaccel d3d11va (dual-GPU adapter pickup hazard): %s", argStr)
	}
	// AMF encoder codec.
	if !strings.Contains(argStr, "-c:v h264_amf") {
		t.Errorf("expected -c:v h264_amf in args: %s", argStr)
	}
	// AMF-specific flags.
	if !strings.Contains(argStr, "-quality balanced") {
		t.Errorf("expected -quality balanced in AMF args: %s", argStr)
	}
	if !strings.Contains(argStr, "-rc cbr") {
		t.Errorf("expected -rc cbr in AMF args: %s", argStr)
	}
	// AMF uses both: -g as a defensive upper bound + -force_key_frames
	// to land segment boundaries on exact SegmentDuration multiples
	// regardless of source fps. The previous -g-only approach baked in
	// a 30fps assumption that broke on 24fps content.
	if !strings.Contains(argStr, "-force_key_frames expr:gte(t,n_forced*4)") {
		t.Errorf("expected -force_key_frames at segment boundaries for AMF: %s", argStr)
	}
	if !strings.Contains(argStr, "-g 120") {
		t.Errorf("expected GOP upper-bound -g 120 for AMF: %s", argStr)
	}
	// Should NOT have NVENC flags.
	if strings.Contains(argStr, "-preset p4") {
		t.Errorf("AMF should not have NVENC preset: %s", argStr)
	}
}

func TestBuildHLS_AMF_NoNVENCHwaccel(t *testing.T) {
	// AMF must not use CUDA/NVENC hwaccel.
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderAMF,
		BitrateKbps:   4000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if strings.Contains(argStr, "cuda") {
		t.Errorf("AMF should not reference cuda: %s", argStr)
	}
	if strings.Contains(argStr, "extra_hw_frames") {
		t.Errorf("AMF should not have extra_hw_frames: %s", argStr)
	}
}

func TestBuildHLS_NVENC_NoCudaHwaccel(t *testing.T) {
	// NVENC now uses software decode + software scale + NVENC encode —
	// the all-CUDA pipeline (`-hwaccel cuda`, `scale_cuda`,
	// `tonemap_cuda`/`tonemap_opencl`) was fragile across mainline
	// ffmpeg 8.x + recent NVIDIA drivers (No decoder surfaces left,
	// h264_nvenc -22 EINVAL on the cuda-frame chain). Same shape as
	// AMF and QSV; uniform pipeline trades some efficiency for
	// reliability across every source / driver / build combination.
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderNVENC,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")

	if strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("NVENC should NOT use -hwaccel cuda — software decode is the reliable path: %s", argStr)
	}
	if strings.Contains(argStr, "-hwaccel_output_format cuda") {
		t.Errorf("NVENC should NOT use -hwaccel_output_format cuda: %s", argStr)
	}
	if strings.Contains(argStr, "scale_cuda") {
		t.Errorf("NVENC should NOT use scale_cuda — software scale only: %s", argStr)
	}
	if strings.Contains(argStr, "tonemap_cuda") {
		t.Errorf("tonemap_cuda not in mainline ffmpeg; should never emit: %s", argStr)
	}
	if !strings.Contains(argStr, "scale=1920:1080:force_original_aspect_ratio=decrease") {
		t.Errorf("expected software scale=W:H filter: %s", argStr)
	}
	// The encoder itself + keyframe / GOP discipline still needs to be right.
	if !strings.Contains(argStr, "-c:v h264_nvenc") {
		t.Errorf("expected -c:v h264_nvenc: %s", argStr)
	}
	if !strings.Contains(argStr, "-force_key_frames expr:gte(t,n_forced*4)") {
		t.Errorf("expected -force_key_frames at segment boundaries: %s", argStr)
	}
	if !strings.Contains(argStr, "-g 120") {
		t.Errorf("expected GOP upper-bound -g 120: %s", argStr)
	}
}

func TestBuildHLS_NVENC_TonemapFallback(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:      "/media/hdr_movie.mkv",
		Encoder:        EncoderNVENC,
		Width:          1920,
		Height:         1080,
		BitrateKbps:    8000,
		NeedsToneMap:   true,
		HasTonemapCuda: false,
		HasZscale:      true,
		AudioCodec:     "aac",
		SessionDir:     "/tmp/sessions/x",
		SegmentPrefix:  "seg",
	})
	argStr := strings.Join(args, " ")

	// No CUDA hwaccel — software decode + zscale tonemap + NVENC encode.
	if strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("should NOT use CUDA hwaccel without tonemap_cuda: %s", argStr)
	}
	// Software tonemap via zscale.
	if !strings.Contains(argStr, "zscale") {
		t.Errorf("expected zscale software tonemap fallback: %s", argStr)
	}
	if !strings.Contains(argStr, "tonemap=tonemap=hable") {
		t.Errorf("expected hable tonemap: %s", argStr)
	}
	// Still uses NVENC for encoding.
	if !strings.Contains(argStr, "-c:v h264_nvenc") {
		t.Errorf("expected h264_nvenc encoder despite tonemap fallback: %s", argStr)
	}
}

func TestBuildHLS_NVENC_HDRSourceUsesZscale(t *testing.T) {
	// Even when tonemap_cuda or tonemap_opencl is "available", NVENC
	// goes through zscale because the cuda-frame chain that fed those
	// filters is gone — input frames are now in CPU memory. zscale
	// is the only HDR→SDR path that fits a software-decode pipeline.
	args := BuildHLS(BuildArgs{
		InputPath:        "/media/hdr_movie.mkv",
		Encoder:          EncoderNVENC,
		Width:            1920,
		Height:           1080,
		BitrateKbps:      8000,
		NeedsToneMap:     true,
		HasTonemapCuda:   true,  // even if reported, we no longer use it
		HasTonemapOpenCL: true,  // same
		HasZscale:        true,
		AudioCodec:       "aac",
		SessionDir:       "/tmp/sessions/x",
		SegmentPrefix:    "seg",
	})
	argStr := strings.Join(args, " ")

	if !strings.Contains(argStr, "zscale=t=linear:npl=100") {
		t.Errorf("expected zscale tonemap chain: %s", argStr)
	}
	if !strings.Contains(argStr, "tonemap=tonemap=hable") {
		t.Errorf("expected hable tonemap operator: %s", argStr)
	}
	if strings.Contains(argStr, "tonemap_cuda") {
		t.Errorf("tonemap_cuda must not appear (cuda pipeline retired): %s", argStr)
	}
	if strings.Contains(argStr, "tonemap_opencl") {
		t.Errorf("tonemap_opencl must not appear (cuda pipeline retired): %s", argStr)
	}
	if strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("NVENC must not init CUDA input hwaccel: %s", argStr)
	}
	if strings.Contains(argStr, "scale_cuda") {
		t.Errorf("NVENC must not use scale_cuda: %s", argStr)
	}
	// Must still END the filter chain at yuv420p so h264_nvenc accepts the input.
	if !strings.Contains(argStr, "format=yuv420p") {
		t.Errorf("expected format=yuv420p as final filter step: %s", argStr)
	}
}

func TestBuildHLS_NVENC_NoTonemapAvailable(t *testing.T) {
	// No tonemap_cuda, no tonemap_opencl, no zscale — should skip tonemapping entirely.
	args := BuildHLS(BuildArgs{
		InputPath:        "/media/hdr_movie.mkv",
		Encoder:          EncoderNVENC,
		Width:            1920,
		Height:           1080,
		BitrateKbps:      8000,
		NeedsToneMap:     true,
		HasTonemapCuda:   false,
		HasTonemapOpenCL: false,
		HasZscale:        false,
		AudioCodec:       "aac",
		SessionDir:       "/tmp/sessions/x",
		SegmentPrefix:    "seg",
	})
	argStr := strings.Join(args, " ")

	// Should not have any tonemap filter.
	if strings.Contains(argStr, "tonemap") {
		t.Errorf("should skip tonemapping when no tonemap filter available: %s", argStr)
	}
	// Should still produce output (not crash).
	if !strings.Contains(argStr, "-c:v h264_nvenc") {
		t.Errorf("should still encode with NVENC: %s", argStr)
	}
	// Without any GPU tonemap, falls back to software decode (no CUDA hwaccel).
	if strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("should not use CUDA hwaccel without any GPU tonemap: %s", argStr)
	}
}

func TestBuildHLS_HEVC_NVENC_HDRSourceUsesZscale(t *testing.T) {
	// HEVC NVENC HDR → still goes through zscale (matches H.264 NVENC).
	// The cuda-frame pipeline that drove tonemap_cuda / tonemap_opencl
	// was retired; HEVC NVENC follows the same software-decode +
	// zscale + GPU-encode shape as H.264 NVENC.
	args := BuildHLS(BuildArgs{
		InputPath:        "/media/4k_hdr_movie.mkv",
		Encoder:          EncoderHEVCNVENC,
		Width:            3840,
		Height:           2160,
		BitrateKbps:      24000,
		NeedsToneMap:     true,
		HasTonemapCuda:   true,  // even if reported, we no longer use it
		HasTonemapOpenCL: true,  // same
		HasZscale:        true,
		AudioCodec:       "aac",
		SessionDir:       "/tmp/sessions/x",
		SegmentPrefix:    "seg",
	})
	argStr := strings.Join(args, " ")

	if !strings.Contains(argStr, "-c:v hevc_nvenc") {
		t.Errorf("expected -c:v hevc_nvenc: %s", argStr)
	}
	if !strings.Contains(argStr, "zscale=t=linear:npl=100") {
		t.Errorf("expected zscale tonemap chain: %s", argStr)
	}
	if strings.Contains(argStr, "tonemap_cuda") {
		t.Errorf("tonemap_cuda must not appear (cuda pipeline retired): %s", argStr)
	}
	if strings.Contains(argStr, "tonemap_opencl") {
		t.Errorf("tonemap_opencl must not appear (cuda pipeline retired): %s", argStr)
	}
	if strings.Contains(argStr, "-init_hw_device opencl=ocl") {
		t.Errorf("OpenCL device init must not appear: %s", argStr)
	}
	if strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("HEVC NVENC must not init CUDA input hwaccel: %s", argStr)
	}
	if !strings.Contains(argStr, "-hls_segment_type fmp4") {
		t.Errorf("HEVC output must use fMP4 segments: %s", argStr)
	}
	if !strings.Contains(argStr, "-tag:v hvc1") {
		t.Errorf("HEVC output must have hvc1 tag: %s", argStr)
	}
}

func TestBuildHLS_HEVC_NVENC(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/4k_movie.mkv",
		Encoder:       EncoderHEVCNVENC,
		Width:         3840,
		Height:        2160,
		BitrateKbps:   24000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")

	// HEVC NVENC follows the same software-decode pipeline as H.264 NVENC —
	// no CUDA input hwaccel, no scale_cuda. Same reliability rationale.
	if strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("HEVC NVENC must NOT use -hwaccel cuda: %s", argStr)
	}
	if strings.Contains(argStr, "scale_cuda") {
		t.Errorf("HEVC NVENC must NOT use scale_cuda: %s", argStr)
	}
	if !strings.Contains(argStr, "-c:v hevc_nvenc") {
		t.Errorf("expected -c:v hevc_nvenc: %s", argStr)
	}
	if !strings.Contains(argStr, "-profile:v main") {
		t.Errorf("expected HEVC main profile: %s", argStr)
	}
	if strings.Contains(argStr, "-level") {
		t.Errorf("HEVC NVENC should auto-select level, not force one: %s", argStr)
	}
	if !strings.Contains(argStr, "-g 120") {
		t.Errorf("expected fixed GOP for NVENC: %s", argStr)
	}
	// Software scale is the new default for the GPU encoders.
	if !strings.Contains(argStr, "scale=3840:2160:force_original_aspect_ratio=decrease") {
		t.Errorf("expected software scale=W:H filter: %s", argStr)
	}
	// HEVC output must use fMP4 segments for HLS.js compatibility.
	if !strings.Contains(argStr, "-hls_segment_type fmp4") {
		t.Errorf("expected fMP4 segment type for HEVC: %s", argStr)
	}
	if !strings.Contains(argStr, ".m4s") {
		t.Errorf("expected .m4s segment extension for HEVC: %s", argStr)
	}
	if !strings.Contains(argStr, "-tag:v hvc1") {
		t.Errorf("expected -tag:v hvc1 for browser HEVC playback: %s", argStr)
	}
	if !strings.Contains(argStr, "-hls_fmp4_init_filename init.mp4") {
		t.Errorf("expected single-rendition fMP4 init filename (init.mp4): %s", argStr)
	}
	if strings.Contains(argStr, "-var_stream_map") {
		t.Errorf("HLS-fMP4 should be single muxed rendition; var_stream_map demux is shaka-MSE residue: %s", argStr)
	}
	if strings.Contains(argStr, "-master_pl_name") {
		t.Errorf("HLS-fMP4 should not need a master playlist (single rendition): %s", argStr)
	}
}

func TestBuildHLS_VideoCopy_EventPlaylist(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       "copy",
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-c:v copy") {
		t.Errorf("expected -c:v copy: %s", argStr)
	}
	if !strings.Contains(argStr, "-hls_playlist_type event") {
		t.Errorf("remux should use event playlist type: %s", argStr)
	}
	// Should not have encoder-specific flags.
	if strings.Contains(argStr, "-preset") {
		t.Errorf("video copy should not have encoder preset: %s", argStr)
	}
	if strings.Contains(argStr, "-hwaccel") {
		t.Errorf("video copy should not have hwaccel: %s", argStr)
	}
}

func TestBuildHLS_AudioStreamIndex(t *testing.T) {
	// Default audio stream (index -1 / not set).
	args := BuildHLS(BuildArgs{
		InputPath:        "/media/movie.mkv",
		Encoder:          EncoderSoftware,
		AudioCodec:       "aac",
		AudioStreamIndex: -1,
		SessionDir:       "/tmp/sessions/x",
		SegmentPrefix:    "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-map 0:a:0") {
		t.Errorf("expected default audio map 0:a:0: %s", argStr)
	}

	// Specific audio stream index.
	args = BuildHLS(BuildArgs{
		InputPath:        "/media/movie.mkv",
		Encoder:          EncoderSoftware,
		AudioCodec:       "aac",
		AudioStreamIndex: 2,
		SessionDir:       "/tmp/sessions/x",
		SegmentPrefix:    "seg",
	})
	argStr = strings.Join(args, " ")
	if !strings.Contains(argStr, "-map 0:a:2") {
		t.Errorf("expected audio map 0:a:2: %s", argStr)
	}
}

func TestBuildHLS_MaxMuxingQueueSize(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderNVENC,
		BitrateKbps:   20000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-max_muxing_queue_size 2048") {
		t.Errorf("expected -max_muxing_queue_size 2048: %s", argStr)
	}
	if !strings.Contains(argStr, "-max_delay 5000000") {
		t.Errorf("expected -max_delay 5000000: %s", argStr)
	}
}

func TestBuildVideoFilter_Empty_NoScaleNoTonemap(t *testing.T) {
	// libx264 / NVENC / AMF / QSV always end the chain at format=yuv420p
	// to keep the encoder on the 8-bit path (browsers can't decode 10-bit
	// H.264 High 10 from libx264, and the GPU encoders reject 10-bit
	// input outright). The chain is otherwise empty when no scale and
	// no tonemap are requested. HEVC and AV1 software encoders allow
	// 10-bit (Main10 / AV1 10-bit profiles) and produce a truly empty
	// chain in this case.
	if vf := buildVideoFilter(BuildArgs{Encoder: EncoderSoftware}); vf != "format=yuv420p" {
		t.Errorf("libx264 chain: want format=yuv420p, got: %q", vf)
	}
	if vf := buildVideoFilter(BuildArgs{Encoder: EncoderHEVCSoftware}); vf != "" {
		t.Errorf("libx265 chain should stay empty (allows 10-bit Main10), got: %q", vf)
	}
	if vf := buildVideoFilter(BuildArgs{Encoder: EncoderAV1Software}); vf != "" {
		t.Errorf("libsvtav1 chain should stay empty (handles 10-bit), got: %q", vf)
	}
}

func TestBuildHLS_HEVC_Software(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/4k_hdr_movie.mkv",
		Encoder:       EncoderHEVCSoftware,
		Width:         3840,
		Height:        2160,
		BitrateKbps:   20000,
		NeedsToneMap:  true,
		HasZscale:     true,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")

	if !strings.Contains(argStr, "-c:v libx265") {
		t.Errorf("expected -c:v libx265: %s", argStr)
	}
	// Software HEVC should use zscale tonemap when NeedsToneMap is set.
	if !strings.Contains(argStr, "zscale") {
		t.Errorf("expected zscale tonemap for software HEVC: %s", argStr)
	}
	if !strings.Contains(argStr, "tonemap=") {
		t.Errorf("expected tonemap filter for HDR→SDR: %s", argStr)
	}
	// Should NOT use CUDA hwaccel.
	if strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("software encoder should not use CUDA hwaccel: %s", argStr)
	}
	// HEVC software output also needs fMP4 segments.
	if !strings.Contains(argStr, "-hls_segment_type fmp4") {
		t.Errorf("expected fMP4 segment type for HEVC software: %s", argStr)
	}
	if !strings.Contains(argStr, "-tag:v hvc1") {
		t.Errorf("expected -tag:v hvc1 for HEVC software: %s", argStr)
	}
}

func TestBuildHLS_CustomEncoderOpts(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderNVENC,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
		EncoderOpts: EncoderOpts{
			NVENCPreset:  "p1",
			NVENCTune:    "ll",
			NVENCRC:      "cbr",
			MaxrateRatio: 2.0,
		},
	})
	argStr := strings.Join(args, " ")

	if !strings.Contains(argStr, "-preset p1") {
		t.Errorf("expected custom preset p1: %s", argStr)
	}
	if !strings.Contains(argStr, "-tune ll") {
		t.Errorf("expected custom tune ll: %s", argStr)
	}
	if !strings.Contains(argStr, "-rc cbr") {
		t.Errorf("expected custom rc cbr: %s", argStr)
	}
	// maxrate = 8000 * 2.0 = 16000
	if !strings.Contains(argStr, "-maxrate 16000k") {
		t.Errorf("expected -maxrate 16000k (bitrate × 2.0): %s", argStr)
	}
}

func TestBuildHLS_DefaultEncoderOpts(t *testing.T) {
	// Zero-value EncoderOpts should produce the same defaults as before.
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderNVENC,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")

	if !strings.Contains(argStr, "-preset p4") {
		t.Errorf("expected default preset p4: %s", argStr)
	}
	if !strings.Contains(argStr, "-tune hq") {
		t.Errorf("expected default tune hq: %s", argStr)
	}
	if !strings.Contains(argStr, "-rc vbr") {
		t.Errorf("expected default rc vbr: %s", argStr)
	}
	// maxrate = 8000 * 1.5 = 12000
	if !strings.Contains(argStr, "-maxrate 12000k") {
		t.Errorf("expected default -maxrate 12000k (bitrate × 1.5): %s", argStr)
	}
}

func TestBuildHLS_MaxrateRatio_Software(t *testing.T) {
	// MaxrateRatio applies to all encoders, not just NVENC.
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderSoftware,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
		EncoderOpts: EncoderOpts{
			MaxrateRatio: 1.2,
		},
	})
	argStr := strings.Join(args, " ")

	// maxrate = 8000 * 1.2 = 9600
	if !strings.Contains(argStr, "-maxrate 9600k") {
		t.Errorf("expected -maxrate 9600k for software encoder: %s", argStr)
	}
}

func TestBuildHLS_HEVC_NVENC_NoTonemap(t *testing.T) {
	// 4K HEVC SDR — NVENC encode, no tonemapping needed.
	args := BuildHLS(BuildArgs{
		InputPath:      "/media/4k_sdr_movie.mkv",
		Encoder:        EncoderHEVCNVENC,
		Width:          3840,
		Height:         2160,
		BitrateKbps:    24000,
		NeedsToneMap:   false,
		HasTonemapCuda: true,
		AudioCodec:     "aac",
		SessionDir:     "/tmp/sessions/x",
		SegmentPrefix:  "seg",
	})
	argStr := strings.Join(args, " ")

	if !strings.Contains(argStr, "-c:v hevc_nvenc") {
		t.Errorf("expected -c:v hevc_nvenc: %s", argStr)
	}
	// HEVC NVENC follows the same uniform software-decode pipeline.
	if strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("HEVC NVENC must NOT use -hwaccel cuda: %s", argStr)
	}
	// No tonemap filters when HDR is not involved.
	if strings.Contains(argStr, "tonemap") {
		t.Errorf("should not have tonemap filter without HDR content: %s", argStr)
	}
	// HEVC output always uses fMP4 regardless of tonemap.
	if !strings.Contains(argStr, "-hls_segment_type fmp4") {
		t.Errorf("expected fMP4 for HEVC output: %s", argStr)
	}
}
