package transcode

// The receiver encode: audio the server can't copy for a client that
// bitstreams Dolby to an AV receiver is encoded to E-AC-3 / AC-3 instead of
// AAC (TranscodeJob.AudioCodec "eac3" / "ac3").

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildHLS_DolbyEncodeArgs(t *testing.T) {
	for _, codec := range []string{"eac3", "ac3"} {
		t.Run(codec, func(t *testing.T) {
			args := BuildHLS(BuildArgs{
				InputPath: "/media/1917.mkv", Encoder: EncoderCopy, IsHEVC: true, TSSegments: true,
				AudioCodec: codec, AudioChannels: 6, AudioBitrateKbps: DolbyEncodeKbps,
				HasLibfdkAAC: true, AudioStreamIndex: -1,
				SessionDir: "/tmp/sessions/x", SegmentPrefix: "seg",
			})
			argStr := strings.Join(args, " ")
			for _, want := range []string{"-c:a " + codec, "-ac 6", "-b:a 640k", "-af aresample=async=1"} {
				if !strings.Contains(argStr, want) {
					t.Errorf("want %q in: %s", want, argStr)
				}
			}
			for _, bad := range []string{"libfdk_aac", "-c:a aac", "-strict"} {
				if strings.Contains(argStr, bad) {
					t.Errorf("%q must not appear for a %s encode: %s", bad, codec, argStr)
				}
			}
			// E-AC-3 / AC-3 ride MPEG-TS: the segment container isn't forced.
			if ext := segmentFilenameExt(t, args); ext != ".ts" {
				t.Errorf("segments %s, want .ts", ext)
			}
		})
	}
}

// A 7.1 count from an older caller, or none at all, is held to 5.1 (the
// native encoders' ceiling); the bitrate defaults to DolbyEncodeKbps.
func TestBuildHLS_DolbyEncodeChannelCeiling(t *testing.T) {
	for _, ch := range []int{0, 8} {
		args := BuildHLS(BuildArgs{
			InputPath: "/media/x.mkv", Encoder: EncoderCopy, AudioCodec: "eac3", AudioChannels: ch,
			SessionDir: "/tmp/sessions/x", SegmentPrefix: "seg",
		})
		if !hasArgPair(args, "-ac", "6") || !hasArgPair(args, "-b:a", "640k") {
			t.Errorf("channels %d: %s", ch, strings.Join(args, " "))
		}
	}
}

// A mid-file start: like the AAC encode, the E-AC-3 encoder's priming
// samples (256) must survive the seek (make_zero) and the resampler is off.
func TestBuildHLS_DolbyEncodeMidStream(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath: "/media/x.mkv", StartOffset: 600, Encoder: EncoderCopy, AudioCodec: "eac3",
		AudioChannels: 6, SessionDir: "/tmp/sessions/x", SegmentPrefix: "seg",
	})
	if !hasArgPair(args, "-avoid_negative_ts", "make_zero") {
		t.Errorf("want -avoid_negative_ts make_zero: %s", strings.Join(args, " "))
	}
	if slices.ContainsFunc(args, func(s string) bool { return strings.Contains(s, "aresample") }) {
		t.Errorf("aresample must not run mid-stream: %s", strings.Join(args, " "))
	}
}

func TestJobNeeds_DolbyEncode(t *testing.T) {
	for _, c := range []struct {
		codec string
		want  bool
	}{{"eac3", true}, {"ac3", true}, {"aac", false}, {"copy", false}} {
		got := slices.Contains(jobNeeds(TranscodeJob{AudioCodec: c.codec}).Features, WorkerFeatureAudioDolbyEncode)
		if got != c.want {
			t.Errorf("%s: needs audio_dolby_encode = %v, want %v", c.codec, got, c.want)
		}
	}
	if !slices.Contains(workerFeatures, WorkerFeatureAudioDolbyEncode) {
		t.Error("this build's workers must advertise audio_dolby_encode")
	}
}

func TestMasterCodecs_DolbyEncode(t *testing.T) {
	if got := MasterCodecs(LadderHEVC, 1080, 0, "eac3"); got != "hvc1.1.6.L123.B0,ec-3" {
		t.Errorf("HEVC + E-AC-3: %q", got)
	}
	if got := MasterCodecs(LadderAV1, 1080, 0, "ac3"); !strings.HasSuffix(got, ",ac-3") {
		t.Errorf("AV1 + AC-3: %q", got)
	}
}

func TestDolbyEncodeChannels(t *testing.T) {
	for src, want := range map[int]int{8: 6, 7: 6, 6: 6, 3: 3, 0: 1} {
		if got := DolbyEncodeChannels(src); got != want {
			t.Errorf("DolbyEncodeChannels(%d) = %d, want %d", src, got, want)
		}
	}
}

// Real ffmpeg: a 7.1 track remuxed into MPEG-TS from mid-file comes out as
// 5.1 E-AC-3 / AC-3 at 640 kb/s with audio in segment 0.
func TestFFmpeg_DolbyEncodeRemuxTS(t *testing.T) {
	if testing.Short() {
		t.Skip("real-ffmpeg test skipped in -short mode")
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	src := filepath.Join(t.TempDir(), "src71.mkv")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "10", "-map", "0:v", "-map", "1:a",
		"-c:v", "libx264", "-g", "48", "-pix_fmt", "yuv420p",
		"-c:a", "flac", "-ac", "8", src).CombinedOutput(); err != nil {
		t.Skipf("can't make a 7.1 source: %v\n%s", err, out)
	}
	for _, codec := range []string{"eac3", "ac3"} {
		t.Run(codec, func(t *testing.T) {
			dir := runHLSAny(t, BuildArgs{
				InputPath: src, Encoder: EncoderCopy, TSSegments: true, StartOffset: 2,
				AudioCodec: codec, AudioChannels: DolbyEncodeChannels(8), AudioBitrateKbps: DolbyEncodeKbps,
				AudioStreamIndex: -1,
			})
			seg := probeableFirstSegment(t, dir)
			if !strings.HasSuffix(seg, ".ts") {
				t.Fatalf("first segment %s, want MPEG-TS", seg)
			}
			audio := probeEntries(t, seg, "a:0", "codec_name,channels,bit_rate")
			for _, want := range []string{"codec_name=" + codec, "channels=6", "bit_rate=640000"} {
				if !strings.Contains(audio, want) {
					t.Errorf("segment audio %q, want %s", audio, want)
				}
			}
		})
	}
}
