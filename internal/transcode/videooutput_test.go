package transcode

import (
	"path/filepath"
	"strings"
	"testing"
)

// segmentFilenameExt digs the extension out of the -hls_segment_filename
// pattern BuildHLS actually emitted. This is the ground truth: whatever
// extension appears here is the name ffmpeg will write on disk.
func segmentFilenameExt(t *testing.T, args []string) string {
	t.Helper()
	for i, a := range args {
		if a == "-hls_segment_filename" && i+1 < len(args) {
			return filepath.Ext(args[i+1])
		}
	}
	t.Fatalf("no -hls_segment_filename in args: %s", strings.Join(args, " "))
	return ""
}

// TestVideoOutput_ServerAndClientAgreeOnContainer is the regression guard for a
// bug family that has now bitten four times: the segment container was derived
// from the ENCODER in one place and from something else in another, so the API
// predicted a filename ffmpeg never wrote.
//
// The invariant is not "HEVC uses fMP4" — it is that BuildHLS (which writes the
// files), the worker (which scans for them by extension), and the API (which
// waits for them by name) all reach the SAME answer. So this asserts agreement
// between the emitted ffmpeg args and the VideoOutput every other consumer
// reads, across the matrix that has historically diverged.
func TestVideoOutput_ServerAndClientAgreeOnContainer(t *testing.T) {
	cases := []struct {
		name       string
		encoder    Encoder
		srcHEVC    bool
		srcAV1     bool
		forceFMP4  bool
		wantExt    string
		wantTagV   string
		wantSegTyp string
	}{
		// ── Remux: the container follows the SOURCE, not the encoder string.
		// "copy" is in no codec family, so every encoder-derived predicate
		// answers H.264 here and gets all three of these wrong.
		{
			name: "HEVC source remux", encoder: EncoderCopy, srcHEVC: true,
			wantExt: ".m4s", wantTagV: "hvc1", wantSegTyp: "fmp4",
		},
		{
			name: "AV1 source remux", encoder: EncoderCopy, srcAV1: true,
			wantExt: ".m4s", wantTagV: "av01", wantSegTyp: "fmp4",
		},
		{
			name: "H.264 source remux", encoder: EncoderCopy,
			wantExt: ".ts", wantTagV: "", wantSegTyp: "mpegts",
		},
		// ForceFMP4 is ignored on a remux — the source codec already decided.
		{
			name: "H.264 remux ignores ForceFMP4", encoder: EncoderCopy, forceFMP4: true,
			wantExt: ".ts", wantTagV: "", wantSegTyp: "mpegts",
		},

		// ── Re-encode: the container follows the ENCODER.
		{
			name: "HEVC encode", encoder: EncoderHEVCNVENC,
			wantExt: ".m4s", wantTagV: "hvc1", wantSegTyp: "fmp4",
		},
		{
			name: "AV1 encode", encoder: EncoderAV1NVENC,
			wantExt: ".m4s", wantTagV: "av01", wantSegTyp: "fmp4",
		},
		{
			name: "H.264 encode", encoder: EncoderSoftware,
			wantExt: ".ts", wantTagV: "", wantSegTyp: "mpegts",
		},
		// An HEVC SOURCE transcoded to H.264 must not inherit the source's
		// container — this is the inverse of the remux case and the reason
		// ResolveVideoOutput branches on videoCopy rather than OR-ing.
		{
			name: "HEVC source encoded to H.264", encoder: EncoderSoftware, srcHEVC: true,
			wantExt: ".ts", wantTagV: "", wantSegTyp: "mpegts",
		},

		// ── ForceFMP4: an ABR rung whose encoder fell back to H.264 must keep
		// the .m4s container the already-served playlist promised — but must
		// NOT be tagged hvc1/av01, since the bitstream really is H.264.
		{
			name: "H.264 fallback under ForceFMP4", encoder: EncoderSoftware, forceFMP4: true,
			wantExt: ".m4s", wantTagV: "", wantSegTyp: "fmp4",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vout := ResolveVideoOutput(tc.encoder, tc.srcHEVC, tc.srcAV1, tc.forceFMP4)

			// 1. The resolver's own answer.
			if got := vout.SegExt(); got != tc.wantExt {
				t.Errorf("VideoOutput.SegExt() = %q, want %q", got, tc.wantExt)
			}
			if got := vout.SegType(); got != tc.wantSegTyp {
				t.Errorf("VideoOutput.SegType() = %q, want %q", got, tc.wantSegTyp)
			}
			if got := vout.TagV(); got != tc.wantTagV {
				t.Errorf("VideoOutput.TagV() = %q, want %q", got, tc.wantTagV)
			}

			// 2. What ffmpeg is actually told to write. If this drifts from
			// (1), the API waits for a file that never appears.
			args := BuildHLS(BuildArgs{
				InputPath:     "/media/movie.mkv",
				Encoder:       tc.encoder,
				IsHEVC:        tc.srcHEVC,
				IsAV1:         tc.srcAV1,
				ForceFMP4:     tc.forceFMP4,
				Width:         1920,
				Height:        1080,
				BitrateKbps:   8000,
				AudioCodec:    "aac",
				SessionDir:    "/tmp/sessions/x",
				SegmentPrefix: "seg",
			})
			argStr := strings.Join(args, " ")

			if got := segmentFilenameExt(t, args); got != tc.wantExt {
				t.Errorf("ffmpeg writes %q but VideoOutput predicts %q\nargs: %s",
					got, tc.wantExt, argStr)
			}
			if !strings.Contains(argStr, "-hls_segment_type "+tc.wantSegTyp) {
				t.Errorf("want -hls_segment_type %s\nargs: %s", tc.wantSegTyp, argStr)
			}
			// The fMP4 init segment must accompany fMP4 output and only that:
			// a client handed an EXT-X-MAP with no init.mp4 stalls forever.
			wantInit := tc.wantSegTyp == "fmp4"
			if got := strings.Contains(argStr, "-hls_fmp4_init_filename"); got != wantInit {
				t.Errorf("hls_fmp4_init_filename present = %v, want %v\nargs: %s",
					got, wantInit, argStr)
			}
			if tc.wantTagV == "" {
				if strings.Contains(argStr, "-tag:v") {
					t.Errorf("unexpected -tag:v for a non-HEVC/AV1 output\nargs: %s", argStr)
				}
			} else if !strings.Contains(argStr, "-tag:v "+tc.wantTagV) {
				t.Errorf("want -tag:v %s\nargs: %s", tc.wantTagV, argStr)
			}

			// 3. The API side. Session carries only the two codec booleans, so
			// this is the round trip that the playlist-readiness probe and the
			// ABR segment proxy actually perform. ForcedFMP4 is deliberately
			// excluded: it is not a codec fact and is never stamped on a
			// session (only ABR rung CHILDREN carry it, and nothing reads a
			// child's flags for a container decision).
			if !tc.forceFMP4 {
				sess := &Session{HEVCOutput: vout.HEVC, AV1Output: vout.AV1}
				if got := sess.VideoOutput().SegExt(); got != tc.wantExt {
					t.Errorf("Session.VideoOutput().SegExt() = %q, want %q — "+
						"the API would wait for the wrong filename", got, tc.wantExt)
				}
			}
		})
	}
}

// TestIsVideoCopy pins the one definition of "remux", since ResolveVideoOutput
// branches on it and a caller passing its own videoCopy was how the encoder and
// the container got to disagree in the first place.
func TestIsVideoCopy(t *testing.T) {
	if !IsVideoCopy(EncoderCopy) {
		t.Error("EncoderCopy must be a video copy")
	}
	for _, enc := range []Encoder{EncoderSoftware, EncoderHEVCNVENC, EncoderAV1NVENC, ""} {
		if IsVideoCopy(enc) {
			t.Errorf("%q must not be a video copy", enc)
		}
	}
}
