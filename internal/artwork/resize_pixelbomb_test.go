package artwork

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

// resizeBombPNG builds a valid PNG signature + IHDR chunk (correct CRC)
// declaring w×h with no pixel data — image.DecodeConfig reads the dimensions
// from it without allocating, exactly the pixel-flood "bomb" the resize guard
// must reject before imaging.Decode would materialize the bitmap.
func resizeBombPNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // colour type: truecolour
	chunk := append([]byte("IHDR"), ihdr...)
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], 13)
	b.Write(l[:])
	b.Write(chunk)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(chunk))
	b.Write(crc[:])
	return b.Bytes()
}

func resizeTinyPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDecodeResizeEncodeJPEG_RejectsPixelBomb(t *testing.T) {
	_, err := decodeResizeEncodeJPEG(bytes.NewReader(resizeBombPNG(60000, 60000)), 300, 0, 80)
	if err == nil || !strings.Contains(err.Error(), "dimensions too large") {
		t.Fatalf("want dimensions-too-large rejection, got %v", err)
	}
}

// The pixel guard only checks the source. A one-axis request used to derive
// the other axis from the aspect ratio and upscale: a 1×20000 PNG at w=1920
// asked for a 1920×38,400,000 bitmap and killed the process. Every target
// must now stay within the source (and within the requested box).
func TestDecodeResizeEncodeJPEG_NeverExceedsSource(t *testing.T) {
	encode := func(w, h int) []byte {
		var b bytes.Buffer
		if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	tall, wide, poster := encode(1, 20000), encode(20000, 1), encode(200, 300)
	for _, tc := range []struct {
		name         string
		src          []byte
		w, h         int
		wantW, wantH int
	}{
		{"tall strip, width only (artwork ?w=)", tall, 1920, 0, 1, 20000},
		{"wide strip, height only", wide, 0, 1920, 20000, 1},
		{"tall strip, digest poster width", tall, 192, 0, 1, 20000},
		{"tall strip, cover box", tall, coverMaxW, coverMaxH, 1, 1500},
		{"small poster, larger width", poster, 1920, 0, 200, 300},
		{"poster downscale width only", poster, 100, 0, 100, 150},
		{"poster downscale height only", poster, 0, 150, 100, 150},
		{"poster inside box", poster, 1000, 1500, 200, 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := decodeResizeEncodeJPEG(bytes.NewReader(tc.src), tc.w, tc.h, 80)
			if err != nil {
				t.Fatalf("resize: %v", err)
			}
			cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("decode output: %v", err)
			}
			if cfg.Width != tc.wantW || cfg.Height != tc.wantH {
				t.Errorf("got %dx%d, want %dx%d", cfg.Width, cfg.Height, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestDecodeResizeEncodeJPEG_AllowsNormalImage(t *testing.T) {
	out, err := decodeResizeEncodeJPEG(bytes.NewReader(resizeTinyPNG(t)), 2, 2, 80)
	if err != nil {
		t.Fatalf("resize small png: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("empty JPEG output")
	}
}
