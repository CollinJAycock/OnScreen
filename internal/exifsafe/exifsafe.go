// Package exifsafe puts a bounds check in front of github.com/rwcarlsen/goexif
// so an EXIF block from an untrusted file can't hang or exhaust the process.
//
// goexif (last release 2019) trusts the structure it is handed:
//
//   - tiff.Decode follows the IFD0 → IFD1 → … chain and only notices an IFD
//     that points at itself. Two IFDs that point at each other loop forever,
//     appending a Dir per pass — a 34-byte JPEG grows the heap by ~1 GB/s
//     until the process dies, and a photo-library rescan repeats it.
//   - A tag's value length is computed as size × count in uint32, so a count
//     that wraps the multiplication reads 4 bytes and then allocates
//     count × 8 bytes for the converted values (tens of GB).
//   - Every tag value is copied, then expanded (one int64 per BYTE), and many
//     tags may point at the same bytes, so a small block can declare far more
//     value bytes than it holds.
//   - For a TIFF-shaped input it reads the whole file, three times over.
//
// Decode finds the EXIF TIFF structure itself (JPEG APP1, raw "Exif\0\0", or
// a TIFF file), walks every IFD goexif would read with a visited set and hard
// caps on IFDs, entries and value bytes, and only then hands goexif the
// prefix of the structure those IFDs and values occupy. Anything over a cap is
// refused (ErrRefused): the caller treats it as "no EXIF".
package exifsafe

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/rwcarlsen/goexif/exif"
)

const (
	// maxJPEGScan bounds the bytes walked through a JPEG's segments before
	// the EXIF APP1 turns up. EXIF sits right after SOI (or a JFIF APP0) in
	// every encoder; ICC profiles and Photoshop blocks before it are at most
	// a few MB.
	maxJPEGScan = 16 << 20
	// maxTIFFBytes bounds the TIFF-shaped input goexif is given. A TIFF
	// whose directory sits past this has no EXIF here; goexif used to read
	// the whole file into memory three times to find it.
	maxTIFFBytes = 32 << 20
	// maxIFDs bounds the IFDs walked: the IFD0 chain (a JPEG has two) plus
	// the Exif / GPS / Interop sub-IFDs.
	maxIFDs = 64
	// maxIFDEntries bounds one IFD. Real ones hold well under 300.
	maxIFDEntries = 1024
	// maxTotalEntries bounds the entries across all IFDs, which may overlap.
	maxTotalEntries = 8192
	// maxValueCost bounds what goexif allocates for tag values: the copied
	// bytes plus the converted elements (see entryCost).
	maxValueCost = 32 << 20
)

// ErrRefused means the EXIF structure is malformed or exceeds a bound and was
// not handed to goexif.
var ErrRefused = errors.New("exifsafe: EXIF structure refused")

// errNoEXIF is returned when the input carries no EXIF block at all.
var errNoEXIF = errors.New("exifsafe: no EXIF block")

// Decode is exif.Decode for untrusted input. Its errors follow goexif's
// contract: exif.IsCriticalError reports true for "no usable EXIF" (including
// ErrRefused), false when the returned *exif.Exif holds partial data.
func Decode(r io.Reader) (*exif.Exif, error) {
	ra, size, err := locate(r)
	if err != nil {
		return nil, err
	}
	end, err := check(ra, size)
	if err != nil {
		return nil, err
	}
	// goexif only reads what the checked IFDs and values occupy, so give it
	// exactly that: a large TIFF whose directory comes first costs a few KB
	// instead of the whole file.
	prefix := make([]byte, end)
	if _, err := ra.ReadAt(prefix, 0); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("exifsafe: read EXIF: %w", err)
	}
	return exif.Decode(bytes.NewReader(prefix))
}

// locate returns the TIFF structure EXIF is stored as, wherever the input
// keeps it, and its size.
func locate(r io.Reader) (io.ReaderAt, int64, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, 0, errNoEXIF
	}
	switch string(head[:]) {
	case "II*\x00", "MM\x00*":
		return tiffInput(r, head[:])
	case "Exif":
		var rest [2]byte
		if _, err := io.ReadFull(r, rest[:]); err != nil || rest != [2]byte{} {
			return nil, 0, errNoEXIF
		}
		blob, err := readCapped(r, maxTIFFBytes)
		if err != nil {
			return nil, 0, err
		}
		return bytes.NewReader(blob), int64(len(blob)), nil
	}
	if head[0] != 0xFF || head[1] != 0xD8 {
		// goexif would scan any other input for an FF E1 byte pair; outside
		// JPEG that only ever finds compressed data, never an EXIF block.
		return nil, 0, errNoEXIF
	}
	blob, err := jpegEXIF(io.MultiReader(bytes.NewReader(head[2:]), r))
	if err != nil {
		return nil, 0, err
	}
	return bytes.NewReader(blob), int64(len(blob)), nil
}

