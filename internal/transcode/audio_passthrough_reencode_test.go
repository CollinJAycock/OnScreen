package transcode

import (
	"strings"
	"testing"
)

// ── Passthrough on a re-encode (v2.7) ───────────────────────────────────────

// The video is re-encoded while the audio track is copied for the client's
// receiver. AC-3 / E-AC-3 ride the codec's usual container (MPEG-TS for H.264,
// fMP4 for HEVC) with no -strict; TrueHD and DTS force fMP4 and -strict -2,
// H.264 included. None of the AAC-only arguments appear, and a seek stays an
// accurate one (the decoder lands on any frame, the copied audio is trimmed to
// the same point).
func TestBuildHLS_AudioPassthrough_Reencode(t *testing.T) {
	cases := []struct {
		name        string
		enc         Encoder
		passthrough bool // TrueHD / DTS (AudioPassthrough)
		segType     string
		strict      bool
		tagV        string
	}{
		{"h264 + ac3 copy: TS", EncoderSoftware, false, "mpegts", false, ""},
		{"h264 nvenc + truehd copy: fMP4", EncoderNVENC, true, "fmp4", true, ""},
		{"hevc nvenc + eac3 copy: fMP4", EncoderHEVCNVENC, false, "fmp4", false, "hvc1"},
		{"hevc + dts copy: fMP4", EncoderHEVCSoftware, true, "fmp4", true, "hvc1"},
		{"av1 + truehd copy: fMP4", EncoderAV1Software, true, "fmp4", true, "av01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := BuildHLS(BuildArgs{
				InputPath: "/media/movie.mkv", Encoder: tc.enc, Width: 1920, Height: 1080, BitrateKbps: 8000,
				AudioCodec: "copy", AudioPassthrough: tc.passthrough, AudioStreamIndex: 1, StartOffset: 600,
				SessionDir: "/tmp/sessions/x", SegmentPrefix: "seg",
			})
			argStr := strings.Join(args, " ")
			if got := argAfter(args, "-c:v"); got != string(tc.enc) {
				t.Errorf("-c:v %q, want the re-encode %q", got, tc.enc)
			}
			for _, want := range []string{"-map 0:v:0", "-map 0:a:1", "-c:a copy", "-hls_segment_type " + tc.segType} {
				if !strings.Contains(argStr, want) {
					t.Errorf("want %q in: %s", want, argStr)
				}
			}
			if got := strings.Contains(argStr, "-strict -2"); got != tc.strict {
				t.Errorf("-strict -2 present=%v, want %v: %s", got, tc.strict, argStr)
			}
			if got := strings.Contains(argStr, "-hls_fmp4_init_filename init.mp4"); got != (tc.segType == "fmp4") {
				t.Errorf("init segment present=%v for %s", got, tc.segType)
			}
			if got := argAfter(args, "-tag:v"); got != tc.tagV {
				t.Errorf("-tag:v %q, want %q", got, tc.tagV)
			}
			for _, aacOnly := range []string{"-ac ", "-b:a", "-af", "make_zero", "-noaccurate_seek"} {
				if strings.Contains(argStr, aacOnly) {
					t.Errorf("%q on a re-encode with copied audio: %s", aacOnly, argStr)
				}
			}
		})
	}
}

// A subtitle burn-in re-encodes the picture through -filter_complex; the
// copied audio is mapped from the source input untouched.
func TestBuildHLS_AudioPassthrough_ReencodeWithBurnIn(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath: "/media/movie.mkv", Encoder: EncoderSoftware, Width: 1920, Height: 1080, BitrateKbps: 8000,
		AudioCodec: "copy", AudioPassthrough: true, AudioStreamIndex: -1,
		BurnSubtitle: &SubtitleBurn{StreamIndex: 4},
		SessionDir:   "/tmp/sessions/x", SegmentPrefix: "seg",
	})
	argStr := strings.Join(args, " ")
	for _, want := range []string{"-filter_complex", "-map [vout]", "-map 0:a:0", "-c:a copy -strict -2", "-hls_segment_type fmp4"} {
		if !strings.Contains(argStr, want) {
			t.Errorf("want %q in: %s", want, argStr)
		}
	}
}

// An ABR rung of a passthrough ladder: the job the API builds for it copies
// the audio and, for TrueHD/DTS, needs the audio_passthrough worker feature.
func TestJobNeeds_ReencodePassthrough(t *testing.T) {
	job := TranscodeJob{Decision: "transcode", AudioCodec: "copy", AudioPassthrough: true, ForceFMP4: true}
	if !job.ForcesFMP4() {
		t.Error("a TrueHD rung must be fMP4")
	}
	feats := jobNeeds(job).Features
	if len(feats) != 1 || feats[0] != WorkerFeatureAudioPassthrough {
		t.Errorf("features %v, want [audio_passthrough]", feats)
	}
	if f := jobNeeds(TranscodeJob{Decision: "transcode", AudioCodec: "copy"}).Features; len(f) != 0 {
		t.Errorf("an AC-3 copy needs no worker feature, got %v", f)
	}
}
