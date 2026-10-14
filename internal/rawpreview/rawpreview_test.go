package rawpreview

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"path/filepath"
	"testing"
)

// ── fixtures ─────────────────────────────────────────────────────────────

func makeJPEG(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 50}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// tiffBuilder lays out a TIFF: payloads first, then IFDs, each IFD's
// out-of-line values right after it.
type tiffBuilder struct {
	bo  binary.AppendByteOrder
	buf []byte
}

type tent struct {
	tag, typ uint16
	count    uint32
	data     []byte
}

func newTB(bo binary.AppendByteOrder, magic string) *tiffBuilder {
	b := &tiffBuilder{bo: bo}
	b.buf = append(b.buf, magic...)
	b.buf = append(b.buf, 0, 0, 0, 0) // IFD0 offset, patched by setFirst
	return b
}

func (b *tiffBuilder) blob(p []byte) uint32 {
	if len(b.buf)%2 == 1 {
		b.buf = append(b.buf, 0)
	}
	off := uint32(len(b.buf))
	b.buf = append(b.buf, p...)
	return off
}

func (b *tiffBuilder) ifd(entries []tent, next uint32) uint32 {
	if len(b.buf)%2 == 1 {
		b.buf = append(b.buf, 0)
	}
	off := uint32(len(b.buf))
	dataStart := off + 2 + uint32(len(entries))*12 + 4
	var body, data []byte
	body = b.bo.AppendUint16(body, uint16(len(entries)))
	for _, e := range entries {
		body = b.bo.AppendUint16(body, e.tag)
		body = b.bo.AppendUint16(body, e.typ)
		body = b.bo.AppendUint32(body, e.count)
		if len(e.data) <= 4 {
			v := make([]byte, 4)
			copy(v, e.data)
			body = append(body, v...)
		} else {
			body = b.bo.AppendUint32(body, dataStart+uint32(len(data)))
			data = append(data, e.data...)
			if len(data)%2 == 1 {
				data = append(data, 0)
			}
		}
	}
	body = b.bo.AppendUint32(body, next)
	b.buf = append(b.buf, body...)
	b.buf = append(b.buf, data...)
	return off
}

func (b *tiffBuilder) setFirst(off uint32) { b.bo.(binary.ByteOrder).PutUint32(b.buf[4:8], off) }

func (b *tiffBuilder) short(tag uint16, v uint16) tent {
	return tent{tag: tag, typ: 3, count: 1, data: b.bo.AppendUint16(nil, v)}
}
func (b *tiffBuilder) long(tag uint16, v uint32) tent {
	return tent{tag: tag, typ: 4, count: 1, data: b.bo.AppendUint32(nil, v)}
}
func (b *tiffBuilder) ascii(tag uint16, s string) tent {
	return tent{tag: tag, typ: 2, count: uint32(len(s) + 1), data: append([]byte(s), 0)}
}
func (b *tiffBuilder) rats(tag uint16, vals ...[2]uint32) tent {
	var d []byte
	for _, v := range vals {
		d = b.bo.AppendUint32(d, v[0])
		d = b.bo.AppendUint32(d, v[1])
	}
	return tent{tag: tag, typ: 5, count: uint32(len(vals)), data: d}
}

// cr2Like builds a TIFF RAW the way CR2 / NEF / DNG lay previews out: a
// full-size JPEG as IFD0's single JPEG-compressed strip, a thumbnail via
// JPEGInterchangeFormat in IFD1, EXIF and GPS IFDs.
func cr2Like(t testing.TB, bo binary.AppendByteOrder) []byte {
	b := newTB(bo, map[bool]string{true: "II*\x00", false: "MM\x00*"}[bo == binary.LittleEndian])
	big := makeJPEG(t, 1600, 1200)
	thumb := makeJPEG(t, 160, 120)
	bigOff := b.blob(big)
	thumbOff := b.blob(thumb)

	exif := b.ifd([]tent{
		b.rats(tagExposureTime, [2]uint32{1, 250}),
		b.rats(tagFNumber, [2]uint32{28, 10}),
		b.short(tagISO, 800),
		b.ascii(tagDateTimeOrig, "2021:07:04 18:30:00"),
		b.short(tagFlash, 1),
		b.rats(tagFocalLength, [2]uint32{50, 1}),
		b.ascii(tagLensModel, "EF50mm f/1.8 STM"),
	}, 0)
	gps := b.ifd([]tent{
		b.ascii(1, "S"),
		b.rats(2, [2]uint32{33, 1}, [2]uint32{52, 1}, [2]uint32{0, 1}),
		b.ascii(3, "E"),
		b.rats(4, [2]uint32{151, 1}, [2]uint32{12, 1}, [2]uint32{36, 1}),
		{tag: 5, typ: 1, count: 1, data: []byte{0}},
		b.rats(6, [2]uint32{58, 1}),
	}, 0)
	ifd1 := b.ifd([]tent{
		b.long(tagJPEGOffset, thumbOff),
		b.long(tagJPEGLength, uint32(len(thumb))),
	}, 0)
	ifd0 := b.ifd([]tent{
		b.short(tagCompression, 6),
		b.ascii(tagMake, "Canon"),
		b.ascii(tagModel, "Canon EOS R6"),
		b.long(tagStripOffsets, bigOff),
		b.short(tagOrientation, 6),
		b.long(tagStripByteCounts, uint32(len(big))),
		b.long(tagExifIFD, exif),
		b.long(tagGPSIFD, gps),
	}, ifd1)
	b.setFirst(ifd0)
	return b.buf
}

