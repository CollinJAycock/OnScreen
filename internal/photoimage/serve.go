// Package photoimage serves on-demand resized, orientation-corrected
// derivatives of source photos.
//
// Sources are decoded in-process for JPEG/PNG/GIF and via an ffmpeg
// subprocess for HEIC/HEIF (browsers can't decode HEIC and the standard
// library has no HEIC decoder). EXIF orientation is read from the source
// and applied so the output is always upright — a phone-taken photo with
// orientation=6 lands right-side up regardless of what the requesting
// client supports.
//
// Derivatives are cached on disk under cacheDir, keyed by
// (sourcePath, width, height, fit, quality). The cache entry is treated
// as stale when the source mtime is newer than the cache mtime, mirroring
// the artwork pipeline.
package photoimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/gif"  // register GIF decoder
	_ "image/png"  // register PNG decoder
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/image/draw"
)

// Server serves photo derivatives.
type Server struct {
	cacheDir string
}

// New constructs a Server. cacheDir is created lazily on first write.
func New(cacheDir string) *Server {
	return &Server{cacheDir: cacheDir}
}

// Fit controls how Width/Height constrain the output.
type Fit string

const (
	// FitContain shrinks the image so it fits inside the W×H box, preserving
	// aspect ratio. Output dimensions may be smaller than requested.
	FitContain Fit = "contain"
	// FitCover scales the image to fill the W×H box, then center-crops the
	// excess on one axis. Output is exactly W×H. Used for grid thumbnails.
	FitCover Fit = "cover"
)

// Options configures a single Serve call.
type Options struct {
	Width   int  // 0 = unconstrained
	Height  int  // 0 = unconstrained
	Fit     Fit  // default FitContain
	Quality int  // JPEG quality 1-100, default 85
}

// Serve writes a JPEG derivative of sourcePath to w, going through the
// on-disk cache when possible. Returns an error wrapping the source path
// for any decode/encode failure so the caller can log it.
func (s *Server) Serve(ctx context.Context, w io.Writer, sourcePath string, opts Options) error {
	opts = opts.withDefaults()

	cachePath := s.cachePathFor(sourcePath, opts)
	if isCacheValid(sourcePath, cachePath) {
		cf, err := os.Open(cachePath)
		if err == nil {
			defer cf.Close()
			_, err := io.Copy(w, cf)
			return err
		}
	}

	src, autoRotated, err := decodeSource(ctx, sourcePath)
	if err != nil {
		return fmt.Errorf("decode %s: %w", sourcePath, err)
	}
	if !autoRotated {
		// In-process decoders ignore EXIF orientation; apply it ourselves so
		// rotated phone photos render upright. The HEIC path delegates this
		// to ffmpeg's autorotate, which is why the flag exists.
		if orient := readOrientation(sourcePath); orient > 1 {
			src = applyOrientation(src, orient)
		}
	}

	dst := resize(src, opts.Width, opts.Height, opts.Fit)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: opts.Quality}); err != nil {
		return fmt.Errorf("encode jpeg: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
		// Best-effort cache write — a failure here just costs us a cache miss
		// next time, not a wrong response.
		if err := atomicWrite(cachePath, buf.Bytes()); err != nil {
			// silently degrade — the response still succeeds
		}
	}
	_, err = w.Write(buf.Bytes())
	return err
}

func (o Options) withDefaults() Options {
	if o.Quality <= 0 || o.Quality > 100 {
		o.Quality = 85
	}
	if o.Fit == "" {
		o.Fit = FitContain
	}
	return o
}

// decodeSource opens sourcePath and returns the decoded image plus whether
// EXIF orientation was already applied during decode (true for HEIC, which
// is decoded by ffmpeg with -autorotate).
func decodeSource(ctx context.Context, sourcePath string) (image.Image, bool, error) {
	if isHEIC(sourcePath) {
		img, err := decodeHEIC(ctx, sourcePath)
		return img, true, err
	}
	f, err := os.Open(sourcePath)
	if err != nil {
		return nil, false, fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, false, fmt.Errorf("decode: %w", err)
	}
	return img, false, nil
}

// isHEIC returns true when sourcePath has a HEIC/HEIF extension. Detection
// by extension is sufficient — the scanner only persists files with these
// extensions as photo items, and the source filesystem is trusted.
func isHEIC(sourcePath string) bool {
	ext := strings.ToLower(filepath.Ext(sourcePath))
	return ext == ".heic" || ext == ".heif"
}

// decodeHEIC shells out to ffmpeg to convert a HEIC/HEIF into a JPEG byte
// stream we can hand to image.Decode. ffmpeg auto-rotates from EXIF in this
// path, so the caller does not apply our own orientation transform.
//
// Sized cap on the JPEG stdout pipe prevents a corrupt source from
// exhausting memory. 50 MB is comfortably above any sensible single-photo
// JPEG (24 MP at quality 95 is ~10 MB).
func decodeHEIC(ctx context.Context, sourcePath string) (image.Image, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-i", sourcePath,
		"-frames:v", "1",
		"-f", "mjpeg",
		"-q:v", "2",
		"pipe:1",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg heic decode: %w (stderr=%s)", err, strings.TrimSpace(stderr.String()))
	}
	if out.Len() > 50*1024*1024 {
		return nil, fmt.Errorf("ffmpeg heic decode: output exceeds 50 MB")
	}
	img, err := jpeg.Decode(&out)
	if err != nil {
		return nil, fmt.Errorf("decode ffmpeg jpeg: %w", err)
	}
	return img, nil
}

