package transcode

import (
	"strings"
	"testing"
)

// These assert the generated ffmpeg command for the opt-in full-VRAM Intel
// paths. They can't prove the pipeline runs on real silicon (no Intel HW in
// CI), but they lock the argv to the documented recipe so a refactor can't
// silently break decode→scale→encode staying in VA surfaces.

func TestBuildHLS_QSVVRAM_HEVC(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderHEVCQSV,
		IsHEVC:        true,
		QSVVRAM:       true,
		Width:         1280,
		Height:        720,
		BitrateKbps:   4000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	s := strings.Join(args, " ")
	// Decode lands in QSV (VA) surfaces, with the source-matched QSV decoder.
	if !strings.Contains(s, "-hwaccel qsv -hwaccel_output_format qsv") {
		t.Errorf("expected QSV full-VRAM hwaccel in args: %s", s)
	}
	if !strings.Contains(s, "-c:v hevc_qsv") {
		t.Errorf("expected hevc_qsv (decode+encode) in args: %s", s)
	}
	// Scale stays on the GPU via vpp_qsv (nv12, 8-bit), not the CPU swScale.
	if !strings.Contains(s, "vpp_qsv=w=1280:h=720:format=nv12") {
		t.Errorf("expected vpp_qsv scale in args: %s", s)
	}
	// Nothing should pull frames back to system memory: no software pad/scale,
	// no CPU format strip, no hwupload.
	if strings.Contains(s, "pad=") {
		t.Errorf("VRAM path must not use the software scale/pad: %s", s)
	}
	if strings.Contains(s, "format=yuv420p") {
		t.Errorf("VRAM path must not insert a CPU format=yuv420p strip: %s", s)
	}
	if strings.Contains(s, "hwupload") {
		t.Errorf("QSV VRAM path decodes into surfaces — no hwupload: %s", s)
	}
}

func TestBuildHLS_VAAPIVRAM_HEVC(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderHEVCVAAPI,
		IsVAAPI:       true,
		IsHEVC:        true,
		VAAPIVRAM:     true,
		Width:         1280,
		Height:        720,
		BitrateKbps:   4000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	s := strings.Join(args, " ")
	if !strings.Contains(s, "-hwaccel vaapi -hwaccel_output_format vaapi") {
		t.Errorf("expected VAAPI full-VRAM hwaccel in args: %s", s)
	}
	if !strings.Contains(s, "scale_vaapi=w=1280:h=720:force_original_aspect_ratio=decrease:format=nv12") {
		t.Errorf("expected scale_vaapi in args: %s", s)
	}
	// Hardware-decoded into VA surfaces, so there's nothing to upload.
	if strings.Contains(s, "hwupload") {
		t.Errorf("VAAPI VRAM path must skip hwupload (frames already on VA surfaces): %s", s)
	}
}

// SDR-only: an HDR source keeps the software/libplacebo tonemap path, so the
// full-VRAM QSV decode must NOT engage even when QSVVRAM is set.
func TestBuildHLS_QSVVRAM_HDR_StaysSoftware(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/hdr.mkv",
		Encoder:       EncoderHEVCQSV,
		IsHEVC:        true,
		QSVVRAM:       true,
		NeedsToneMap:  true,
		HasZscale:     true,
		Width:         1280,
		Height:        720,
		BitrateKbps:   4000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	s := strings.Join(args, " ")
	if strings.Contains(s, "-hwaccel_output_format qsv") {
		t.Errorf("QSV full-VRAM must not engage for HDR (SDR-only): %s", s)
	}
	if strings.Contains(s, "vpp_qsv") {
		t.Errorf("QSV full-VRAM must not engage for HDR (SDR-only): %s", s)
	}
}

// Default (no opt-in) keeps the existing QSV behaviour: a QSV encoder with no
// QSVVRAM flag must not emit the full-VRAM decode args.
func TestBuildHLS_QSV_DefaultNoVRAM(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderHEVCQSV,
		IsHEVC:        true,
		Width:         1280,
		Height:        720,
		BitrateKbps:   4000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/sessions/x",
		SegmentPrefix: "seg",
	})
	s := strings.Join(args, " ")
	if strings.Contains(s, "-hwaccel_output_format qsv") || strings.Contains(s, "vpp_qsv") {
		t.Errorf("default QSV (no opt-in) must not use the full-VRAM path: %s", s)
	}
}
