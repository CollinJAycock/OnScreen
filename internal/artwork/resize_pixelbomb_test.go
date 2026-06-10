package artwork

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
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

func TestDecodeResizeEncodeJPEG_AllowsNormalImage(t *testing.T) {
	out, err := decodeResizeEncodeJPEG(bytes.NewReader(resizeTinyPNG(t)), 2, 2, 80)
	if err != nil {
		t.Fatalf("resize small png: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("empty JPEG output")
	}
}
