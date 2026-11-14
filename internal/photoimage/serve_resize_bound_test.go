package photoimage

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"path/filepath"
	"testing"
	"time"
)

// A one-axis request used to derive the other axis from the aspect ratio and
// upscale without bound: a 1×20000 PNG at w=1920 asked for a 1920×38,400,000
// bitmap. Every target must stay within the source.
func TestResize_ExtremeAspectNeverExceedsSource(t *testing.T) {
	for _, tc := range []struct {
		name       string
		srcW, srcH int
		w, h       int
		fit        Fit
		wantW      int
		wantH      int
	}{
		{"tall, width only", 1, 20000, 1920, 0, FitContain, 1, 20000},
		{"wide, height only", 20000, 1, 0, 1920, FitContain, 20000, 1},
		{"tall, both", 1, 20000, 1920, 1920, FitContain, 1, 1920},
		{"tall, cover", 1, 20000, 1920, 1920, FitCover, 1, 1},
		{"wide, cover", 20000, 1, 400, 300, FitCover, 1, 1},
		{"small source, contain box", 100, 50, 4096, 4096, FitContain, 100, 50},
		{"small source, cover box", 100, 50, 400, 400, FitCover, 50, 50},
		{"downscale width only", 800, 400, 200, 0, FitContain, 200, 100},
		{"cover downscale", 1000, 500, 200, 200, FitCover, 200, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := image.NewRGBA(image.Rect(0, 0, tc.srcW, tc.srcH))
			got, err := resize(src, tc.w, tc.h, tc.fit)
			if err != nil {
				t.Fatalf("resize: %v", err)
			}
			b := got.Bounds()
			if b.Dx() != tc.wantW || b.Dy() != tc.wantH {
				t.Errorf("got %dx%d, want %dx%d", b.Dx(), b.Dy(), tc.wantW, tc.wantH)
			}
		})
	}
}

// End to end through Serve with the PNG on disk, as /items/{id}/image would.
func TestServe_ExtremeAspectPNGWidthOnly(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "strip.png")
	makePNG(t, src, 1, 20000, color.RGBA{1, 2, 3, 255})

	s := New(filepath.Join(dir, "cache"))
	var buf bytes.Buffer
	if err := s.Serve(context.Background(), &buf, src, Options{Width: 1920}); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	cfg, err := jpeg.DecodeConfig(&buf)
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if cfg.Width != 1 || cfg.Height != 20000 {
		t.Errorf("got %dx%d, want the 1x20000 source unscaled", cfg.Width, cfg.Height)
	}
}

func TestNewTarget_RefusesOverCap(t *testing.T) {
	if _, err := newTarget(20000, 20000); err == nil {
		t.Fatal("a 400 MP target must be refused before allocating")
	}
}

// readOrientation used goexif directly, which never returns on two IFDs that
// point at each other.
func TestReadOrientation_IFDLoopReturnsPromptly(t *testing.T) {
	p := writeTemp(t, "loop.jpg", []byte{
		0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x1C, 'E', 'x', 'i', 'f', 0, 0,
		'I', 'I', 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x0E, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x08, 0x00, 0x00, 0x00,
		0xFF, 0xD9,
	})
	done := make(chan int, 1)
	go func() { done <- readOrientation(p) }()
	select {
	case got := <-done:
		if got != 1 {
			t.Errorf("readOrientation = %d, want 1", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readOrientation did not return on a looping IFD chain")
	}
}
