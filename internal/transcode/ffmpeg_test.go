package transcode

import (
	"strings"
	"testing"
)

func TestBuildHLS_ContainsRequiredArgs(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:   "/media/movie.mkv",
		Encoder:     EncoderSoftware,
		Width:       1920,
		Height:      1080,
		BitrateKbps: 8000,
		AudioCodec:  "aac",
		SessionDir:  "/tmp/onscreen/sessions/abc",
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
		InputPath:   "/media/movie.mkv",
		StartOffset: 30.5,
		Encoder:     EncoderSoftware,
		AudioCodec:  "aac",
		SessionDir:  "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-ss 30.500") {
		t.Errorf("expected -ss 30.500 in args: %s", argStr)
	}
}

func TestBuildHLS_NoStartOffset(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:   "/media/movie.mkv",
		Encoder:     EncoderSoftware,
		AudioCodec:  "aac",
		SessionDir:  "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if strings.Contains(argStr, "-ss") {
		t.Errorf("expected no -ss when StartOffset=0, got: %s", argStr)
	}
}

func TestBuildHLS_NVENC_Flags(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:   "/media/movie.mkv",
		Encoder:     EncoderNVENC,
		BitrateKbps: 8000,
		AudioCodec:  "aac",
		SessionDir:  "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	if !strings.Contains(argStr, "-preset p4") {
		t.Errorf("expected NVENC -preset p4 in args: %s", argStr)
	}
	if !strings.Contains(argStr, "-tune ll") {
		t.Errorf("expected NVENC -tune ll in args: %s", argStr)
	}
}

func TestBuildHLS_VAAPI_Filter(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:   "/media/movie.mkv",
		Encoder:     EncoderVAAPI,
		IsVAAPI:     true,
		Width:       1920,
		Height:      1080,
		BitrateKbps: 8000,
		AudioCodec:  "aac",
		SessionDir:  "/tmp/sessions/x",
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
		InputPath:    "/media/hdr.mkv",
		Encoder:      EncoderSoftware,
		NeedsToneMap: true,
		BitrateKbps:  8000,
		AudioCodec:   "aac",
		SessionDir:   "/tmp/sessions/x",
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
		InputPath:   "/media/movie.mkv",
		Encoder:     EncoderSoftware,
		AudioCodec:  "copy",
		SessionDir:  "/tmp/sessions/x",
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
		SubtitleStreams:  []int{0, 2},
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
		InputPath:   "/media/movie.mkv",
		Encoder:     EncoderSoftware,
		AudioCodec:  "aac",
		SessionDir:  "/tmp/sessions/x",
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

	// AMF uses d3d11va hardware decode.
	if !strings.Contains(argStr, "-hwaccel d3d11va") {
		t.Errorf("expected -hwaccel d3d11va in AMF args: %s", argStr)
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
	// Fixed GOP like NVENC (not -force_key_frames).
	if strings.Contains(argStr, "-force_key_frames") {
		t.Errorf("AMF should use fixed GOP, not -force_key_frames: %s", argStr)
	}
	if !strings.Contains(argStr, "-g 120") {
		t.Errorf("expected fixed GOP -g 120 for AMF: %s", argStr)
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

func TestBuildHLS_NVENC_CudaHwaccel(t *testing.T) {
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

	// NVENC uses full CUDA hardware pipeline (Jellyfin-style flags).
	if !strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("expected -hwaccel cuda: %s", argStr)
	}
	if !strings.Contains(argStr, "-hwaccel_output_format cuda") {
		t.Errorf("expected -hwaccel_output_format cuda: %s", argStr)
	}
	if !strings.Contains(argStr, "-hwaccel_flags +unsafe_output") {
		t.Errorf("expected -hwaccel_flags +unsafe_output: %s", argStr)
	}
	if !strings.Contains(argStr, "-threads 1") {
		t.Errorf("expected -threads 1: %s", argStr)
	}
	// GPU-side scale_cuda with format=nv12 for 10-bit → 8-bit conversion.
	if !strings.Contains(argStr, "scale_cuda") {
		t.Errorf("expected scale_cuda filter: %s", argStr)
	}
	if !strings.Contains(argStr, "format=nv12") {
		t.Errorf("expected format=nv12 in scale_cuda: %s", argStr)
	}
	// Fixed GOP (not expression-based).
	if strings.Contains(argStr, "-force_key_frames") {
		t.Errorf("NVENC should use fixed GOP, not -force_key_frames: %s", argStr)
	}
	if !strings.Contains(argStr, "-g 120") {
		t.Errorf("expected fixed GOP -g 120: %s", argStr)
	}
}

func TestBuildHLS_NVENC_TonemapCuda(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:      "/media/hdr_movie.mkv",
		Encoder:        EncoderNVENC,
		Width:          1920,
		Height:         1080,
		BitrateKbps:    8000,
		NeedsToneMap:   true,
		HasTonemapCuda: true,
		AudioCodec:     "aac",
		SessionDir:     "/tmp/sessions/x",
		SegmentPrefix:  "seg",
	})
	argStr := strings.Join(args, " ")

	// HDR→SDR: tonemap_cuda + scale_cuda — frames already in CUDA from hwdec.
	if !strings.Contains(argStr, "tonemap_cuda") {
		t.Errorf("expected tonemap_cuda for HDR→SDR: %s", argStr)
	}
	if !strings.Contains(argStr, "scale_cuda") {
		t.Errorf("expected scale_cuda in tonemap pipeline: %s", argStr)
	}
	if !strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("expected CUDA hwaccel with tonemap_cuda available: %s", argStr)
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

	// HEVC NVENC also gets full CUDA hwaccel pipeline.
	if !strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("expected -hwaccel cuda for HEVC NVENC: %s", argStr)
	}
	if !strings.Contains(argStr, "-c:v hevc_nvenc") {
		t.Errorf("expected -c:v hevc_nvenc: %s", argStr)
	}
	if !strings.Contains(argStr, "-profile:v main") {
		t.Errorf("expected HEVC main profile: %s", argStr)
	}
	if !strings.Contains(argStr, "-level 150") {
		t.Errorf("expected HEVC level 5.0 (150): %s", argStr)
	}
	if !strings.Contains(argStr, "-g 120") {
		t.Errorf("expected fixed GOP for NVENC: %s", argStr)
	}
	if !strings.Contains(argStr, "scale_cuda") {
		t.Errorf("expected scale_cuda for GPU pipeline: %s", argStr)
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
	vf := buildVideoFilter(BuildArgs{
		Encoder: EncoderSoftware,
		// no width/height, no tonemap
	})
	if vf != "" {
		t.Errorf("expected empty filter chain, got: %s", vf)
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
	if !strings.Contains(argStr, "-hwaccel cuda") {
		t.Errorf("expected -hwaccel cuda for HEVC NVENC: %s", argStr)
	}
	// No tonemap filters when HDR is not involved.
	if strings.Contains(argStr, "tonemap") {
		t.Errorf("should not have tonemap filter without HDR content: %s", argStr)
	}
}
