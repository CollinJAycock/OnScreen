package trickplay

import (
	"strings"
	"testing"
)

// The mjpeg encoder in the shipped image's ffmpeg (8.x) refuses limited-range
// input, so every sprite run failed until the graph ended in a full-range
// conversion. Pin it: dropping it breaks generation for nearly all real video.
func TestSpriteFilter_EndsInFullRangeJPEGFormat(t *testing.T) {
	vf := spriteFilter(Spec{IntervalSec: 10, ThumbWidth: 320, ThumbHeight: 180, GridCols: 10, GridRows: 10})
	if !strings.HasSuffix(vf, ",format=yuvj420p") {
		t.Fatalf("sprite filter must convert to full-range yuvj420p last, got %q", vf)
	}
	for _, want := range []string{"fps=1/10", "scale=320:180", "pad=320:180", "tile=10x10"} {
		if !strings.Contains(vf, want) {
			t.Errorf("sprite filter missing %q: %q", want, vf)
		}
	}
}