// readOrientation reads the EXIF Orientation tag from sourcePath. Returns 1
// (no transform) when the file has no EXIF block or any other failure —
// orientation is a best-effort hint, not load-bearing.
func readOrientation(sourcePath string) int {
	f, err := os.Open(sourcePath)
	if err != nil {
		return 1
	}
	defer f.Close()
	x, err := exif.Decode(f)
	if err != nil || x == nil {
		return 1
	}
	tag, err := x.Get(exif.Orientation)
	if err != nil {
		return 1
	}
	v, err := tag.Int(0)
	if err != nil {
		return 1
	}
	return v
}

// resize returns a scaled image. fit==contain shrinks to fit inside w×h
// preserving aspect ratio; fit==cover scales to fill and center-crops.
// Either dimension being 0 means "unconstrained on that axis."
func resize(src image.Image, maxW, maxH int, fit Fit) image.Image {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	if maxW == 0 && maxH == 0 {
		return src
	}

	if fit == FitCover && maxW > 0 && maxH > 0 {
		return resizeCover(src, srcW, srcH, maxW, maxH)
	}
	return resizeContain(src, srcW, srcH, maxW, maxH)
}

func resizeContain(src image.Image, srcW, srcH, maxW, maxH int) image.Image {
	var dstW, dstH int
	switch {
	case maxW == 0:
		dstH = maxH
		dstW = srcW * maxH / srcH
	case maxH == 0:
		dstW = maxW
		dstH = srcH * maxW / srcW
	default:
		// Pick the dimension that requires the larger downscale so the result
		// fits inside the box on both axes.
		if srcW*maxH > srcH*maxW {
			dstW = maxW
			dstH = srcH * maxW / srcW
		} else {
			dstH = maxH
			dstW = srcW * maxH / srcH
		}
	}
	if dstW <= 0 {
		dstW = 1
	}
	if dstH <= 0 {
		dstH = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

// resizeCover scales src so it covers maxW×maxH then center-crops to
// exactly that size. Used for square-ish thumbnails in grid views where a
// uniform tile size matters more than preserving the full frame.
func resizeCover(src image.Image, srcW, srcH, maxW, maxH int) image.Image {
	// Pick the dimension that requires the smaller downscale so the result
	// fully covers the box, then crop the overflow on the other axis.
	scaleX := float64(maxW) / float64(srcW)
	scaleY := float64(maxH) / float64(srcH)
	scale := scaleX
	if scaleY > scaleX {
		scale = scaleY
	}
	scaledW := int(float64(srcW) * scale)
	scaledH := int(float64(srcH) * scale)
	if scaledW < maxW {
		scaledW = maxW
	}
	if scaledH < maxH {
		scaledH = maxH
	}
	scaled := image.NewRGBA(image.Rect(0, 0, scaledW, scaledH))
	draw.BiLinear.Scale(scaled, scaled.Bounds(), src, src.Bounds(), draw.Over, nil)

	offX := (scaledW - maxW) / 2
	offY := (scaledH - maxH) / 2
	dst := image.NewRGBA(image.Rect(0, 0, maxW, maxH))
	draw.Draw(dst, dst.Bounds(), scaled, image.Point{X: offX, Y: offY}, draw.Src)
	return dst
}

// cachePathFor builds the cache file path for a given (source, opts) tuple.
// SHA-256 over the canonicalized inputs makes the key collision-resistant
// and filesystem-safe. The first two hex bytes shard the cache directory
// so a million entries don't all live in one directory.
func (s *Server) cachePathFor(sourcePath string, opts Options) string {
	key := strings.Join([]string{
		sourcePath,
		strconv.Itoa(opts.Width),
		strconv.Itoa(opts.Height),
		string(opts.Fit),
		strconv.Itoa(opts.Quality),
	}, "|")
	sum := sha256.Sum256([]byte(key))
	hexSum := hex.EncodeToString(sum[:16])
	return filepath.Join(s.cacheDir, hexSum[:2], hexSum+".jpg")
}

// isCacheValid returns true when cachePath exists and was written after
// sourcePath was last modified. A re-saved photo invalidates every
// derivative for that source on the next request.
func isCacheValid(sourcePath, cachePath string) bool {
	srcInfo, err := os.Stat(sourcePath)
	if err != nil {
		return false
	}
	cacheInfo, err := os.Stat(cachePath)
	if err != nil {
		return false
	}
	return cacheInfo.ModTime().After(srcInfo.ModTime()) ||
		cacheInfo.ModTime().Equal(srcInfo.ModTime())
}

// atomicWrite writes data to path via a same-directory temp file + rename,
// so a partial write never leaves a corrupted derivative on disk.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".photoimg-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	// Touch mtime so isCacheValid uses our write time, not the temp-file
	// creation time. Not strictly necessary on most filesystems but cheap.
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return nil
}
