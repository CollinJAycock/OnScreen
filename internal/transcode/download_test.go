package transcode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/ffsafe"
)

func TestFitWithin(t *testing.T) {
	cases := []struct {
		name                   string
		srcW, srcH, maxW, maxH int
		wantW, wantH           int
	}{
		{"4K into 1080p", 3840, 2160, 1920, 1080, 1920, 1080},
		{"scope into 720p", 1920, 800, 1280, 720, 1280, 532},
		{"never upscales", 640, 480, 1920, 1080, 640, 480},
		{"odd source rounds down", 1279, 719, 1920, 1080, 1278, 718},
		{"portrait bounded by height", 1080, 1920, 1280, 720, 404, 720},
		{"4:3 into 480p", 1440, 1080, 854, 480, 640, 480},
		{"unknown source", 0, 0, 1280, 720, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, h := FitWithin(c.srcW, c.srcH, c.maxW, c.maxH)
			if w != c.wantW || h != c.wantH {
				t.Errorf("FitWithin(%d,%d,%d,%d) = %dx%d, want %dx%d",
					c.srcW, c.srcH, c.maxW, c.maxH, w, h, c.wantW, c.wantH)
			}
		})
	}
}

func argsString(a []string) string { return strings.Join(a, " ") }

func TestBuildProgressiveMP4_Software(t *testing.T) {
	args := BuildProgressiveMP4(ProgressiveArgs{
		InputPath:   "/media/movie.mkv",
		OutputPath:  "/cache/x.part",
		Encoder:     EncoderSoftware,
		BitrateKbps: 4000,
		MaxWidth:    1280, MaxHeight: 720,
		SourceWidth: 1920, SourceHeight: 1080,
		TextSubtitleStreams: []int{3},
	})
	s := argsString(args)
	for _, want := range []string{
		"-protocol_whitelist file,pipe,crypto,data,subfile -i /media/movie.mkv",
		"-map 0:v:0 -map 0:a:0? -map 0:3",
		"-vf scale=1280:720,format=yuv420p",
		"-c:v libx264 -b:v 4000k -maxrate 6000k -bufsize 12000k",
		"-c:a aac -ac 2 -b:a 160k",
		"-c:s mov_text",
		"-movflags +faststart",
		"-progress pipe:1",
		"-f mp4 /cache/x.part",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("argv missing %q:\n%s", want, s)
		}
	}
	// The allowlist must precede the -i it guards.
	if strings.Index(s, "-protocol_whitelist") > strings.Index(s, "-i ") {
		t.Errorf("protocol allowlist after -i: %s", s)
	}
}

func TestBuildProgressiveMP4_HEVCEncoderFallsBackToH264(t *testing.T) {
	s := argsString(BuildProgressiveMP4(ProgressiveArgs{
		InputPath: "/m.mkv", OutputPath: "/o", Encoder: EncoderHEVCNVENC, BitrateKbps: 1000,
	}))
	if !strings.Contains(s, "-c:v libx264") {
		t.Errorf("an HEVC encoder must not produce the H.264 download: %s", s)
	}
}

func TestBuildProgressiveMP4_VAAPIUploadsAndTonemaps(t *testing.T) {
	s := argsString(BuildProgressiveMP4(ProgressiveArgs{
		InputPath: "/m.mkv", OutputPath: "/o", Encoder: EncoderVAAPI, BitrateKbps: 8000,
		MaxWidth: 1920, MaxHeight: 1080, SourceWidth: 3840, SourceHeight: 2160,
		NeedsToneMap: true, HasZscale: true, NoAudio: true,
	}))
	if !strings.Contains(s, "-vaapi_device /dev/dri/renderD128") {
		t.Errorf("missing vaapi device: %s", s)
	}
	if !strings.Contains(s, "scale=1920:1080,zscale=t=linear") || !strings.Contains(s, "format=nv12,hwupload") {
		t.Errorf("vaapi filter chain wrong: %s", s)
	}
	if strings.Contains(s, "0:a:0") || strings.Contains(s, "-c:a") {
		t.Errorf("NoAudio still maps audio: %s", s)
	}
}

