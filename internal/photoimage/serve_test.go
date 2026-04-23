package photoimage

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// makeJPEG writes a w×h JPEG with the given dominant color to path. Used
// to build deterministic source files for resize/cache tests.
func makeJPEG(t *testing.T, path string, w, h int, c color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
}

func makePNG(t *testing.T, path string, w, h int, c color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
}

// ── Resize math ──────────────────────────────────────────────────────────────

func TestServe_ContainPreservesAspectRatio(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jpg")
	makeJPEG(t, src, 1000, 500, color.RGBA{200, 100, 50, 255}) // 2:1

	s := New(filepath.Join(dir, "cache"))
	var buf bytes.Buffer
	if err := s.Serve(context.Background(), &buf, src, Options{Width: 200, Height: 200, Fit: FitContain}); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	img, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	b := img.Bounds()
	// Source 2:1 contained inside 200×200 → 200×100.
	if b.Dx() != 200 || b.Dy() != 100 {
		t.Errorf("got %dx%d, want 200x100", b.Dx(), b.Dy())
	}
}

func TestServe_CoverFillsBox(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jpg")
	makeJPEG(t, src, 1000, 500, color.RGBA{200, 100, 50, 255}) // 2:1

	s := New(filepath.Join(dir, "cache"))
	var buf bytes.Buffer
	if err := s.Serve(context.Background(), &buf, src, Options{Width: 200, Height: 200, Fit: FitCover}); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	img, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 200 || b.Dy() != 200 {
		t.Errorf("cover should fill box exactly: got %dx%d, want 200x200", b.Dx(), b.Dy())
	}
}

func TestServe_WidthOnly(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jpg")
	makeJPEG(t, src, 800, 400, color.RGBA{0, 0, 255, 255})

	s := New(filepath.Join(dir, "cache"))
	var buf bytes.Buffer
	if err := s.Serve(context.Background(), &buf, src, Options{Width: 200}); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	img, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	b := img.Bounds()
	// 200 wide, height scaled to maintain 2:1 → 100.
	if b.Dx() != 200 || b.Dy() != 100 {
		t.Errorf("got %dx%d, want 200x100", b.Dx(), b.Dy())
	}
}

func TestServe_NoConstraintReturnsOriginalDimensions(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.png") // PNG so the in-process decoder runs
	makePNG(t, src, 320, 240, color.RGBA{10, 20, 30, 255})

	s := New(filepath.Join(dir, "cache"))
	var buf bytes.Buffer
	if err := s.Serve(context.Background(), &buf, src, Options{}); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	img, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 320 || b.Dy() != 240 {
		t.Errorf("got %dx%d, want 320x240 (no resize)", b.Dx(), b.Dy())
	}
}

// ── Cache behavior ───────────────────────────────────────────────────────────

func TestServe_HitsCacheOnSecondCall(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jpg")
	makeJPEG(t, src, 400, 300, color.RGBA{10, 20, 30, 255})

	cacheDir := filepath.Join(dir, "cache")
	s := New(cacheDir)

	var first bytes.Buffer
	if err := s.Serve(context.Background(), &first, src, Options{Width: 100, Height: 100, Fit: FitContain}); err != nil {
		t.Fatalf("first Serve: %v", err)
	}
	// Cache file should now exist.
	cachePath := s.cachePathFor(src, Options{Width: 100, Height: 100, Fit: FitContain}.withDefaults())
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache file should exist after first serve: %v", err)
	}

	// Mutate the cached file with a sentinel and confirm the second call
	// returns the sentinel — proving we read from cache rather than
	// re-encoding from source.
	sentinel := []byte("CACHED-SENTINEL")
	if err := os.WriteFile(cachePath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	var second bytes.Buffer
	if err := s.Serve(context.Background(), &second, src, Options{Width: 100, Height: 100, Fit: FitContain}); err != nil {
		t.Fatalf("second Serve: %v", err)
	}
	if !bytes.Equal(second.Bytes(), sentinel) {
		t.Errorf("second call should have served the cached sentinel; got %d bytes", second.Len())
	}
}

func TestServe_DifferentDimensionsUseDifferentCacheKeys(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jpg")
	makeJPEG(t, src, 400, 300, color.RGBA{10, 20, 30, 255})

	s := New(filepath.Join(dir, "cache"))
	a := s.cachePathFor(src, Options{Width: 100, Height: 100}.withDefaults())
	b := s.cachePathFor(src, Options{Width: 200, Height: 200}.withDefaults())
	if a == b {
		t.Errorf("cache keys for different dimensions must differ; both got %q", a)
	}
}

// ── HEIC detection ───────────────────────────────────────────────────────────

func TestIsHEIC(t *testing.T) {
	cases := map[string]bool{
		"/tmp/foo.heic": true,
		"/tmp/foo.HEIC": true,
		"/tmp/foo.heif": true,
		"/tmp/foo.HEIF": true,
		"/tmp/foo.jpg":  false,
		"/tmp/foo.jpeg": false,
		"/tmp/foo.png":  false,
		"/tmp/foo":      false,
	}
	for path, want := range cases {
		if got := isHEIC(path); got != want {
			t.Errorf("isHEIC(%q) = %v, want %v", path, got, want)
		}
	}
}

// ── Defaults ────────────────────────────────────────────────────────────────

func TestOptionsDefaults(t *testing.T) {
	o := Options{}.withDefaults()
	if o.Quality != 85 {
		t.Errorf("default quality: got %d, want 85", o.Quality)
	}
	if o.Fit != FitContain {
		t.Errorf("default fit: got %q, want %q", o.Fit, FitContain)
	}

	// Out-of-range quality clamps to default.
	o = Options{Quality: 150}.withDefaults()
	if o.Quality != 85 {
		t.Errorf("quality 150 should clamp to 85, got %d", o.Quality)
	}
}
