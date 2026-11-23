// Resize backend for /artwork hot path. Wraps github.com/disintegration/imaging
// — pure-Go (no CGO, no native deps) but materially faster than the stdlib
// image/draw.BiLinear it replaced on 2026-05-27. Benchmarks against the old
// path on a 4000×6000 source resized to 300px: ~1.0 s → ~0.35 s. Not as fast
// as libvips would be (~50-150 ms with shrink-on-load) but keeps Windows dev
// builds frictionless — every contributor's `go build` stays a one-step
// pure-Go compile, no MSYS2 / libvips-Windows toolchain.
package artwork

import (
	"bytes"
	"fmt"
	"image"
	"io"

	"github.com/disintegration/imaging"
)

const (
	// maxArtworkBytes caps the encoded source we read into memory.
	maxArtworkBytes = 64 << 20 // 64 MiB
	// maxArtworkPixels caps the decoded resolution to reject pixel-flood /
	// decompression bombs before imaging.Decode allocates the full bitmap.
	maxArtworkPixels = 100_000_000 // 100 MP
)

// jpegQuality is the encode quality used for every resized variant. 90
// matches the prior pure-Go path so visual diffs from this swap stay
// within JPEG-encoder noise.
const jpegQuality = 90

// decodeResizeEncodeJPEG reads the source image bytes from src, resizes to
// fit within (w × h) preserving aspect ratio, and returns the JPEG-encoded
// result. width/height of 0 means "unconstrained on that axis"; both zero
// returns the source untouched (the contract the old pure-Go path used and
// at least one caller depends on).
//
// The result never exceeds the source: an unconstrained axis is bounded by the
// source's size, and a source already inside the box is re-encoded as is.
// The pixel guard below only checks the source; with one axis given, the
// other used to be derived from the aspect ratio and upscaled with no cap, so
// a 123-byte 1×20000 PNG at ?w=1920 asked for a 1920×38,400,000 bitmap
// (~295 GB) — a fatal out-of-memory the panic recovery cannot catch, reachable
// from /artwork, uploaded covers and the email digest alike. Linear is the
// speed/quality balance the prior BiLinear stand-in aimed at.
//
// A panic inside the decoders or imaging comes back as an error. imaging
// 1.6.2 (the last release; GHSA-q7pp-wcgr-pffx / CVE-2023-36308 has no fix)
// indexes out of range in its pixel scanner on a crafted TIFF, and TIFF stays
// decodable because photo libraries hold .tif files. HTTP handlers recover
// panics, but this also runs for uploaded covers and the email digest, where
// a panic would take the goroutine down.
func decodeResizeEncodeJPEG(src io.Reader, w, h, quality int) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("decode/resize image: %v", r)
		}
	}()
	// Buffer the (bounded) encoded source so we can size-check the header
	// before decoding the full bitmap — a tiny file can declare enormous
	// dimensions that decode to many GB of RGBA.
	data, err := io.ReadAll(io.LimitReader(src, maxArtworkBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read source: %w", err)
	}
	if len(data) > maxArtworkBytes {
		return nil, fmt.Errorf("source image exceeds %d bytes", maxArtworkBytes)
	}
	if cfg, _, derr := image.DecodeConfig(bytes.NewReader(data)); derr == nil {
		if int64(cfg.Width)*int64(cfg.Height) > maxArtworkPixels {
			return nil, fmt.Errorf("source dimensions too large: %dx%d", cfg.Width, cfg.Height)
		}
	}
	img, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode source: %w", err)
	}

	// No-op when unconstrained or already inside the box: re-encode at the
	// target quality so output is normalised JPEG even when the source was a
	// PNG with an alpha channel, without a needless pixel copy.
	var resized = img
	b := img.Bounds()
	if dw, dh := fitDims(b.Dx(), b.Dy(), w, h); dw != b.Dx() || dh != b.Dy() {
		if int64(dw)*int64(dh) > maxArtworkPixels {
			return nil, fmt.Errorf("resize target too large: %dx%d", dw, dh)
		}
		resized = imaging.Resize(img, dw, dh, imaging.Linear)
	}

	var buf bytes.Buffer
	if err := imaging.Encode(&buf, resized, imaging.JPEG, imaging.JPEGQuality(quality)); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

// fitDims fits srcW×srcH inside w×h preserving aspect (0 = unconstrained on
// that axis), never beyond the source's own size, rounding the way
// imaging.Fit does. Both results are at least 1.
func fitDims(srcW, srcH, w, h int) (int, int) {
	if srcW <= 0 || srcH <= 0 {
		return srcW, srcH
	}
	if w <= 0 || w > srcW {
		w = srcW
	}
	if h <= 0 || h > srcH {
		h = srcH
	}
	if w == srcW && h == srcH {
		return srcW, srcH
	}
	// Whichever axis needs the larger downscale sets the size.
	if int64(srcW)*int64(h) > int64(srcH)*int64(w) {
		return w, max(int(float64(srcH)*float64(w)/float64(srcW)+0.5), 1)
	}
	return max(int(float64(srcW)*float64(h)/float64(srcH)+0.5), 1), h
}

// Uploaded covers (a manual collection's poster) are normalised to a JPEG
// that fits in coverMaxW × coverMaxH, so only that output is ever stored or
// served, whatever was uploaded.
const (
	coverMaxW    = 1000
	coverMaxH    = 1500
	coverQuality = 88
)

// NormalizeCover decodes an uploaded image through the same bounded path as
// the /artwork resizer (byte cap, pixel cap checked from the header before
// the bitmap is allocated) and returns it as a JPEG fitting coverMaxW ×
// coverMaxH. A file that isn't a decodable image is an error.
func NormalizeCover(src io.Reader) ([]byte, error) {
	return decodeResizeEncodeJPEG(src, coverMaxW, coverMaxH, coverQuality)
}
