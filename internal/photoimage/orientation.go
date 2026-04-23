package photoimage

import "image"

// applyOrientation rotates/flips img per the EXIF Orientation tag (1-8 in
// the spec). Orientation 1 (or any unknown value) is a no-op. Mirror
// transforms (2, 4, 5, 7) are rare in the wild but cheap to support, so we
// handle the full table rather than punting on them.
func applyOrientation(img image.Image, orient int) image.Image {
	switch orient {
	case 2:
		return flipHoriz(img)
	case 3:
		return rotate180(img)
	case 4:
		return flipVert(img)
	case 5:
		return rotate90CW(flipHoriz(img))
	case 6:
		return rotate90CW(img)
	case 7:
		return rotate270CW(flipHoriz(img))
	case 8:
		return rotate270CW(img)
	default:
		return img
	}
}

// rotate90CW returns img rotated 90 degrees clockwise. Output has swapped
// dimensions: an H×W result for a W×H input.
func rotate90CW(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// rotate270CW returns img rotated 270 degrees clockwise (== 90 CCW).
func rotate270CW(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func rotate180(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func flipHoriz(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func flipVert(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