// tiffInput serves a TIFF file. A ReaderAt (an open file) is read in place, so
// only the bytes the IFDs occupy are ever copied; anything else is buffered up
// to maxTIFFBytes.
func tiffInput(r io.Reader, head []byte) (io.ReaderAt, int64, error) {
	if ra, ok := r.(io.ReaderAt); ok {
		if s, ok := r.(io.Seeker); ok {
			if size, err := s.Seek(0, io.SeekEnd); err == nil {
				if size > maxTIFFBytes {
					size = maxTIFFBytes
				}
				return ra, size, nil
			}
		}
	}
	rest, err := readCapped(r, maxTIFFBytes-int64(len(head)))
	if err != nil {
		return nil, 0, err
	}
	blob := append(append([]byte{}, head...), rest...)
	return bytes.NewReader(blob), int64(len(blob)), nil
}

// readCapped reads at most n bytes; a longer input is truncated, which leaves
// any IFD past the cap out of range (refused by check).
func readCapped(r io.Reader, n int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, n))
	if err != nil {
		return nil, fmt.Errorf("exifsafe: read: %w", err)
	}
	return b, nil
}

// jpegEXIF walks a JPEG's marker segments (r is positioned just after SOI) and
// returns the TIFF structure of the first APP1 that carries "Exif\0\0". It
// stops at the start of the image data: EXIF always precedes it.
func jpegEXIF(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(io.LimitReader(r, maxJPEGScan))
	for {
		b, err := br.ReadByte()
		if err != nil {
			return nil, errNoEXIF
		}
		if b != 0xFF {
			return nil, errNoEXIF
		}
		m, err := br.ReadByte()
		for err == nil && m == 0xFF { // fill bytes before a marker
			m, err = br.ReadByte()
		}
		if err != nil {
			return nil, errNoEXIF
		}
		switch {
		case m == 0xDA || m == 0xD9: // start of scan / end of image
			return nil, errNoEXIF
		case m == 0x01 || (m >= 0xD0 && m <= 0xD7): // no length field
			continue
		}
		var lb [2]byte
		if _, err := io.ReadFull(br, lb[:]); err != nil {
			return nil, errNoEXIF
		}
		n := int(binary.BigEndian.Uint16(lb[:]))
		if n < 2 {
			return nil, errNoEXIF
		}
		n -= 2
		if m != 0xE1 || n < 6 {
			if _, err := br.Discard(n); err != nil {
				return nil, errNoEXIF
			}
			continue
		}
		seg := make([]byte, n)
		if _, err := io.ReadFull(br, seg); err != nil {
			return nil, errNoEXIF
		}
		if bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return seg[6:], nil
		}
		// An XMP APP1 ahead of the EXIF one: keep looking.
	}
}

// Pointer tags goexif follows from the IFDs it loads (exif.loadSubDir).
const (
	tagExifIFD    = 0x8769
	tagGPSIFD     = 0x8825
	tagInteropIFD = 0xA005
)

type checker struct {
	r       io.ReaderAt
	size    int64
	bo      binary.ByteOrder
	visited map[int64]bool
	ifds    int
	entries int
	cost    int64
	end     int64 // furthest byte goexif will read
}

// check walks the TIFF structure the way goexif reads it and returns the end
// of the bytes it occupies.
func check(r io.ReaderAt, size int64) (int64, error) {
	var h [8]byte
	if size < 8 {
		return 0, ErrRefused
	}
	if _, err := r.ReadAt(h[:], 0); err != nil {
		return 0, ErrRefused
	}
	c := &checker{r: r, size: size, visited: map[int64]bool{}, end: 8}
	switch string(h[:4]) {
	case "II*\x00":
		c.bo = binary.LittleEndian
	case "MM\x00*":
		c.bo = binary.BigEndian
	default:
		return 0, ErrRefused
	}
	// The IFD0 chain. tiff.Decode follows every next pointer and decodes
	// each IFD in full, failing the whole decode on any structural error, so
	// a chain IFD that doesn't fit is refused here rather than skipped.
	off := int64(c.bo.Uint32(h[4:8]))
	for off != 0 {
		if c.visited[off] {
			return 0, fmt.Errorf("%w: IFD chain loops at offset %d", ErrRefused, off)
		}
		c.visited[off] = true
		next, err := c.ifd(off, true, 0)
		if err != nil {
			return 0, err
		}
		off = next
	}
	return c.end, nil
}

