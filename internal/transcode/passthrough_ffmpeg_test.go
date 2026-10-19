package transcode

// Real-ffmpeg check of the v2.7 re-encode paths: audio copied for a receiver
// while the video is re-encoded, and the encoder held to the client's H.264
// profile / level. Generates its own sources (lavfi) in a temp dir, so it
// needs nothing but ffmpeg/ffprobe on PATH; skipped in -short mode.
//
//	go test ./internal/transcode/ -run TestFFmpeg_Reencode -v

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeSource writes a 10 s 1280x720 H.264 High@5.1 / 5.1-audio MKV with the
// given audio encoder, or skips when this ffmpeg can't make one.
func makeSource(t *testing.T, audioEncoder string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("real-ffmpeg test skipped in -short mode")
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	src := filepath.Join(t.TempDir(), "src_"+audioEncoder+".mkv")
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "10", "-map", "0:v", "-map", "1:a",
		"-c:v", "libx264", "-profile:v", "high", "-level", "5.1", "-pix_fmt", "yuv420p",
		"-c:a", audioEncoder, "-ac", "6", "-strict", "-2", src}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Skipf("this ffmpeg can't encode %s: %v\n%s", audioEncoder, err, out)
	}
	return src
}

// probeEntries returns `key=value` lines ffprobe prints for one stream.
func probeEntries(t *testing.T, path, stream, entries string) string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-of", "default=nw=1",
		"-select_streams", stream, "-show_entries", "stream="+entries, path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	return strings.ReplaceAll(strings.TrimSpace(string(out)), "\r", "")
}

// probeableFirstSegment returns a probe-able path for the session's first segment: the
// .ts itself, or init.mp4 + the .m4s joined.
func probeableFirstSegment(t *testing.T, dir string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "seg00000.ts")); err == nil {
		return filepath.Join(dir, "seg00000.ts")
	}
	seg := filepath.Join(dir, "seg00000.m4s")
	joined := filepath.Join(t.TempDir(), "joined.mp4")
	if err := concatFiles(joined, filepath.Join(dir, "init.mp4"), seg); err != nil {
		t.Fatalf("join init + segment: %v", err)
	}
	return joined
}

func TestFFmpeg_ReencodeAudioPassthrough(t *testing.T) {
	cases := []struct {
		encoder     string // ffmpeg audio encoder for the source
		codec       string // what ffprobe calls it
		passthrough bool   // TrueHD / DTS: fMP4 + -strict -2
		wantExt     string
	}{
		{"ac3", "ac3", false, ".ts"},
		{"eac3", "eac3", false, ".ts"},
		{"truehd", "truehd", true, ".m4s"},
		{"dca", "dts", true, ".m4s"},
	}
	for _, tc := range cases {
		t.Run(tc.codec, func(t *testing.T) {
			src := makeSource(t, tc.encoder)
			dir := runHLSAny(t, BuildArgs{
				InputPath: src, Encoder: EncoderSoftware, Width: 1280, Height: 720, BitrateKbps: 2000,
				AudioCodec: "copy", AudioPassthrough: tc.passthrough, AudioStreamIndex: -1,
				// A mid-file start and an ABR rung's timeline shift, as a rung
				// restart would run it.
				StartOffset: 1.5, OutputTSOffsetSec: 1.5,
				OutputLimits: OutputLimits{H264Profile: "main", H264Level: 41, FrameRate: 24},
			})
			if _, err := os.Stat(filepath.Join(dir, "seg00000"+tc.wantExt)); err != nil {
				t.Fatalf("want seg00000%s: %v", tc.wantExt, err)
			}
			seg := probeableFirstSegment(t, dir)
			audio := probeEntries(t, seg, "a:0", "codec_name,channels")
			if !strings.Contains(audio, "codec_name="+tc.codec) || !strings.Contains(audio, "channels=6") {
				t.Errorf("segment audio %q, want the source's 5.1 %s copied", audio, tc.codec)
			}
			video := probeEntries(t, seg, "v:0", "codec_name,profile,level")
			if !strings.Contains(video, "codec_name=h264") || !strings.Contains(video, "profile=Main") ||
				!strings.Contains(video, "level=41") {
				t.Errorf("segment video %q, want H.264 Main@4.1 re-encoded", video)
			}
			if tc.wantExt == ".m4s" {
				// The sample entry the master playlist's CODECS names
				// (AudioCodecString): mlpa for TrueHD, mp4a (object type
				// 0xA9) for DTS.
				tag := probeEntries(t, seg, "a:0", "codec_tag_string")
				if want := strings.SplitN(AudioCodecString(tc.codec), ".", 2)[0]; !strings.Contains(tag, "codec_tag_string="+want) {
					t.Errorf("audio sample entry %q, want %s (CODECS %s)", tag, want, AudioCodecString(tc.codec))
				}
				// The playlist names the init segment, as hls.js / Media3 need.
				pl, _ := os.ReadFile(filepath.Join(dir, "index.m3u8"))
				if !strings.Contains(string(pl), `#EXT-X-MAP:URI="init.mp4"`) {
					t.Errorf("fMP4 playlist without its init segment:\n%s", pl)
				}
			}
		})
	}
}
