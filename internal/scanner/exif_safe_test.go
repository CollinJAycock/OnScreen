package scanner

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"testing"
	"time"

	"github.com/onscreen/onscreen/internal/exifsafe"
)

// exifJPEG builds a JPEG whose APP1 carries IFD0 {Make, Orientation, Exif
// pointer} and an Exif IFD {DateTimeOriginal}, little-endian.
func exifJPEG(t *testing.T, cameraMake string, orientation uint16, taken string) []byte {
	t.Helper()
	le := binary.LittleEndian
	makeV := append([]byte(cameraMake), 0)
	takenV := append([]byte(taken), 0)
	const ifd0 = 8
	ifd0Len := 2 + 3*12 + 4
	makeOff := ifd0 + ifd0Len
	exifOff := makeOff + len(makeV)
	exifLen := 2 + 12 + 4
	takenOff := exifOff + exifLen

	tiff := make([]byte, takenOff+len(takenV))
	copy(tiff, "II*\x00")
	le.PutUint32(tiff[4:], ifd0)
	entry := func(at int, tag, typ uint16, count, val uint32) {
		le.PutUint16(tiff[at:], tag)
		le.PutUint16(tiff[at+2:], typ)
		le.PutUint32(tiff[at+4:], count)
		le.PutUint32(tiff[at+8:], val)
	}
	le.PutUint16(tiff[ifd0:], 3)
	entry(ifd0+2, 0x010F, 2, uint32(len(makeV)), uint32(makeOff))
	entry(ifd0+14, 0x0112, 3, 1, uint32(orientation))
	entry(ifd0+26, 0x8769, 4, 1, uint32(exifOff))
	copy(tiff[makeOff:], makeV)
	le.PutUint16(tiff[exifOff:], 1)
	entry(exifOff+2, 0x9003, 2, uint32(len(takenV)), uint32(takenOff))
	copy(tiff[takenOff:], takenV)

	var img bytes.Buffer
	if err := jpeg.Encode(&img, image.NewGray(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	out := []byte{0xFF, 0xD8, 0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(out[4:], uint16(len(payload)+2))
	out = append(out, payload...)
	return append(out, img.Bytes()[2:]...)
}

func extractWithin(t *testing.T, data []byte) (*PhotoEXIF, error) {
	t.Helper()
	type res struct {
		ex  *PhotoEXIF
		err error
	}
	ch := make(chan res, 1)
	go func() {
		ex, err := ExtractEXIFReader(bytes.NewReader(data))
		ch <- res{ex, err}
	}()
	select {
	case r := <-ch:
		return r.ex, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("ExtractEXIFReader did not return within 5s")
		return nil, nil
	}
}

// The 34-byte JPEG from the 2026-11 scan: two IFDs pointing at each other.
// goexif alone never returns on it; every rescan of the library re-read it.
func TestExtractEXIFReader_MutualIFDLoopReturnsPromptly(t *testing.T) {
	data := []byte{
		0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x1C, 'E', 'x', 'i', 'f', 0, 0,
		'I', 'I', 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x0E, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x08, 0x00, 0x00, 0x00,
		0xFF, 0xD9,
	}
	ex, err := extractWithin(t, data)
	if ex != nil {
		t.Errorf("ex = %+v, want no EXIF", ex)
	}
	if !errors.Is(err, exifsafe.ErrRefused) {
		t.Errorf("err = %v, want exifsafe.ErrRefused so the scan logs it", err)
	}
}

func TestExtractEXIFReader_ParsesEXIF(t *testing.T) {
	ex, err := extractWithin(t, exifJPEG(t, "Canon", 6, "2024:05:06 07:08:09"))
	if err != nil {
		t.Fatalf("ExtractEXIFReader: %v", err)
	}
	if ex == nil {
		t.Fatal("no EXIF parsed")
	}
	if ex.CameraMake == nil || *ex.CameraMake != "Canon" {
		t.Errorf("CameraMake = %v, want Canon", ex.CameraMake)
	}
	if ex.Orientation == nil || *ex.Orientation != 6 {
		t.Errorf("Orientation = %v, want 6", ex.Orientation)
	}
	if ex.TakenAt == nil || ex.TakenAt.Format("2006-01-02 15:04:05") != "2024-05-06 07:08:09" {
		t.Errorf("TakenAt = %v, want 2024-05-06 07:08:09", ex.TakenAt)
	}
}