// ifd checks the IFD at off and the sub-IFDs it points to. chain marks an
// IFD0-chain directory (goexif fails outright on its errors); a sub-IFD is read
// on its own and a structural error there only drops that sub-IFD in goexif,
// so it is accounted up to the point goexif stops and not refused. It returns
// the next-IFD offset.
func (c *checker) ifd(off int64, chain bool, depth int) (int64, error) {
	c.ifds++
	if c.ifds > maxIFDs {
		return 0, fmt.Errorf("%w: more than %d IFDs", ErrRefused, maxIFDs)
	}
	var nb [2]byte
	if off < 0 || off+2 > c.size {
		if chain {
			return 0, fmt.Errorf("%w: IFD offset %d out of range", ErrRefused, off)
		}
		return 0, nil
	}
	if _, err := c.r.ReadAt(nb[:], off); err != nil {
		return 0, ErrRefused
	}
	// goexif reads the count as int16; a negative one means no entries.
	n := int64(int16(c.bo.Uint16(nb[:])))
	if n < 0 {
		n = 0
	}
	if n > maxIFDEntries {
		return 0, fmt.Errorf("%w: IFD with %d entries", ErrRefused, n)
	}
	c.entries += int(n)
	if c.entries > maxTotalEntries {
		return 0, fmt.Errorf("%w: more than %d IFD entries", ErrRefused, maxTotalEntries)
	}
	ifdEnd := off + 2 + n*12 + 4
	if ifdEnd > c.size {
		if chain {
			return 0, fmt.Errorf("%w: IFD at %d overruns the data", ErrRefused, off)
		}
		ifdEnd = c.size
	}
	raw := make([]byte, ifdEnd-off-2)
	if _, err := c.r.ReadAt(raw, off+2); err != nil && !errors.Is(err, io.EOF) {
		return 0, ErrRefused
	}
	c.extend(ifdEnd)

	var subs []int64
	for i := int64(0); i < n; i++ {
		if (i+1)*12 > int64(len(raw)) {
			break // truncated sub-IFD: goexif stops at this entry
		}
		e := raw[i*12 : i*12+12]
		tag, typ, count := c.bo.Uint16(e[0:2]), c.bo.Uint16(e[2:4]), c.bo.Uint32(e[4:8])
		ok, err := c.value(e, typ, count)
		if err != nil {
			return 0, err
		}
		if !ok {
			if chain {
				return 0, fmt.Errorf("%w: unreadable tag 0x%04X", ErrRefused, tag)
			}
			break
		}
		if depth < 2 && (tag == tagExifIFD || tag == tagGPSIFD || tag == tagInteropIFD) {
			if p, ok := c.firstInt(e, typ, count); ok {
				subs = append(subs, p)
			}
		}
	}
	for _, p := range subs {
		if _, err := c.ifd(p, false, depth+1); err != nil {
			return 0, err
		}
	}
	if int64(len(raw)) < n*12+4 {
		return 0, nil
	}
	return int64(c.bo.Uint32(raw[n*12:])), nil
}

// typeSize mirrors goexif's tiff.typeSize: types 1–12; anything else (0)
// makes goexif fail the IFD.
func typeSize(typ uint16) uint64 {
	switch typ {
	case 1, 2, 6, 7:
		return 1
	case 3, 8:
		return 2
	case 4, 9, 11:
		return 4
	case 5, 10, 12:
		return 8
	}
	return 0
}

// value accounts one entry's value. ok=false means goexif fails on this tag
// (unknown type, zero count, value past the data); an error means the tag
// would make goexif allocate without bound.
func (c *checker) value(e []byte, typ uint16, count uint32) (bool, error) {
	ts := typeSize(typ)
	if ts == 0 || count == 0 || count == math.MaxUint32 {
		return false, nil
	}
	n := ts * uint64(count)
	if n > math.MaxUint32 {
		// goexif's uint32 size×count wraps, reads a few bytes, then
		// allocates count converted elements.
		return false, fmt.Errorf("%w: tag value length overflows", ErrRefused)
	}
	c.cost += entryCost(typ, n, count)
	if c.cost > maxValueCost {
		return false, fmt.Errorf("%w: tag values exceed %d bytes", ErrRefused, maxValueCost)
	}
	if n <= 4 {
		return true, nil
	}
	voff := int64(c.bo.Uint32(e[8:12]))
	if voff+int64(n) > c.size {
		return false, nil
	}
	c.extend(voff + int64(n))
	return true, nil
}

// entryCost estimates what goexif allocates for one tag: the Tag itself, the
// copied value bytes, and the converted values (an int64 or float64 per
// integer / float element, an []int64 pair per rational, a string copy).
func entryCost(typ uint16, n uint64, count uint32) int64 {
	cost := int64(128) + int64(n)
	switch typ {
	case 1, 3, 4, 6, 8, 9, 11, 12:
		cost += int64(count) * 8
	case 5, 10:
		cost += int64(count) * 48
	case 2:
		cost += int64(n)
	}
	return cost
}

// firstInt reads the first element of an integer entry, as goexif's
// tag.Int64(0) would, for a sub-IFD pointer.
func (c *checker) firstInt(e []byte, typ uint16, count uint32) (int64, bool) {
	n := typeSize(typ) * uint64(count)
	var v [4]byte
	if n <= 4 {
		copy(v[:], e[8:12])
	} else {
		if _, err := c.r.ReadAt(v[:typeSize(typ)], int64(c.bo.Uint32(e[8:12]))); err != nil {
			return 0, false
		}
	}
	switch typ {
	case 1:
		return int64(v[0]), true
	case 6:
		return int64(int8(v[0])), true
	case 3:
		return int64(c.bo.Uint16(v[:2])), true
	case 8:
		return int64(int16(c.bo.Uint16(v[:2]))), true
	case 4:
		return int64(c.bo.Uint32(v[:])), true
	case 9:
		return int64(int32(c.bo.Uint32(v[:]))), true
	}
	return 0, false
}

func (c *checker) extend(end int64) {
	if end > c.end {
		c.end = end
	}
}