func extractBytes(t testing.TB, data []byte) (*Result, error) {
	t.Helper()
	return Extract(bytes.NewReader(data), int64(len(data)))
}

// ── tests ────────────────────────────────────────────────────────────────

func TestIsRAW(t *testing.T) {
	for _, p := range []string{"a.CR2", "b.cr3", "c.NEF", "d.arw", "e.dng", "f.RAF", "g.orf", "h.Rw2", "i.pef"} {
		if !IsRAW(p) {
			t.Errorf("IsRAW(%q) = false", p)
		}
	}
	for _, p := range []string{"a.jpg", "b.tif", "c", "d.heic"} {
		if IsRAW(p) {
			t.Errorf("IsRAW(%q) = true", p)
		}
	}
}

func TestExtract_TIFFPicksFullSizePreviewAndReadsEXIF(t *testing.T) {
	for _, bo := range []binary.AppendByteOrder{binary.LittleEndian, binary.BigEndian} {
		res, err := extractBytes(t, cr2Like(t, bo))
		if err != nil {
			t.Fatalf("%v: %v", bo, err)
		}
		if res.Preview.Width != 1600 || res.Preview.Height != 1200 {
			t.Fatalf("%v: preview %dx%d, want the 1600x1200 strip", bo, res.Preview.Width, res.Preview.Height)
		}
		m := res.Meta
		if m.Make != "Canon" || m.Model != "Canon EOS R6" || m.LensModel != "EF50mm f/1.8 STM" {
			t.Errorf("%v: camera %q %q lens %q", bo, m.Make, m.Model, m.LensModel)
		}
		if m.Orientation != 6 || m.ISO != 800 || m.ExposureNum != 1 || m.ExposureDen != 250 {
			t.Errorf("%v: orientation %d iso %d exposure %d/%d", bo, m.Orientation, m.ISO, m.ExposureNum, m.ExposureDen)
		}
		if m.FNumber == nil || *m.FNumber != 2.8 || m.FocalLength == nil || *m.FocalLength != 50 {
			t.Errorf("%v: fnumber/focal %v %v", bo, m.FNumber, m.FocalLength)
		}
		if m.DateTimeOriginal != "2021:07:04 18:30:00" || m.Flash == nil || *m.Flash != 1 {
			t.Errorf("%v: date %q flash %v", bo, m.DateTimeOriginal, m.Flash)
		}
		if m.GPSLat == nil || math.Abs(*m.GPSLat-(-33.8666667)) > 1e-4 ||
			m.GPSLon == nil || math.Abs(*m.GPSLon-151.21) > 1e-4 || m.GPSAlt == nil || *m.GPSAlt != 58 {
			t.Errorf("%v: gps %v %v %v", bo, deref(m.GPSLat), deref(m.GPSLon), deref(m.GPSAlt))
		}
	}
}

func deref(p *float64) float64 {
	if p == nil {
		return math.NaN()
	}
	return *p
}

func TestExtract_NEFSubIFDPreview(t *testing.T) {
	b := newTB(binary.LittleEndian, "II*\x00")
	big := makeJPEG(t, 1400, 1000)
	off := b.blob(big)
	sub := b.ifd([]tent{
		b.long(tagJPEGOffset, off),
		b.long(tagJPEGLength, uint32(len(big))),
	}, 0)
	ifd0 := b.ifd([]tent{
		b.ascii(tagMake, "NIKON CORPORATION"),
		b.long(tagSubIFDs, sub),
	}, 0)
	b.setFirst(ifd0)
	res, err := extractBytes(t, b.buf)
	if err != nil {
		t.Fatal(err)
	}
	if res.Preview.Width != 1400 || res.Meta.Make != "NIKON CORPORATION" {
		t.Fatalf("got %dx%d make %q", res.Preview.Width, res.Preview.Height, res.Meta.Make)
	}
}

