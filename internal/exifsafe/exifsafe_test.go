package exifsafe

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"testing"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

var le = binary.LittleEndian

// tEntry / tIFD describe a little-endian TIFF for encodeTIFF. An entry with
// sub set is a LONG pointer to that IFD.
type tEntry struct {
	tag, typ uint16
	count    uint32
	data     []byte
	sub      *tIFD
}

type tIFD struct {
	entries []tEntry
	next    *tIFD
}

func encodeTIFF(root *tIFD) []byte {
	buf := []byte("II*\x00\x08\x00\x00\x00")
	var place func(d *tIFD) uint32
	place = func(d *tIFD) uint32 {
		off := len(buf)
		n := len(d.entries)
		buf = append(buf, make([]byte, 2+12*n+4)...)
		le.PutUint16(buf[off:], uint16(n))
		for i, e := range d.entries {
			p := off + 2 + 12*i
			le.PutUint16(buf[p:], e.tag)
			le.PutUint16(buf[p+2:], e.typ)
			le.PutUint32(buf[p+4:], e.count)
			if e.sub != nil {
				continue
			}
			if len(e.data) <= 4 {
				copy(buf[p+8:p+12], e.data)
			} else {
				le.PutUint32(buf[p+8:], uint32(len(buf)))
				buf = append(buf, e.data...)
			}
		}
		for i, e := range d.entries {
			if e.sub != nil {
				so := place(e.sub)
				le.PutUint32(buf[off+2+12*i+8:], so)
			}
		}
		if d.next != nil {
			no := place(d.next)
			le.PutUint32(buf[off+2+12*n:], no)
		}
		return uint32(off)
	}
	place(root)
	return buf
}

func ascii(tag uint16, s string) tEntry {
	b := append([]byte(s), 0)
	return tEntry{tag: tag, typ: 2, count: uint32(len(b)), data: b}
}

func short(tag uint16, v uint16) tEntry {
	b := make([]byte, 2)
	le.PutUint16(b, v)
	return tEntry{tag: tag, typ: 3, count: 1, data: b}
}

