package photoimage

import (
	"image"
	"image/color"
	"testing"
)

// asymImage builds a 2x3 image where every pixel is uniquely identifiable
// by its (x, y) coordinates encoded in the RGB channels. This lets the
// orientation tests assert exact pixel placement after each transform
// rather than relying on a single dominant color.
func asymImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 100), G: uint8(y * 80), B: 200, A: 255})
		}
	}
	return img
}

func sameColor(t *testing.T, got, want color.Color, label string) {
	t.Helper()
	gr, gg, gb, ga := got.RGBA()
	wr, wg, wb, wa := want.RGBA()
	if gr != wr || gg != wg || gb != wb || ga != wa {
		t.Errorf("%s: got rgba(%d,%d,%d,%d), want rgba(%d,%d,%d,%d)",
			label, gr>>8, gg>>8, gb>>8, ga>>8, wr>>8, wg>>8, wb>>8, wa>>8)
	}
}

func TestApplyOrientation_NoOp(t *testing.T) {
	src := asymImage()
	for _, orient := range []int{0, 1, 99} {
		got := applyOrientation(src, orient)
		if got != src {
			t.Errorf("orient=%d should return src unchanged", orient)
		}
	}
}

func TestRotate90CW_DimensionsAndPixels(t *testing.T) {
	src := asymImage() // 2 wide, 3 tall
	got := rotate90CW(src)
	b := got.Bounds()
	if b.Dx() != 3 || b.Dy() != 2 {
		t.Fatalf("dims: got %dx%d, want 3x2", b.Dx(), b.Dy())
	}
	// Pixel at (0,0) in src → (h-1, 0) = (2, 0) in rotated.
	sameColor(t, got.At(2, 0), src.At(0, 0), "src(0,0) -> rot(2,0)")
	// Pixel at (1, 2) in src → (h-1-y, x) = (0, 1) in rotated.
	sameColor(t, got.At(0, 1), src.At(1, 2), "src(1,2) -> rot(0,1)")
}

func TestRotate270CW_DimensionsAndPixels(t *testing.T) {
	src := asymImage() // 2x3
	got := rotate270CW(src)
	b := got.Bounds()
	if b.Dx() != 3 || b.Dy() != 2 {
		t.Fatalf("dims: got %dx%d, want 3x2", b.Dx(), b.Dy())
	}
	// (0, 0) in src → (y, w-1-x) = (0, 1) in rotated.
	sameColor(t, got.At(0, 1), src.At(0, 0), "src(0,0) -> rot(0,1)")
	// (1, 2) in src → (2, 0) in rotated.
	sameColor(t, got.At(2, 0), src.At(1, 2), "src(1,2) -> rot(2,0)")
}

func TestRotate180_PixelsInverted(t *testing.T) {
	src := asymImage()
	got := rotate180(src)
	b := got.Bounds()
	if b.Dx() != 2 || b.Dy() != 3 {
		t.Fatalf("dims: got %dx%d, want 2x3", b.Dx(), b.Dy())
	}
	// (0,0) ↔ (1, 2)
	sameColor(t, got.At(1, 2), src.At(0, 0), "src(0,0) -> rot(1,2)")
	sameColor(t, got.At(0, 0), src.At(1, 2), "src(1,2) -> rot(0,0)")
}

func TestFlipHoriz_PixelsMirrored(t *testing.T) {
	src := asymImage() // 2x3
	got := flipHoriz(src)
	b := got.Bounds()
	if b.Dx() != 2 || b.Dy() != 3 {
		t.Fatalf("dims: got %dx%d, want 2x3", b.Dx(), b.Dy())
	}
	// X mirrors, Y same: (0, 1) ↔ (1, 1).
	sameColor(t, got.At(1, 1), src.At(0, 1), "horiz mirror src(0,1) -> got(1,1)")
	sameColor(t, got.At(0, 2), src.At(1, 2), "horiz mirror src(1,2) -> got(0,2)")
}

func TestFlipVert_PixelsMirrored(t *testing.T) {
	src := asymImage()
	got := flipVert(src)
	// Y mirrors, X same: (0, 0) ↔ (0, 2).
	sameColor(t, got.At(0, 2), src.At(0, 0), "vert mirror src(0,0) -> got(0,2)")
	sameColor(t, got.At(1, 0), src.At(1, 2), "vert mirror src(1,2) -> got(1,0)")
}

// pixelsEqual compares every pixel of two images. Returns the first
// mismatch as a (x, y) pair so failures point at the offending coord
// rather than just "images differ."
func pixelsEqual(t *testing.T, got, want image.Image, label string) {
	t.Helper()
	gb, wb := got.Bounds(), want.Bounds()
	if gb != wb {
		t.Fatalf("%s: bounds %v != %v", label, gb, wb)
	}
	for y := 0; y < gb.Dy(); y++ {
		for x := 0; x < gb.Dx(); x++ {
			gr, gg, gb_, ga := got.At(x, y).RGBA()
			wr, wg, wb_, wa := want.At(x, y).RGBA()
			if gr != wr || gg != wg || gb_ != wb_ || ga != wa {
				t.Fatalf("%s: pixel (%d,%d) differs", label, x, y)
			}
		}
	}
}

// Each EXIF orientation value (2-8) must dispatch to the documented
// transform. Without this, a typo in the switch would silently mis-rotate
// half the photos shot on iPhones in portrait.
func TestApplyOrientation_DispatchTable(t *testing.T) {
	src := asymImage()
	cases := []struct {
		orient int
		want   image.Image
		label  string
	}{
		{2, flipHoriz(src), "orient=2 → flipHoriz"},
		{3, rotate180(src), "orient=3 → rotate180"},
		{4, flipVert(src), "orient=4 → flipVert"},
		{5, rotate90CW(flipHoriz(src)), "orient=5 → rotate90CW(flipHoriz)"},
		{7, rotate270CW(flipHoriz(src)), "orient=7 → rotate270CW(flipHoriz)"},
		{8, rotate270CW(src), "orient=8 → rotate270CW"},
	}
	for _, c := range cases {
		got := applyOrientation(src, c.orient)
		pixelsEqual(t, got, c.want, c.label)
	}
}

// Orientation 6 is the most common in-the-wild "phone photo taken in
// portrait" — verify the public table dispatches to rotate90CW.
func TestApplyOrientation_6_Rotates90CW(t *testing.T) {
	src := asymImage()
	got := applyOrientation(src, 6)
	want := rotate90CW(src)
	gb := got.Bounds()
	wb := want.Bounds()
	if gb != wb {
		t.Fatalf("dims: got %v, want %v", gb, wb)
	}
	for y := 0; y < gb.Dy(); y++ {
		for x := 0; x < gb.Dx(); x++ {
			sameColor(t, got.At(x, y), want.At(x, y), "orient=6 should equal rotate90CW")
		}
	}
}
