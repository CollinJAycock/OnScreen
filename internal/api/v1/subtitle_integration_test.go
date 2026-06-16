//go:build integration

// Integration tests for the real ffmpeg subtitle-extraction pipeline. The unit
// tests mock ffmpeg; almost everything that broke on QA this cycle (the
// image-sub empty-200, the 4K extraction timeout/cache) only manifested with
// the real binary. These synthesize a tiny fixture with ffmpeg and exercise the
// actual extraction, so the class is caught pre-deploy.
//
// Run with: go test -tags integration ./internal/api/v1/
// Skips automatically when ffmpeg isn't on PATH.

package v1

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH; skipping integration test")
	}
}

// synthesizeSubtitleMKV writes a tiny .mkv with one video stream (index 0) and
// one embedded subtitle stream (index 1) of the given codec, carrying a known
// cue, and returns its path. codec "srt" → text (subrip), "dvdsub" → image.
func synthesizeSubtitleMKV(t *testing.T, dir, codec string) string {
	t.Helper()
	srtPath := filepath.Join(dir, "cue.srt")
	const srt = "1\n00:00:00,500 --> 00:00:02,000\nIntegration cue\n"
	if err := os.WriteFile(srtPath, []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "fixture_"+codec+".mkv")
	cmd := exec.Command("ffmpeg", "-y", "-v", "error",
		"-f", "lavfi", "-i", "testsrc=duration=3:size=160x120:rate=10",
		"-i", srtPath,
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:s", codec, "-shortest", out,
	)
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg synth (%s): %v\n%s", codec, err, outBytes)
	}
	return out
}

func TestIntegration_ExtractEmbeddedTextSubtitle(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	mkv := synthesizeSubtitleMKV(t, dir, "srt")
	cachePath := filepath.Join(dir, "cache", "1.vtt")

	// Subtitle is the second stream (video=0, subtitle=1).
	data, err := extractEmbeddedSubtitleToCache(mkv, 1, cachePath)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "WEBVTT") {
		t.Fatalf("expected WEBVTT header, got:\n%s", got)
	}
	if !strings.Contains(got, "Integration cue") {
		t.Fatalf("expected cue text in output, got:\n%s", got)
	}

	// The extraction must have atomically written the cache file (no leftover
	// .tmp), and re-reading it must match what was returned.
	onDisk, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	if string(onDisk) != got {
		t.Fatalf("cache file content != returned bytes")
	}
	if _, err := os.Stat(cachePath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("leftover .tmp file after atomic rename")
	}
}

func TestIntegration_ExtractInvalidStreamFails(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	mkv := synthesizeSubtitleMKV(t, dir, "srt")
	// Stream index 9 doesn't exist → ffmpeg fails → no empty cache file left.
	cachePath := filepath.Join(dir, "cache", "9.vtt")
	if _, err := extractEmbeddedSubtitleToCache(mkv, 9, cachePath); err == nil {
		t.Fatal("expected error extracting a non-existent stream")
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatal("a failed extraction must not leave a cache file")
	}
}