func TestExtract_RW2JpgFromRaw(t *testing.T) {
	b := newTB(binary.LittleEndian, "IIU\x00")
	big := makeJPEG(t, 1920, 1080)
	ifd0 := b.ifd([]tent{
		b.ascii(tagMake, "Panasonic"),
		{tag: tagRW2JpgFromRaw, typ: 7, count: uint32(len(big)), data: big},
	}, 0)
	b.setFirst(ifd0)
	res, err := extractBytes(t, b.buf)
	if err != nil {
		t.Fatal(err)
	}
	if res.Preview.Width != 1920 || res.Meta.Make != "Panasonic" {
		t.Fatalf("got %dx%d make %q", res.Preview.Width, res.Preview.Height, res.Meta.Make)
	}
}

// jpegWithEXIF wraps a JPEG with an APP1 EXIF block carrying make,
// orientation and a date.
func jpegWithEXIF(t testing.TB, w, h int) []byte {
	b := newTB(binary.BigEndian, "MM\x00*")
	exif := b.ifd([]tent{b.ascii(tagDateTimeOrig, "2019:01:02 03:04:05")}, 0)
	ifd0 := b.ifd([]tent{
		b.ascii(tagMake, "FUJIFILM"),
		b.short(tagOrientation, 8),
		b.long(tagExifIFD, exif),
	}, 0)
	b.setFirst(ifd0)
	app1 := append([]byte("Exif\x00\x00"), b.buf...)
	j := makeJPEG(t, w, h)
	out := []byte{0xFF, 0xD8, 0xFF, 0xE1}
	out = binary.BigEndian.AppendUint16(out, uint16(len(app1)+2))
	out = append(out, app1...)
	return append(out, j[2:]...)
}

func TestExtract_RAFHeaderPreview(t *testing.T) {
	j := jpegWithEXIF(t, 1500, 1000)
	hdr := make([]byte, 160)
	copy(hdr, "FUJIFILMCCD-RAW 0201FF383501")
	binary.BigEndian.PutUint32(hdr[84:], 160)
	binary.BigEndian.PutUint32(hdr[88:], uint32(len(j)))
	data := append(hdr, j...)
	res, err := extractBytes(t, data)
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "raf" || res.Preview.Width != 1500 {
		t.Fatalf("format %q preview %dx%d", res.Format, res.Preview.Width, res.Preview.Height)
	}
	if res.Meta.Make != "FUJIFILM" || res.Meta.Orientation != 8 || res.Meta.DateTimeOriginal != "2019:01:02 03:04:05" {
		t.Fatalf("meta %+v", res.Meta)
	}
}

func box(typ string, payload ...[]byte) []byte {
	var p []byte
	for _, x := range payload {
		p = append(p, x...)
	}
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(p)))
	out = append(out, typ...)
	return append(out, p...)
}

func TestExtract_CR3Boxes(t *testing.T) {
	cmt1 := newTB(binary.LittleEndian, "II*\x00")
	cmt1.setFirst(cmt1.ifd([]tent{
		cmt1.ascii(tagMake, "Canon"),
		cmt1.ascii(tagModel, "Canon EOS R5"),
		cmt1.short(tagOrientation, 3),
		cmt1.long(tagExifIFD, 0xFFFFFF), // points outside the box: ignored
	}, 0))
	cmt2 := newTB(binary.LittleEndian, "II*\x00")
	cmt2.setFirst(cmt2.ifd([]tent{
		cmt2.ascii(tagDateTimeOrig, "2023:05:06 07:08:09"),
		cmt2.short(tagISO, 100),
	}, 0))
	thumb := makeJPEG(t, 160, 120)
	prvw := makeJPEG(t, 1620, 1080)
	thmbHdr := make([]byte, 16)
	prvwHdr := make([]byte, 16)

	moov := box("moov", box("uuid", cr3CanonUUID,
		box("CMT1", cmt1.buf),
		box("CMT2", cmt2.buf),
		box("THMB", thmbHdr, thumb),
	))
	pre := box("uuid", cr3PreviewUUID, make([]byte, 8), box("PRVW", prvwHdr, prvw))
	ftyp := box("ftyp", []byte("crx "), make([]byte, 4))
	data := append(append(append(ftyp, moov...), pre...), box("mdat", make([]byte, 64))...)

	res, err := extractBytes(t, data)
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "cr3" || res.Preview.Width != 1620 {
		t.Fatalf("format %q preview %dx%d", res.Format, res.Preview.Width, res.Preview.Height)
	}
	m := res.Meta
	if m.Model != "Canon EOS R5" || m.Orientation != 3 || m.ISO != 100 || m.DateTimeOriginal != "2023:05:06 07:08:09" {
		t.Fatalf("meta %+v", m)
	}
}

