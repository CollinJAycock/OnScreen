package transcode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAtempoFilter(t *testing.T) {
	for rate, want := range map[float64]string{
		1.5:  "atempo=1.5",
		3:    "atempo=3",
		0.75: "atempo=0.75",
		0.25: "atempo=0.5,atempo=0.5",
	} {
		if got := AtempoFilter(rate); got != want {
			t.Errorf("AtempoFilter(%v) = %q, want %q", rate, got, want)
		}
	}
}

// An audio-only session asked to run at 1.5x gets atempo (after the start
// resampler when there is one); a video session never does, whatever it asks.
func TestBuildHLS_AudioRate(t *testing.T) {
	audio := BuildArgs{InputPath: "/media/book.mp3", AudioOnly: true, AudioCodec: "aac", SessionDir: "/tmp/s", SegmentPrefix: "seg", AudioRate: 1.5}
	if got := strings.Join(BuildHLS(audio), " "); !strings.Contains(got, "-af aresample=async=1,atempo=1.5") {
		t.Errorf("audio-only 1.5x lacks atempo: %s", got)
	}
	audio.StartOffset = 120
	if got := strings.Join(BuildHLS(audio), " "); !strings.Contains(got, "-af atempo=1.5") || strings.Contains(got, "aresample") {
		t.Errorf("resumed audio-only 1.5x should carry atempo alone: %s", got)
	}
	audio.AudioRate = 1
	if got := strings.Join(BuildHLS(audio), " "); strings.Contains(got, "atempo") {
		t.Errorf("1x must not filter: %s", got)
	}
	video := BuildArgs{InputPath: "/media/movie.mkv", Encoder: EncoderSoftware, AudioCodec: "aac", SessionDir: "/tmp/s", SegmentPrefix: "seg", AudioRate: 1.5}
	if got := strings.Join(BuildHLS(video), " "); strings.Contains(got, "atempo") {
		t.Errorf("video session must never get atempo: %s", got)
	}
}

// The real thing: the server's args on an MP3 give audio 1/1.5 as long.
func TestBuildHLS_AudioRate_ffmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "book.mp3")
	if b, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=300:duration=12", "-c:a", "libmp3lame", src).CombinedOutput(); err != nil {
		t.Skipf("fixture: %v %s", err, b)
	}
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	args := BuildHLS(BuildArgs{InputPath: src, AudioOnly: true, AudioCodec: "aac", AudioChannels: 2, SessionDir: out, SegmentPrefix: "seg", AudioRate: 1.5})
	// Drop pacing so the test doesn't run in real time.
	var trimmed []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-readrate" || args[i] == "-readrate_initial_burst" {
			i++
			continue
		}
		trimmed = append(trimmed, args[i])
	}
	cmd := exec.Command("ffmpeg", trimmed...)
	cmd.Dir = out
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, b)
	}
	pl, err := os.ReadFile(filepath.Join(out, "index.m3u8"))
	if err != nil {
		t.Fatalf("playlist: %v", err)
	}
	total := 0.0
	for _, line := range strings.Split(string(pl), "\n") {
		if strings.HasPrefix(line, "#EXTINF:") {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ","), 64)
			total += v
		}
	}
	if total < 7.5 || total > 8.6 {
		t.Errorf("12 s at 1.5x came out %.2f s, want ~8", total)
	}
}
