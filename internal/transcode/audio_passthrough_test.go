package transcode

import (
	"strings"
	"testing"
)

// A TrueHD (or DTS) passthrough remux: video and audio both copied, packaged
// as fMP4 even for H.264 video (no player demuxes TrueHD out of MPEG-TS), and
// -strict -2 because ffmpeg's mp4 muxer refuses TrueHD without it. Checked
// against ffmpeg 8.1: without -strict the header write fails with
// "truehd in MP4 support is experimental"; with it the init segment carries
// an mlpa track.
func TestBuildHLS_AudioPassthrough_TrueHDRemux(t *testing.T) {
	for _, tc := range []struct {
		name    string
		srcHEVC bool
		tagV    string
	}{
		{"h264", false, ""},
		{"hevc", true, "hvc1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := BuildHLS(BuildArgs{
				InputPath:        "/media/movie.m2ts",
				Encoder:          EncoderCopy,
				IsHEVC:           tc.srcHEVC,
				AudioCodec:       "copy",
				AudioStreamIndex: -1,
				AudioPassthrough: true,
				StartOffset:      30,
				SessionDir:       "/tmp/sessions/x",
				SegmentPrefix:    "seg",
			})
			argStr := strings.Join(args, " ")
			for _, want := range []string{
				"-c:v copy", "-c:a copy -strict -2", "-hls_segment_type fmp4",
				"-hls_fmp4_init_filename init.mp4", "-noaccurate_seek",
			} {
				if !strings.Contains(argStr, want) {
					t.Errorf("want %q in: %s", want, argStr)
				}
			}
			if ext := segmentFilenameExt(t, args); ext != ".m4s" {
				t.Errorf("segments %s, want .m4s", ext)
			}
			if got := argAfter(args, "-tag:v"); got != tc.tagV {
				t.Errorf("-tag:v %q, want %q", got, tc.tagV)
			}
			for _, aacOnly := range []string{"-ac ", "-b:a", "-af", "make_zero"} {
				if strings.Contains(argStr, aacOnly) {
					t.Errorf("AAC-only %q on a copy: %s", aacOnly, argStr)
				}
			}
		})
	}
}

// Without AudioPassthrough an H.264 audio-copy remux is MPEG-TS with no
// -strict, exactly as before; a passthrough flag with an AAC encode is inert.
func TestBuildHLS_AudioPassthrough_OnlyForCopy(t *testing.T) {
	plain := strings.Join(BuildHLS(BuildArgs{
		InputPath: "/media/movie.mkv", Encoder: EncoderCopy, AudioCodec: "copy", AudioStreamIndex: -1,
		SessionDir: "/tmp/sessions/x", SegmentPrefix: "seg",
	}), " ")
	if strings.Contains(plain, "-strict") || !strings.Contains(plain, "-hls_segment_type mpegts") {
		t.Errorf("plain audio copy changed: %s", plain)
	}
	aac := strings.Join(BuildHLS(BuildArgs{
		InputPath: "/media/movie.mkv", Encoder: EncoderCopy, AudioCodec: "aac", AudioStreamIndex: -1,
		AudioPassthrough: true, SessionDir: "/tmp/sessions/x", SegmentPrefix: "seg",
	}), " ")
	if strings.Contains(aac, "-strict") || !strings.Contains(aac, "-hls_segment_type mpegts") {
		t.Errorf("passthrough flag on an AAC encode must be inert: %s", aac)
	}
}

// The worker resolves the container through the job, the same as BuildHLS:
// a passthrough copy forces fMP4, a passthrough flag on an AAC job doesn't.
func TestTranscodeJob_ForcesFMP4(t *testing.T) {
	cases := []struct {
		job  TranscodeJob
		want bool
	}{
		{TranscodeJob{}, false},
		{TranscodeJob{ForceFMP4: true}, true},
		{TranscodeJob{AudioPassthrough: true, AudioCodec: "copy"}, true},
		{TranscodeJob{AudioPassthrough: true, AudioCodec: "aac"}, false},
	}
	for _, tc := range cases {
		if got := tc.job.ForcesFMP4(); got != tc.want {
			t.Errorf("%+v: ForcesFMP4 = %v, want %v", tc.job, got, tc.want)
		}
	}
	// And the playlist handler, reading the session, agrees.
	sess := Session{FMP4Segments: true}
	if sess.VideoOutput().SegExt() != ".m4s" {
		t.Error("a passthrough session must be waited on as .m4s")
	}
}
