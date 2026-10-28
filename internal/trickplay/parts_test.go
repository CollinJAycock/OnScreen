package trickplay

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/onscreen/onscreen/internal/ffsafe"
)

// TestGenerateSprites_StackedParts cuts thumbnails from two parts as one
// movie: the sprite count must cover both parts' frames, and no concat list
// may be left in the served output directory.
func TestGenerateSprites_StackedParts(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ffmpeg")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	dir := t.TempDir()
	var parts []ffsafe.ConcatPart
	for i, dur := range []string{"12", "8"} {
		p := filepath.Join(dir, "Movie (2001) cd"+string(rune('1'+i))+".mkv")
		out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc=size=320x180:rate=10:duration="+dur,
			"-c:v", "libx264", "-pix_fmt", "yuv420p", p).CombinedOutput()
		if err != nil {
			t.Fatalf("make part: %v: %s", err, out)
		}
		d := 12.0
		if i == 1 {
			d = 8
		}
		parts = append(parts, ffsafe.ConcatPart{Input: p, DurationSec: d})
	}
	outDir := filepath.Join(dir, "out")
	// 1 thumb per 2 s, 1x1 grid → one sprite per thumb: 20 s → 10 sprites.
	spec := Spec{IntervalSec: 2, ThumbWidth: 64, ThumbHeight: 36, GridCols: 1, GridRows: 1}
	names, err := generateSprites(context.Background(), parts[0].Input, parts, outDir, spec, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 9 {
		t.Fatalf("sprites = %d, want ~10 covering both parts (part 1 alone gives 6)", len(names))
	}
	entries, _ := os.ReadDir(outDir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".jpg" {
			t.Errorf("unexpected file in output dir: %s", e.Name())
		}
	}
}
