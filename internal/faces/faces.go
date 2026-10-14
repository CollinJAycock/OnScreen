// Package faces finds faces in photos.
//
// Detection uses pigo (github.com/esimov/pigo, MIT), a pure-Go port of the
// pixel-intensity-comparison cascade from Markuš et al.'s pico, with the
// frontal-face cascade pigo ships (cascade/facefinder, from pico, MIT; see
// cascade/LICENSE). No CGO, no model download, no external process.
//
// It only finds faces. Telling whether two faces are the same person needs
// face embeddings, and there is no pure-Go embedding model of usable quality
// under a licence compatible with the AGPL, so matching is manual: people
// are named by hand, helped by suggestions (see the photo_people queries).
package faces

import (
	_ "embed"
	"errors"
	"image"
	"image/color"
	"math"
	"sort"

	pigo "github.com/esimov/pigo/core"
)

//go:embed cascade/facefinder
var facefinderCascade []byte

// Box is one face, normalised to 0..1 of the image it was found in.
type Box struct {
	X, Y, W, H float64
	// Score is the cascade's clustered confidence (higher is surer).
	Score float64
}

// Options tune a Detector. Zero values take the defaults.
type Options struct {
	// MinScore drops detections the cascade is less sure of. Default 5.
	MinScore float64
	// MinFaceFraction is the smallest face looked for, as a fraction of the
	// image's shorter side. Default 0.03 (about 30 px on a 1024 px image).
	MinFaceFraction float64
}

// Detector runs the face cascade. Safe for concurrent use: the unpacked
// cascade is read-only and every call works on its own buffers.
type Detector struct {
	classifier *pigo.Pigo
	opts       Options
}

// maxPixels bounds the image Detect will scan; callers downscale first
// (photoimage.DecodeUpright), this is the backstop.
const maxPixels = 4096 * 4096

// New unpacks the embedded cascade.
func New(opts Options) (*Detector, error) {
	if opts.MinScore <= 0 {
		opts.MinScore = 5
	}
	if opts.MinFaceFraction <= 0 {
		opts.MinFaceFraction = 0.03
	}
	c, err := pigo.NewPigo().Unpack(facefinderCascade)
	if err != nil {
		return nil, err
	}
	return &Detector{classifier: c, opts: opts}, nil
}

// ErrTooLarge is returned for an image over the pixel bound.
var ErrTooLarge = errors.New("faces: image too large")

// Detect returns the faces in img, largest first.
func (d *Detector) Detect(img image.Image) ([]Box, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, nil
	}
	if w*h > maxPixels {
		return nil, ErrTooLarge
	}
	short := w
	if h < short {
		short = h
	}
	minSize := int(float64(short) * d.opts.MinFaceFraction)
	if minSize < 20 {
		minSize = 20
	}
	if minSize > short {
		return nil, nil
	}
	params := pigo.CascadeParams{
		MinSize:     minSize,
		MaxSize:     short,
		ShiftFactor: 0.1,
		ScaleFactor: 1.1,
		ImageParams: pigo.ImageParams{
			Pixels: grayscale(img),
			Rows:   h,
			Cols:   w,
			Dim:    w,
		},
	}
	dets := d.classifier.RunCascade(params, 0)
	dets = d.classifier.ClusterDetections(dets, 0.2)

	var out []Box
	for _, det := range dets {
		if float64(det.Q) < d.opts.MinScore {
			continue
		}
		s := float64(det.Scale)
		x := (float64(det.Col) - s/2) / float64(w)
		y := (float64(det.Row) - s/2) / float64(h)
		bw, bh := s/float64(w), s/float64(h)
		x, bw = clampSpan(x, bw)
		y, bh = clampSpan(y, bh)
		if bw <= 0 || bh <= 0 {
			continue
		}
		out = append(out, Box{X: x, Y: y, W: bw, H: bh, Score: float64(det.Q)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].W*out[i].H > out[j].W*out[j].H })
	return out, nil
}

// clampSpan keeps [start, start+size] inside [0, 1].
func clampSpan(start, size float64) (float64, float64) {
	if start < 0 {
		size += start
		start = 0
	}
	if start+size > 1 {
		size = 1 - start
	}
	if math.IsNaN(start) || math.IsNaN(size) {
		return 0, 0
	}
	return start, size
}

// grayscale returns img's luma as one byte per pixel, row-major.
func grayscale(img image.Image) []uint8 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]uint8, w*h)
	switch src := img.(type) {
	case *image.Gray:
		for y := 0; y < h; y++ {
			o := src.PixOffset(b.Min.X, b.Min.Y+y)
			copy(out[y*w:(y+1)*w], src.Pix[o:o+w])
		}
	case *image.YCbCr:
		for y := 0; y < h; y++ {
			o := src.YOffset(b.Min.X, b.Min.Y+y)
			copy(out[y*w:(y+1)*w], src.Y[o:o+w])
		}
	case *image.RGBA:
		for y := 0; y < h; y++ {
			row := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < w; x++ {
				r, g, bl := row[x*4], row[x*4+1], row[x*4+2]
				out[y*w+x] = uint8((19595*uint32(r) + 38470*uint32(g) + 7471*uint32(bl) + 1<<15) >> 16)
			}
		}
	default:
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out[y*w+x] = color.GrayModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.Gray).Y
			}
		}
	}
	return out
}

// IoU is the intersection over union of two boxes (0 = apart, 1 = equal).
func IoU(a, b Box) float64 {
	ix := math.Max(0, math.Min(a.X+a.W, b.X+b.W)-math.Max(a.X, b.X))
	iy := math.Max(0, math.Min(a.Y+a.H, b.Y+b.H)-math.Max(a.Y, b.Y))
	inter := ix * iy
	union := a.W*a.H + b.W*b.H - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}
