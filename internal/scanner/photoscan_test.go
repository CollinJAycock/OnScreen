package scanner

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"path/filepath"
	"slices"
	"testing"
)

func TestPhotoDisplayPath_PrefersNonRAW(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"/p/IMG_1.CR2", "/p/IMG_1.JPG"}, "/p/IMG_1.JPG"},
		{[]string{"/p/IMG_1.cr3"}, "/p/IMG_1.cr3"},
		{[]string{"/b/x.jpg", "/a/x.jpg", "/a/x.nef"}, "/a/x.jpg"},
	}
	for _, c := range cases {
		if got := PhotoDisplayPath(c.in); got != c.want {
			t.Errorf("PhotoDisplayPath(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPhotoSiblingPaths(t *testing.T) {
	p := filepath.Join("lib", "2023", "IMG_0001.CR2")
	sibs := photoSiblingPaths(p)
	for _, want := range []string{"IMG_0001.JPG", "IMG_0001.jpg", "IMG_0001.heic", "IMG_0001.dng"} {
		if !slices.Contains(sibs, filepath.Join("lib", "2023", want)) {
			t.Errorf("siblings of %s lack %s", p, want)
		}
	}
	if slices.Contains(sibs, p) {
		t.Errorf("siblings include the file itself")
	}
}

func TestPhotoGroup(t *testing.T) {
	if photoGroup("/a/IMG_1.JPG", true) != photoGroup("/a/IMG_1.cr2", true) {
		t.Error("pairing on: a RAW and its JPEG must share a group")
	}
	if photoGroup("/a/IMG_1.JPG", false) == photoGroup("/a/IMG_1.cr2", false) {
		t.Error("pairing off: a RAW and its JPEG must not share a group")
	}
	if photoGroup("/a/IMG_1.JPG", true) == photoGroup("/b/IMG_1.cr2", true) {
		t.Error("different folders must not pair")
	}
}

func TestExtractRAWEXIF_RAF(t *testing.T) {
	be := binary.BigEndian
	// IFD0: Make + Orientation.
	tiff := []byte("MM\x00*")
	tiff = be.AppendUint32(tiff, 8)
	tiff = be.AppendUint16(tiff, 2)
	tiff = be.AppendUint16(tiff, 0x010F)
	tiff = be.AppendUint16(tiff, 2)
	tiff = be.AppendUint32(tiff, 4)
	tiff = append(tiff, 'F', 'U', 'J', 0)
	tiff = be.AppendUint16(tiff, 0x0112)
	tiff = be.AppendUint16(tiff, 3)
	tiff = be.AppendUint32(tiff, 1)
	tiff = be.AppendUint16(tiff, 6)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0)
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	var j bytes.Buffer
	if err := jpeg.Encode(&j, image.NewGray(image.Rect(0, 0, 40, 30)), nil); err != nil {
		t.Fatal(err)
	}
	prev := []byte{0xFF, 0xD8, 0xFF, 0xE1}
	prev = be.AppendUint16(prev, uint16(len(app1)+2))
	prev = append(append(prev, app1...), j.Bytes()[2:]...)
	hdr := make([]byte, 100)
	copy(hdr, "FUJIFILMCCD-RAW ")
	be.PutUint32(hdr[84:], 100)
	be.PutUint32(hdr[88:], uint32(len(prev)))

	ex, err := ExtractRAWEXIF(bytes.NewReader(append(hdr, prev...)))
	if err != nil || ex == nil {
		t.Fatalf("ExtractRAWEXIF = %v, %v", ex, err)
	}
	if ex.CameraMake == nil || *ex.CameraMake != "FUJ" || ex.Orientation == nil || *ex.Orientation != 6 {
		t.Fatalf("make %v orientation %v", ex.CameraMake, ex.Orientation)
	}
	if ex.Width == nil || *ex.Width != 40 {
		t.Fatalf("width %v", ex.Width)
	}

	if ex, err := ExtractRAWEXIF(bytes.NewReader([]byte("not a raw file at all"))); ex != nil || err != nil {
		t.Fatalf("non-RAW: %v, %v", ex, err)
	}
}

func TestExposureFraction(t *testing.T) {
	for _, c := range []struct {
		n, d uint32
		want string
	}{{1, 250, "1/250"}, {2, 1, "2"}, {10, 2500, "1/250"}, {3, 10, "0.30"}, {0, 5, "0"}} {
		if got := exposureFraction(c.n, c.d); got != c.want {
			t.Errorf("exposureFraction(%d,%d) = %q, want %q", c.n, c.d, got, c.want)
		}
	}
}
