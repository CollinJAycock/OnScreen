package rawpreview

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/onscreen/onscreen/internal/ffsafe"
)

// externalTimeout caps one external decoder run.
const externalTimeout = 30 * time.Second

// maxExternalOutput caps what an external decoder may write to stdout. A
// half-size 16-bit PPM of a 100 MP sensor is ~150 MB; anything above this
// is refused rather than buffered.
const maxExternalOutput = 160 << 20

// Decode returns a displayable image of the RAW file at path plus the EXIF
// orientation still to apply (1 = already upright). It uses the embedded
// preview when there is one and otherwise falls back, in order, to the
// external tools found on PATH at runtime: exiftool (embedded previews it
// knows more maker notes for), dcraw / dcraw_emu (thumbnail extraction, then
// a half-size decode), and ffmpeg (DNG). Each tool is optional; with none
// installed a RAW without an embedded JPEG simply has no image.
func Decode(ctx context.Context, path string) (image.Image, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("stat: %w", err)
	}
	res, xerr := Extract(f, st.Size())
	f.Close()
	orient := 1
	if res != nil && res.Meta.Orientation > 0 {
		orient = res.Meta.Orientation
	}
	if xerr == nil && res.Preview != nil {
		img, err := jpeg.Decode(bytes.NewReader(res.Preview.JPEG))
		if err == nil {
			return img, orient, nil
		}
	}
	img, rotated, err := decodeExternal(ctx, path)
	if err != nil {
		if xerr != nil {
			return nil, 0, fmt.Errorf("%w; %v", xerr, err)
		}
		return nil, 0, err
	}
	if rotated {
		orient = 1
	}
	return img, orient, nil
}

// externalTool is one optional fallback decoder.
type externalTool struct {
	name string
	args func(path string) []string
	// rotated is true when the tool applies the camera orientation itself.
	rotated bool
}

var externalTools = []externalTool{
	{name: "exiftool", args: func(p string) []string { return []string{"-b", "-PreviewImage", p} }},
	{name: "exiftool", args: func(p string) []string { return []string{"-b", "-JpgFromRaw", p} }},
	{name: "dcraw", args: func(p string) []string { return []string{"-c", "-e", p} }},
	// Half-size decode of the sensor data, written to stdout as PPM. dcraw
	// applies the camera's rotation.
	{name: "dcraw", args: func(p string) []string { return []string{"-c", "-h", "-w", p} }, rotated: true},
	{name: "dcraw_emu", args: func(p string) []string { return []string{"-h", "-w", "-Z", "-", p} }, rotated: true},
	{name: "ffmpeg", args: func(p string) []string {
		return []string{"-hide_banner", "-loglevel", "error",
			"-protocol_whitelist", ffsafe.Whitelist(p),
			"-i", p, "-frames:v", "1", "-f", "mjpeg", "-q:v", "2", "pipe:1"}
	}},
}

// lookPath is exec.LookPath; tests replace it.
var lookPath = exec.LookPath

// decodeExternal runs the fallback tools in order and returns the first
// image one produces.
func decodeExternal(ctx context.Context, path string) (image.Image, bool, error) {
	// Tools take the path as a bare argument; an absolute, clean path can't
	// be mistaken for an option.
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, false, errors.New("rawpreview: external decode needs an absolute path")
	}
	lastErr := ErrNoPreview
	for _, tool := range externalTools {
		bin, err := lookPath(tool.name)
		if err != nil {
			continue
		}
		out, err := runCapped(ctx, bin, tool.args(path))
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", tool.name, err)
			continue
		}
		img, err := decodeBytes(out)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", tool.name, err)
			continue
		}
		return img, tool.rotated, nil
	}
	return nil, false, lastErr
}

// runCapped runs bin with args (no shell) under a timeout and returns its
// stdout, refusing output over maxExternalOutput.
func runCapped(ctx context.Context, bin string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, externalTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out := &cappedBuffer{limit: maxExternalOutput}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if out.buf.Len() == 0 {
		return nil, ErrNoPreview
	}
	return out.buf.Bytes(), nil
}

type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.limit {
		return 0, fmt.Errorf("output exceeds %d bytes", c.limit)
	}
	return c.buf.Write(p)
}

// decodeBytes decodes a JPEG or a binary PPM (P6), applying the pixel cap
// before allocating.
func decodeBytes(b []byte) (image.Image, error) {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xD8 {
		if validateJPEG(b) == nil {
			return nil, errors.New("invalid or oversized JPEG")
		}
		return jpeg.Decode(bytes.NewReader(b))
	}
	if len(b) >= 2 && b[0] == 'P' && b[1] == '6' {
		return decodePPM(b)
	}
	return nil, errors.New("unrecognised image output")
}

// decodePPM decodes a binary PPM (8 or 16 bits per sample).
func decodePPM(b []byte) (image.Image, error) {
	br := bufio.NewReader(bytes.NewReader(b))
	var fields []int
	magic := make([]byte, 2)
	if _, err := io.ReadFull(br, magic); err != nil {
		return nil, err
	}
	for len(fields) < 3 {
		tok, err := ppmToken(br)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(tok)
		if err != nil || n <= 0 {
			return nil, errors.New("bad PPM header")
		}
		fields = append(fields, n)
	}
	w, h, maxv := fields[0], fields[1], fields[2]
	if int64(w)*int64(h) > MaxPreviewPixels || maxv > 65535 {
		return nil, errors.New("PPM too large")
	}
	bps := 1
	if maxv > 255 {
		bps = 2
	}
	need := w * h * 3 * bps
	pix := make([]byte, need)
	if _, err := io.ReadFull(br, pix); err != nil {
		return nil, fmt.Errorf("short PPM: %w", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		var c color.RGBA
		if bps == 1 {
			c = color.RGBA{pix[i*3], pix[i*3+1], pix[i*3+2], 0xFF}
		} else {
			o := i * 6
			c = color.RGBA{pix[o], pix[o+2], pix[o+4], 0xFF} // high bytes
		}
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = c.R, c.G, c.B, c.A
	}
	return img, nil
}

func ppmToken(br *bufio.Reader) (string, error) {
	var tok []byte
	for {
		c, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		switch {
		case c == '#':
			if _, err := br.ReadString('\n'); err != nil {
				return "", err
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if len(tok) > 0 {
				return string(tok), nil
			}
		default:
			tok = append(tok, c)
			if len(tok) > 12 {
				return "", errors.New("bad PPM header")
			}
		}
	}
}
