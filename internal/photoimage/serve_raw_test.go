package photoimage

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// rafWithPreview builds a minimal Fujifilm RAF: the header's JPEG pointer
// names a w×h preview whose EXIF carries orientation.
func rafWithPreview(t *testing.T, w, h int, orientation uint16) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var j bytes.Buffer
	if err := jpeg.Encode(&j, img, nil); err != nil {
		t.Fatal(err)
	}
	// TIFF (big endian) with one IFD0 entry: Orientation.
	tiff := []byte("MM\x00*\x00\x00\x00\x08")
	tiff = binary.BigEndian.AppendUint16(tiff, 1)
	tiff = binary.BigEndian.AppendUint16(tiff, 0x0112)
	tiff = binary.BigEndian.AppendUint16(tiff, 3)
	tiff = binary.BigEndian.AppendUint32(tiff, 1)
	tiff = binary.BigEndian.AppendUint16(tiff, orientation)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0)
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	preview := []byte{0xFF, 0xD8, 0xFF, 0xE1}
	preview = binary.BigEndian.AppendUint16(preview, uint16(len(app1)+2))
	preview = append(preview, app1...)
	preview = append(preview, j.Bytes()[2:]...)

	hdr := make([]byte, 128)
	copy(hdr, "FUJIFILMCCD-RAW 0201")
	binary.BigEndian.PutUint32(hdr[84:], uint32(len(hdr)))
	binary.BigEndian.PutUint32(hdr[88:], uint32(len(preview)))
	return append(hdr, preview...)
}

func TestServe_RAWUsesEmbeddedPreviewUpright(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "DSCF0001.RAF")
	if err := os.WriteFile(src, rafWithPreview(t, 400, 200, 6), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(dir, "cache"))
	var out bytes.Buffer
	if err := s.Serve(context.Background(), &out, src, Options{}); err != nil {
		t.Fatalf("serve: %v", err)
	}
	cfg, err := jpeg.DecodeConfig(&out)
	if err != nil {
		t.Fatal(err)
	}
	// Orientation 6 rotates 90°, so the 400x200 preview comes out 200x400.
	if cfg.Width != 200 || cfg.Height != 400 {
		t.Fatalf("got %dx%d, want 200x400", cfg.Width, cfg.Height)
	}

	img, err := s.DecodeUpright(context.Background(), src, 100)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 50 || b.Dy() != 100 {
		t.Fatalf("DecodeUpright = %dx%d, want 50x100", b.Dx(), b.Dy())
	}
}

func TestServe_RAWWithoutPreviewFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "broken.nef")
	if err := os.WriteFile(src, []byte("II*\x00\x08\x00\x00\x00\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(dir, "cache"))
	t.Setenv("PATH", "") // no external decoders
	if err := s.Serve(context.Background(), &bytes.Buffer{}, src, Options{}); err == nil {
		t.Fatal("expected an error for a RAW with no preview")
	}
}
