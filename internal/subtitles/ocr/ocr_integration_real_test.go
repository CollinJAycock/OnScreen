//go:build integration

// Integration test for the real OCR pipeline (ffmpeg render → tesseract OCR →
// cues). Synthesizes a fixture with an image-based (dvdsub) subtitle carrying
// known text, runs it through the actual engine, and asserts the text survives
// the round-trip. This exercises the per-stream timeout, the batch path, and
// the page-alignment logic against real binaries — none of which the mocked
// unit tests touch.
//
// Run with: go test -tags integration ./internal/subtitles/ocr/
// Skips when ffmpeg or tesseract isn't on PATH.

package ocr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegration_OCRImageSubtitleRoundTrip(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe", "tesseract"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH; skipping OCR integration test", bin)
		}
	}
	dir := t.TempDir()

	// A clear, OCR-friendly cue (plain word, large render canvas).
	srt := filepath.Join(dir, "cue.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,500 --> 00:00:02,500\nHELLO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mkv := filepath.Join(dir, "img.mkv")
	synth := exec.Command("ffmpeg", "-y", "-v", "error",
		"-f", "lavfi", "-i", "color=c=black:s=640x480:d=3:r=10",
		"-i", srt,
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:s", "dvdsub", "-shortest", mkv,
	)
	if out, err := synth.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg synth dvdsub: %v\n%s", err, out)
	}

	eng := &Engine{}
	cues, err := eng.Run(context.Background(), mkv, 1, "en", filepath.Join(dir, "work"))
	if err != nil {
		t.Fatalf("OCR Run: %v", err)
	}
	if len(cues) == 0 {
		t.Fatal("OCR produced no cues from a non-empty subtitle stream")
	}
	joined := strings.ToUpper(strings.Join(func() []string {
		s := make([]string, len(cues))
		for i, c := range cues {
			s[i] = c.Text
		}
		return s
	}(), " "))
	// Tesseract on a rendered bitmap won't be pixel-perfect; require the core
	// token to survive rather than an exact match.
	if !strings.Contains(joined, "HELLO") {
		t.Fatalf("OCR text round-trip lost the cue; got %q", joined)
	}
}
