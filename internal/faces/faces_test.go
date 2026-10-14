package faces

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newDetector(t *testing.T) *Detector {
	t.Helper()
	d, err := New(Options{})
	if err != nil {
		t.Fatalf("unpack cascade: %v", err)
	}
	return d
}

func TestDetect_BlankImageHasNoFaces(t *testing.T) {
	d := newDetector(t)
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	boxes, err := d.Detect(img)
	if err != nil || len(boxes) != 0 {
		t.Fatalf("Detect(blank) = %v, %v", boxes, err)
	}
}

func TestDetect_TinyAndHugeImages(t *testing.T) {
	d := newDetector(t)
	if boxes, err := d.Detect(image.NewGray(image.Rect(0, 0, 8, 8))); err != nil || boxes != nil {
		t.Fatalf("tiny: %v, %v", boxes, err)
	}
	if _, err := d.Detect(image.NewGray(image.Rect(0, 0, 5000, 5000))); err != ErrTooLarge {
		t.Fatalf("huge: err = %v, want ErrTooLarge", err)
	}
}

func TestGrayscale_SubImageOffsets(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(2, 2, color.RGBA{255, 255, 255, 255})
	sub := img.SubImage(image.Rect(2, 2, 4, 4))
	g := grayscale(sub)
	if len(g) != 4 || g[0] != 255 || g[1] != 0 {
		t.Fatalf("grayscale(sub) = %v", g)
	}
}

// pigoSample returns pigo's own test portrait from the module cache, so the
// detector is checked against a real face without vendoring a photo.
func pigoSample(t *testing.T) string {
	t.Helper()
	cache := os.Getenv("GOMODCACHE")
	if cache == "" {
		out, err := exec.Command("go", "env", "GOMODCACHE").Output()
		if err != nil {
			t.Skip("go env GOMODCACHE unavailable")
		}
		cache = strings.TrimSpace(string(out))
	}
	p := filepath.Join(cache, "github.com", "esimov", "pigo@v1.4.6", "testdata", "sample.jpg")
	if _, err := os.Stat(p); err != nil {
		t.Skip("pigo sample not in the module cache")
	}
	return p
}

func TestDetect_FindsThePortraitFace(t *testing.T) {
	f, err := os.Open(pigoSample(t))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	boxes, err := newDetector(t).Detect(img)
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 1 {
		t.Fatalf("found %d faces, want 1: %+v", len(boxes), boxes)
	}
	b := boxes[0]
	// The face sits in the middle of the frame and fills about half of it.
	cx, cy := b.X+b.W/2, b.Y+b.H/2
	if cx < 0.35 || cx > 0.65 || cy < 0.3 || cy > 0.7 || b.W < 0.3 {
		t.Fatalf("face box %+v not where the face is", b)
	}
	if b.X < 0 || b.Y < 0 || b.X+b.W > 1 || b.Y+b.H > 1 {
		t.Fatalf("face box %+v outside 0..1", b)
	}
}