func TestBuildProgressiveMP4_UnknownSourceSizeUsesExpression(t *testing.T) {
	s := argsString(BuildProgressiveMP4(ProgressiveArgs{
		InputPath: "/m.mkv", OutputPath: "/o", BitrateKbps: 1000, MaxWidth: 854, MaxHeight: 480,
	}))
	if !strings.Contains(s, "scale=w='min(iw,854)':h='min(ih,480)':force_original_aspect_ratio=decrease:force_divisible_by=2") {
		t.Errorf("expected expression scale: %s", s)
	}
}

func TestBuildProgressiveMP4_ConcatInput(t *testing.T) {
	parts := []ffsafe.ConcatPart{{Input: "/m/cd1.avi", DurationSec: 10}, {Input: "/m/cd2.avi", DurationSec: 10}}
	s := argsString(BuildProgressiveMP4(ProgressiveArgs{
		InputPath: "/tmp/list.ffconcat", ConcatParts: parts, OutputPath: "/o", BitrateKbps: 1000,
	}))
	if !strings.Contains(s, "-f concat -safe 0 -protocol_whitelist file,pipe,crypto,data,subfile -i /tmp/list.ffconcat") {
		t.Errorf("stacked input not opened through ffsafe concat args: %s", s)
	}
}

func TestIsTextSubtitleCodec(t *testing.T) {
	for _, c := range []string{"subrip", "ASS", "webvtt", "mov_text"} {
		if !IsTextSubtitleCodec(c) {
			t.Errorf("%s should be text", c)
		}
	}
	for _, c := range []string{"hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", ""} {
		if IsTextSubtitleCodec(c) {
			t.Errorf("%s should not be text", c)
		}
	}
}

func TestWorkerTryReserveSlot(t *testing.T) {
	w := &Worker{}
	w.maxSessions.Store(2)
	r1, ok := w.TryReserveSlot(100)
	if !ok {
		t.Fatal("first reserve should succeed")
	}
	r2, ok := w.TryReserveSlot(50)
	if !ok {
		t.Fatal("second reserve should succeed")
	}
	if _, ok := w.TryReserveSlot(10); ok {
		t.Fatal("third reserve should fail at the cap")
	}
	if got := w.activeCostCenti.Load(); got != 150 {
		t.Errorf("cost = %d, want 150", got)
	}
	r1()
	r1() // idempotent
	if got := w.activeSessions.Load(); got != 1 {
		t.Errorf("active = %d after one release, want 1", got)
	}
	r2()
	if w.activeSessions.Load() != 0 || w.activeCostCenti.Load() != 0 {
		t.Errorf("counters not back to zero: %d / %d", w.activeSessions.Load(), w.activeCostCenti.Load())
	}
}

// TestBuildProgressiveMP4_RealEncode runs the argv through ffmpeg on a tiny
// lavfi clip and checks the result is an H.264/AAC MP4 at the rung's size.
func TestBuildProgressiveMP4_RealEncode(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ffmpeg")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("make source: %v %s", err, out)
	}
	out := filepath.Join(dir, "out.part")
	args := BuildProgressiveMP4(ProgressiveArgs{
		InputPath: src, OutputPath: out, Encoder: EncoderSoftware, BitrateKbps: 1000,
		MaxWidth: 426, MaxHeight: 240, SourceWidth: 640, SourceHeight: 360,
	})
	if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("encode: %v %s", err, b)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Fatalf("no output: %v", err)
	}
	probe, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name,width,height",
		"-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	got := string(probe)
	if !strings.Contains(got, "h264,426,240") || !strings.Contains(got, "aac") {
		t.Errorf("unexpected streams: %q", got)
	}
}
