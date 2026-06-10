package photoimage

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decodeBombPNG builds a valid PNG signature + IHDR (correct CRC) declaring
// w×h with no pixel data, so image.DecodeConfig reports huge dimensions
// without allocating — the pixel-flood "bomb" decodeSource must reject.
func decodeBombPNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8
	ihdr[9] = 2
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

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDecodeSource_RejectsPixelBomb(t *testing.T) {
	p := writeTemp(t, "bomb.png", decodeBombPNG(60000, 60000))
	if _, _, err := decodeSource(context.Background(), p); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("want too-large rejection, got %v", err)
	}
}

func TestDecodeSource_AllowsNormalImage(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	p := writeTemp(t, "ok.png", b.Bytes())
	img, _, err := decodeSource(context.Background(), p)
	if err != nil || img == nil {
		t.Fatalf("decode small png: img=%v err=%v", img, err)
	}
}