// wrapJPEG puts tiff in an APP1 "Exif" segment of a real (tiny) JPEG.
func wrapJPEG(t *testing.T, tiff []byte, extraAPP1 ...[]byte) []byte {
	t.Helper()
	var img bytes.Buffer
	if err := jpeg.Encode(&img, image.NewGray(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	out := []byte{0xFF, 0xD8}
	seg := func(payload []byte) {
		out = append(out, 0xFF, 0xE1, 0, 0)
		binary.BigEndian.PutUint16(out[len(out)-2:], uint16(len(payload)+2))
		out = append(out, payload...)
	}
	for _, p := range extraAPP1 {
		seg(p)
	}
	seg(append([]byte("Exif\x00\x00"), tiff...))
	return append(out, img.Bytes()[2:]...)
}

// decodeWithin fails the test if Decode doesn't return within d — the
// regression being guarded is goexif looping forever.
func decodeWithin(t *testing.T, d time.Duration, data []byte) (*exif.Exif, error) {
	t.Helper()
	type res struct {
		x   *exif.Exif
		err error
	}
	ch := make(chan res, 1)
	go func() {
		x, err := Decode(bytes.NewReader(data))
		ch <- res{x, err}
	}()
	select {
	case r := <-ch:
		return r.x, r.err
	case <-time.After(d):
		t.Fatalf("Decode did not return within %s", d)
		return nil, nil
	}
}

// The reproduction from the 2026-11 scan: two empty IFDs pointing at each
// other. goexif alone never returns on it and allocates ~1 GB/s.
func TestDecode_MutualIFDLoopRefusedPromptly(t *testing.T) {
	data := []byte{
		0xFF, 0xD8, // SOI
		0xFF, 0xE1, 0x00, 0x1C, // APP1, length 28
		'E', 'x', 'i', 'f', 0, 0,
		'I', 'I', 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00, // TIFF header, IFD0 at 8
		0x00, 0x00, 0x0E, 0x00, 0x00, 0x00, // IFD@8: 0 entries, next 14
		0x00, 0x00, 0x08, 0x00, 0x00, 0x00, // IFD@14: 0 entries, next 8
		0xFF, 0xD9, // EOI
	}
	if len(data) != 34 {
		t.Fatalf("repro is %d bytes, want 34", len(data))
	}
	x, err := decodeWithin(t, 5*time.Second, data)
	if !errors.Is(err, ErrRefused) || x != nil {
		t.Fatalf("Decode = (%v, %v), want (nil, ErrRefused)", x, err)
	}
	if !exif.IsCriticalError(err) {
		t.Error("a refusal must read as critical (no usable EXIF)")
	}
}

func TestDecode_ParsesNormalEXIF(t *testing.T) {
	tiff := encodeTIFF(&tIFD{
		entries: []tEntry{
			ascii(0x010F, "Canon"),
			ascii(0x0110, "Canon EOS R5"),
			short(0x0112, 6),
			{tag: tagExifIFD, typ: 4, count: 1, sub: &tIFD{entries: []tEntry{
				ascii(0x9003, "2024:05:06 07:08:09"),
				{tag: tagInteropIFD, typ: 4, count: 1, sub: &tIFD{entries: []tEntry{ascii(0x0001, "R98")}}},
			}}},
			{tag: tagGPSIFD, typ: 4, count: 1, sub: &tIFD{entries: []tEntry{ascii(0x0001, "N")}}},
		},
		next: &tIFD{entries: []tEntry{short(0x0103, 6)}}, // IFD1 (thumbnail)
	})
	xmp := append([]byte("http://ns.adobe.com/xap/1.0/\x00"), []byte("<x:xmpmeta/>")...)
	for name, data := range map[string][]byte{
		"jpeg":             wrapJPEG(t, tiff),
		"jpeg xmp first":   wrapJPEG(t, tiff, xmp),
		"tiff file":        tiff,
		"raw exif payload": append([]byte("Exif\x00\x00"), tiff...),
	} {
		t.Run(name, func(t *testing.T) {
			x, err := decodeWithin(t, 5*time.Second, data)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			for field, want := range map[exif.FieldName]string{
				exif.Make: "Canon", exif.Model: "Canon EOS R5", exif.DateTimeOriginal: "2024:05:06 07:08:09",
				exif.InteroperabilityIndex: "R98", exif.GPSLatitudeRef: "N",
			} {
				tag, err := x.Get(field)
				if err != nil {
					t.Fatalf("Get(%s): %v", field, err)
				}
				if got, _ := tag.StringVal(); got != want {
					t.Errorf("%s = %q, want %q", field, got, want)
				}
			}
			o, err := x.Get(exif.Orientation)
			if err != nil {
				t.Fatalf("Get(Orientation): %v", err)
			}
			if v, _ := o.Int(0); v != 6 {
				t.Errorf("Orientation = %d, want 6", v)
			}
		})
	}
}

// A sub-IFD goexif can't read only drops that sub-IFD (a non-critical error);
// the check must not turn it into a refusal of the whole block.
func TestDecode_BrokenSubIFDKeepsIFD0(t *testing.T) {
	tiff := encodeTIFF(&tIFD{entries: []tEntry{
		ascii(0x010F, "Nikon"),
		{tag: tagExifIFD, typ: 4, count: 1, data: []byte{0x0F, 0x27, 0, 0}}, // offset 9999: past the data
	}})
	x, err := decodeWithin(t, 5*time.Second, wrapJPEG(t, tiff))
	if x == nil || (err != nil && exif.IsCriticalError(err)) {
		t.Fatalf("Decode = (%v, %v), want partial EXIF", x, err)
	}
	tag, gerr := x.Get(exif.Make)
	if gerr != nil {
		t.Fatalf("Get(Make): %v", gerr)
	}
	if got, _ := tag.StringVal(); got != "Nikon" {
		t.Errorf("Make = %q, want Nikon", got)
	}
}

func TestDecode_Refusals(t *testing.T) {
	// 65 chained IFDs: over maxIFDs.
	long := &tIFD{entries: []tEntry{short(0x0112, 1)}}
	for i := 0; i < maxIFDs; i++ {
		long = &tIFD{entries: []tEntry{short(0x0112, 1)}, next: long}
	}

	// SHORT × 0x80000001 wraps goexif's uint32 length to 2 bytes inline,
	// then converts 2^31 elements (16 GiB of int64).
	wrap := encodeTIFF(&tIFD{entries: []tEntry{{tag: 0x0112, typ: 3, count: 0x80000001, data: []byte{1, 0}}}})

	// 1000 BYTE tags all pointing at one 40 KB value: 40 MB declared from a
	// 52 KB block, 360 MB once goexif expands each byte to an int64.
	shared := encodeTIFF(&tIFD{entries: []tEntry{{tag: 0x9286, typ: 1, count: 40000, data: make([]byte, 40000)}}})
	valOff := le.Uint32(shared[8+2+8:])
	ifd := make([]byte, 2+12*1000+4)
	le.PutUint16(ifd, 1000)
	for i := 0; i < 1000; i++ {
		e := ifd[2+12*i:]
		le.PutUint16(e, uint16(0x9000+i))
		le.PutUint16(e[2:], 1)
		le.PutUint32(e[4:], 40000)
		le.PutUint32(e[8:], valOff)
	}
	bomb := append(append([]byte{}, shared...), ifd...)
	le.PutUint32(bomb[4:], uint32(len(shared))) // IFD0 → the 1000-entry IFD

	// One IFD declaring more entries than maxIFDEntries.
	wide := make([]byte, 8+2+12*(maxIFDEntries+1)+4)
	copy(wide, "II*\x00\x08\x00\x00\x00")
	le.PutUint16(wide[8:], maxIFDEntries+1)
	for i := 0; i <= maxIFDEntries; i++ {
		e := wide[10+12*i:]
		le.PutUint16(e, 0x0112)
		le.PutUint16(e[2:], 3)
		le.PutUint32(e[4:], 1)
	}

	for name, tiff := range map[string][]byte{
		"self loop":         {'I', 'I', 0x2A, 0, 8, 0, 0, 0, 0, 0, 8, 0, 0, 0},
		"long chain":        encodeTIFF(long),
		"wrapping count":    wrap,
		"shared value bomb": bomb,
		"too many entries":  wide,
	} {
		t.Run(name, func(t *testing.T) {
			data := tiff
			if len(tiff) < 60000 {
				data = wrapJPEG(t, tiff)
			}
			x, err := decodeWithin(t, 5*time.Second, data)
			if !errors.Is(err, ErrRefused) || x != nil {
				t.Fatalf("Decode = (%v, %v), want (nil, ErrRefused)", x, err)
			}
		})
	}
}

func TestDecode_NoEXIF(t *testing.T) {
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, image.NewGray(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"plain jpeg": plain.Bytes(),
		"png":        []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR"),
		"empty":      nil,
		"garbage":    []byte("not an image at all"),
	} {
		t.Run(name, func(t *testing.T) {
			x, err := Decode(bytes.NewReader(data))
			if x != nil || err == nil || !exif.IsCriticalError(err) {
				t.Fatalf("Decode = (%v, %v), want (nil, critical error)", x, err)
			}
		})
	}
}

// sparseTIFF is a large TIFF "file" (an EXIF directory at the start, then
// zeros) that records the furthest byte read. It stands in for an open file:
// a Reader that is also a ReaderAt and a Seeker.
type sparseTIFF struct {
	head    []byte
	size    int64
	pos     int64
	maxRead int64
}

func (s *sparseTIFF) ReadAt(p []byte, off int64) (int, error) {
	if off >= s.size {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > s.size-off {
		n = int(s.size - off)
	}
	for i := 0; i < n; i++ {
		p[i] = 0
		if off+int64(i) < int64(len(s.head)) {
			p[i] = s.head[off+int64(i)]
		}
	}
	if end := off + int64(n); end > s.maxRead {
		s.maxRead = end
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (s *sparseTIFF) Read(p []byte) (int, error) {
	n, err := s.ReadAt(p, s.pos)
	s.pos += int64(n)
	return n, err
}

func (s *sparseTIFF) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		s.pos = off
	case io.SeekCurrent:
		s.pos += off
	case io.SeekEnd:
		s.pos = s.size + off
	}
	return s.pos, nil
}

// goexif ReadAlls a TIFF input (three copies). Through Decode a 200 MB TIFF
// whose directory comes first costs only the bytes the directory occupies.
func TestDecode_LargeTIFFReadsOnlyTheDirectory(t *testing.T) {
	head := encodeTIFF(&tIFD{entries: []tEntry{ascii(0x010F, "Scanner"), short(0x0112, 1)}})
	f := &sparseTIFF{head: head, size: 200 << 20}
	x, err := Decode(f)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if tag, err := x.Get(exif.Make); err != nil {
		t.Fatalf("Get(Make): %v", err)
	} else if got, _ := tag.StringVal(); got != "Scanner" {
		t.Errorf("Make = %q", got)
	}
	if f.maxRead > 4096 {
		t.Errorf("read up to byte %d of a TIFF whose EXIF ends at %d", f.maxRead, len(head))
	}
}