func TestExtract_CarvesMakerNotePreview(t *testing.T) {
	// ORF-style: the structure only names a thumbnail; the real preview
	// sits elsewhere in the file and the carve has to find it.
	b := newTB(binary.LittleEndian, "IIRO")
	b.blob(bytes.Repeat([]byte{0xFF, 0xD8, 0xFF, 0x00}, 64)) // decoy SOIs
	big := makeJPEG(t, 1600, 1200)
	b.blob(big)
	thumb := makeJPEG(t, 160, 120)
	toff := b.blob(thumb)
	b.setFirst(b.ifd([]tent{
		b.ascii(tagMake, "OLYMPUS"),
		b.long(tagJPEGOffset, toff),
		b.long(tagJPEGLength, uint32(len(thumb))),
	}, 0))
	res, err := extractBytes(t, b.buf)
	if err != nil {
		t.Fatal(err)
	}
	if res.Preview.Width != 1600 {
		t.Fatalf("preview %dx%d, want the carved 1600x1200", res.Preview.Width, res.Preview.Height)
	}
}

func TestExtract_HostileStructures(t *testing.T) {
	base := cr2Like(t, binary.LittleEndian)
	cases := map[string][]byte{}

	// IFD0 pointing past EOF.
	pastEOF := append([]byte(nil), base[:16]...)
	binary.LittleEndian.PutUint32(pastEOF[4:], 0x7FFFFFF0)
	cases["ifd past eof"] = pastEOF

	// An IFD whose next pointer is itself.
	loop := newTB(binary.LittleEndian, "II*\x00")
	loop.blob(make([]byte, 8))
	off := uint32(len(loop.buf))
	loop.ifd([]tent{loop.ascii(tagMake, "X")}, off)
	loop.setFirst(off)
	cases["ifd loop"] = loop.buf

	// A JPEG pointer whose length runs past EOF, and an absurd entry count.
	bad := newTB(binary.LittleEndian, "II*\x00")
	bad.setFirst(bad.ifd([]tent{
		bad.long(tagJPEGOffset, 8),
		bad.long(tagJPEGLength, 0xFFFFFFF0),
		{tag: tagSubIFDs, typ: 4, count: 0x3FFFFFFF, data: bad.bo.AppendUint32(nil, 8)},
	}, 0))
	cases["length past eof"] = bad.buf

	// Truncated real file.
	cases["truncated"] = base[:len(base)/3]

	for name, data := range cases {
		res, err := extractBytes(t, data)
		if err == nil && res.Preview != nil && name != "truncated" {
			t.Errorf("%s: unexpectedly found a preview", name)
		}
		if err != nil && !errors.Is(err, ErrNoPreview) && !errors.Is(err, ErrNotRAW) {
			t.Errorf("%s: unexpected error %v", name, err)
		}
	}
}

func TestExtract_NotRAW(t *testing.T) {
	if _, err := extractBytes(t, makeJPEG(t, 16, 16)); !errors.Is(err, ErrNotRAW) {
		t.Fatalf("err = %v, want ErrNotRAW", err)
	}
}

func TestJPEGEnd_RequiresFrameHeader(t *testing.T) {
	// SOI, a comment segment, then SOS with no SOF: not a JPEG.
	data := []byte{0xFF, 0xD8, 0xFF, 0xFE, 0x00, 0x04, 'h', 'i', 0xFF, 0xDA, 0x00, 0x02, 1, 2, 0xFF, 0xD9}
	if _, ok := jpegEnd(data, 0); ok {
		t.Fatal("accepted a stream without a frame header")
	}
}

func TestDecodePPM(t *testing.T) {
	ppm := append([]byte("P6\n# c\n2 1\n255\n"), 255, 0, 0, 0, 0, 255)
	img, err := decodeBytes(ppm)
	if err != nil {
		t.Fatal(err)
	}
	if got := color.RGBAModel.Convert(img.At(1, 0)).(color.RGBA); got.B != 255 || got.R != 0 {
		t.Fatalf("pixel = %v", got)
	}
	if _, err := decodeBytes([]byte("P6\n99999 99999\n255\n")); err == nil {
		t.Fatal("accepted an oversized PPM")
	}
}

func TestDecodeExternal_NoToolsInstalled(t *testing.T) {
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	defer func() { lookPath = old }()
	if _, _, err := decodeExternal(t.Context(), filepath.Join(t.TempDir(), "x.orf")); !errors.Is(err, ErrNoPreview) {
		t.Fatalf("err = %v, want ErrNoPreview", err)
	}
	if _, _, err := decodeExternal(t.Context(), "relative.orf"); err == nil {
		t.Fatal("accepted a relative path")
	}
}

func FuzzExtract(f *testing.F) {
	f.Add(cr2Like(f, binary.LittleEndian)[:4096])
	f.Add([]byte("II*\x00\x08\x00\x00\x00\x01\x00\x11\x01\x04\x00\x01\x00\x00\x00\x08\x00\x00\x00"))
	f.Add([]byte("FUJIFILMCCD-RAW 0201"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Extract(bytes.NewReader(data), int64(len(data)))
	})
}
