package rawpreview

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strings"
)

// TIFF tags used here.
const (
	tagCompression     = 0x0103
	tagMake            = 0x010F
	tagModel           = 0x0110
	tagStripOffsets    = 0x0111
	tagOrientation     = 0x0112
	tagStripByteCounts = 0x0117
	tagDateTime        = 0x0132
	tagSubIFDs         = 0x014A
	tagJPEGOffset      = 0x0201
	tagJPEGLength      = 0x0202
	tagExposureTime    = 0x829A
	tagFNumber         = 0x829D
	tagExifIFD         = 0x8769
	tagGPSIFD          = 0x8825
	tagISO             = 0x8827
	tagDateTimeOrig    = 0x9003
	tagFlash           = 0x9209
	tagFocalLength     = 0x920A
	tagLensModel       = 0xA434
	// Panasonic RW2 keeps a complete JPEG as the value of tag 0x002E.
	tagRW2JpgFromRaw = 0x002E
)

// isTIFFMagic recognises standard TIFF headers plus the variants RAW formats
// use: Olympus ORF ("IIRO", "IIRS", "MMOR") and Panasonic RW2 ("IIU\0").
func isTIFFMagic(h []byte) bool {
	if len(h) < 4 {
		return false
	}
	switch string(h[:4]) {
	case "II*\x00", "MM\x00*", "IIRO", "IIRS", "MMOR", "IIU\x00":
		return true
	}
	return false
}

// tiffFile reads IFDs from a TIFF structure that starts at base inside r.
// All offsets in the structure are relative to base.
type tiffFile struct {
	r       io.ReaderAt
	size    int64 // absolute end of readable data
	base    int64
	bo      binary.ByteOrder
	visited map[int64]bool
}

type ifdEntry struct {
	tag, typ uint16
	count    uint32
	// valuePos is the absolute position of the value: inline in the entry
	// when it fits in four bytes, else base + the offset it holds.
	valuePos int64
}

func newTIFF(r io.ReaderAt, base, size int64) (*tiffFile, int64, bool) {
	var h [8]byte
	if base < 0 || base+8 > size {
		return nil, 0, false
	}
	if _, err := r.ReadAt(h[:], base); err != nil {
		return nil, 0, false
	}
	var bo binary.ByteOrder
	switch string(h[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return nil, 0, false
	}
	t := &tiffFile{r: r, size: size, base: base, bo: bo, visited: map[int64]bool{}}
	return t, int64(bo.Uint32(h[4:8])), true
}

func typeSize(typ uint16) int64 {
	switch typ {
	case 1, 2, 6, 7:
		return 1
	case 3, 8:
		return 2
	case 4, 9, 11, 13:
		return 4
	case 5, 10, 12:
		return 8
	}
	return 0
}

// readIFD reads one IFD at rel (relative to base). It returns the entries
// and the relative offset of the next IFD in the chain (0 = none). An IFD
// already visited, past EOF, or over the global IFD budget yields nothing.
func (t *tiffFile) readIFD(rel int64) ([]ifdEntry, int64) {
	abs := t.base + rel
	if rel <= 0 || abs+2 > t.size || t.visited[abs] || len(t.visited) >= maxIFDs {
		return nil, 0
	}
	t.visited[abs] = true
	var nb [2]byte
	if _, err := t.r.ReadAt(nb[:], abs); err != nil {
		return nil, 0
	}
	n := int64(t.bo.Uint16(nb[:]))
	if n == 0 || n > maxIFDEntries || abs+2+n*12+4 > t.size {
		return nil, 0
	}
	raw := make([]byte, n*12+4)
	if _, err := t.r.ReadAt(raw, abs+2); err != nil {
		return nil, 0
	}
	entries := make([]ifdEntry, 0, n)
	for i := int64(0); i < n; i++ {
		e := raw[i*12 : i*12+12]
		ent := ifdEntry{
			tag:   t.bo.Uint16(e[0:2]),
			typ:   t.bo.Uint16(e[2:4]),
			count: t.bo.Uint32(e[4:8]),
		}
		ts := typeSize(ent.typ)
		if ts == 0 {
			continue
		}
		if int64(ent.count)*ts <= 4 {
			ent.valuePos = abs + 2 + i*12 + 8
		} else {
			ent.valuePos = t.base + int64(t.bo.Uint32(e[8:12]))
		}
		entries = append(entries, ent)
	}
	next := int64(t.bo.Uint32(raw[n*12:]))
	return entries, next
}

// bytesOf reads an entry's value, capped at max bytes.
func (t *tiffFile) bytesOf(e ifdEntry, max int64) ([]byte, bool) {
	n := int64(e.count) * typeSize(e.typ)
	if n <= 0 || n > max || e.valuePos < 0 || e.valuePos > t.size || n > t.size-e.valuePos {
		return nil, false
	}
	buf := make([]byte, n)
	if _, err := t.r.ReadAt(buf, e.valuePos); err != nil && !errors.Is(err, io.EOF) {
		return nil, false
	}
	return buf, true
}

// uints reads an integer-typed entry (BYTE/SHORT/LONG/IFD) as uint32s.
func (t *tiffFile) uints(e ifdEntry) []uint32 {
	buf, ok := t.bytesOf(e, maxValueBytes)
	if !ok {
		return nil
	}
	out := make([]uint32, 0, e.count)
	switch e.typ {
	case 1, 7:
		for _, b := range buf {
			out = append(out, uint32(b))
		}
	case 3:
		for i := 0; i+2 <= len(buf); i += 2 {
			out = append(out, uint32(t.bo.Uint16(buf[i:])))
		}
	case 4, 13:
		for i := 0; i+4 <= len(buf); i += 4 {
			out = append(out, t.bo.Uint32(buf[i:]))
		}
	}
	return out
}

func (t *tiffFile) uint(e ifdEntry) (uint32, bool) {
	v := t.uints(e)
	if len(v) == 0 {
		return 0, false
	}
	return v[0], true
}

func (t *tiffFile) str(e ifdEntry) string {
	if e.typ != 2 {
		return ""
	}
	buf, ok := t.bytesOf(e, 1024)
	if !ok {
		return ""
	}
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	return strings.TrimSpace(string(buf))
}

// rationals reads a RATIONAL entry as (num, den) pairs.
func (t *tiffFile) rationals(e ifdEntry) [][2]uint32 {
	if e.typ != 5 && e.typ != 10 {
		return nil
	}
	buf, ok := t.bytesOf(e, 256)
	if !ok {
		return nil
	}
	var out [][2]uint32
	for i := 0; i+8 <= len(buf); i += 8 {
		out = append(out, [2]uint32{t.bo.Uint32(buf[i:]), t.bo.Uint32(buf[i+4:])})
	}
	return out
}

func (t *tiffFile) ratFloat(e ifdEntry) (float64, bool) {
	r := t.rationals(e)
	if len(r) == 0 || r[0][1] == 0 {
		return 0, false
	}
	return float64(r[0][0]) / float64(r[0][1]), true
}

// ifdKind says which tags of an IFD feed the metadata.
type ifdKind int

const (
	kindIFD0  ifdKind = iota // primary image: make, model, orientation
	kindOther                // further chain / sub-IFDs: previews only
	kindExif
	kindGPS
)

// walk visits the IFD at rel and everything it points to, appending preview
// candidates and filling meta.
func (t *tiffFile) walk(rel int64, kind ifdKind, meta *Meta, cands *[]candidate, depth int) {
	if depth > 4 {
		return
	}
	entries, _ := t.readIFD(rel)
	t.visitEntries(entries, kind, meta, cands, depth)
}

func (t *tiffFile) visitEntries(entries []ifdEntry, kind ifdKind, meta *Meta, cands *[]candidate, depth int) {
	if len(entries) == 0 {
		return
	}
	if kind == kindGPS {
		t.gpsIFD(entries, meta)
		return
	}
	var jpegOff, jpegLen, compression uint32
	var stripOffs, stripLens []uint32
	for _, e := range entries {
		switch kind {
		case kindExif:
			t.exifTag(e, meta)
		case kindIFD0:
			t.ifd0Tag(e, meta)
		}
		switch e.tag {
		case tagJPEGOffset:
			jpegOff, _ = t.uint(e)
		case tagJPEGLength:
			jpegLen, _ = t.uint(e)
		case tagCompression:
			compression, _ = t.uint(e)
		case tagStripOffsets:
			stripOffs = t.uints(e)
		case tagStripByteCounts:
			stripLens = t.uints(e)
		case tagRW2JpgFromRaw:
			if e.typ == 7 && e.count > 0 {
				*cands = append(*cands, candidate{off: e.valuePos, n: int64(e.count)})
			}
		case tagSubIFDs:
			for _, off := range t.uints(e) {
				t.walk(int64(off), kindOther, meta, cands, depth+1)
			}
		case tagExifIFD:
			if off, ok := t.uint(e); ok {
				t.walk(int64(off), kindExif, meta, cands, depth+1)
			}
		case tagGPSIFD:
			if off, ok := t.uint(e); ok {
				t.walk(int64(off), kindGPS, meta, cands, depth+1)
			}
		}
	}
	if jpegOff > 0 && jpegLen > 0 {
		*cands = append(*cands, candidate{off: t.base + int64(jpegOff), n: int64(jpegLen)})
	}
	// A single-strip image compressed as (old-style or new-style) JPEG is a
	// preview candidate: CR2's IFD0 full-size JPEG, DNG preview sub-IFDs.
	// Lossless-JPEG raw data looks the same here and is rejected when the
	// candidate fails to decode.
	if (compression == 6 || compression == 7) && len(stripOffs) == 1 && len(stripLens) == 1 {
		*cands = append(*cands, candidate{off: t.base + int64(stripOffs[0]), n: int64(stripLens[0])})
	}
}

func (t *tiffFile) ifd0Tag(e ifdEntry, meta *Meta) {
	switch e.tag {
	case tagMake:
		if meta.Make == "" {
			meta.Make = t.str(e)
		}
	case tagModel:
		if meta.Model == "" {
			meta.Model = t.str(e)
		}
	case tagOrientation:
		if v, ok := t.uint(e); ok && v >= 1 && v <= 8 && meta.Orientation == 0 {
			meta.Orientation = int(v)
		}
	case tagDateTime:
		// Fallback only; DateTimeOriginal in the EXIF IFD wins.
		if meta.DateTimeOriginal == "" {
			meta.DateTimeOriginal = t.str(e)
		}
	}
}

func (t *tiffFile) exifTag(e ifdEntry, meta *Meta) {
	switch e.tag {
	case tagDateTimeOrig:
		if s := t.str(e); s != "" {
			meta.DateTimeOriginal = s
		}
	case tagExposureTime:
		if r := t.rationals(e); len(r) > 0 && r[0][1] != 0 {
			meta.ExposureNum, meta.ExposureDen = r[0][0], r[0][1]
		}
	case tagFNumber:
		if v, ok := t.ratFloat(e); ok && v > 0 {
			meta.FNumber = &v
		}
	case tagFocalLength:
		if v, ok := t.ratFloat(e); ok && v > 0 {
			meta.FocalLength = &v
		}
	case tagISO:
		if v, ok := t.uint(e); ok && v > 0 {
			meta.ISO = int(v)
		}
	case tagFlash:
		if v, ok := t.uint(e); ok {
			f := int(v)
			meta.Flash = &f
		}
	case tagLensModel:
		if s := t.str(e); s != "" {
			meta.LensModel = s
		}
	}
}

// gpsIFD reads the GPS IFD. The hemisphere refs may come before or after
// the values, so the IFD is read as a whole.
func (t *tiffFile) gpsIFD(entries []ifdEntry, meta *Meta) {
	var latRef, lonRef string
	var lat, lon, alt *float64
	altBelow := false
	for _, e := range entries {
		switch e.tag {
		case 1, 3: // LatitudeRef / LongitudeRef ("N"/"S", "E"/"W")
			s := strings.ToUpper(t.str(e))
			if e.tag == 1 {
				latRef = s
			} else {
				lonRef = s
			}
		case 2, 4: // Latitude / Longitude as degrees, minutes, seconds
			v, ok := t.dms(e)
			if !ok {
				continue
			}
			if e.tag == 2 && v <= 90 {
				lat = &v
			} else if e.tag == 4 && v <= 180 {
				lon = &v
			}
		case 5: // AltitudeRef: 1 = below sea level
			if v, ok := t.uint(e); ok && v == 1 {
				altBelow = true
			}
		case 6:
			if v, ok := t.ratFloat(e); ok {
				alt = &v
			}
		}
	}
	if lat != nil && lon != nil && !(*lat == 0 && *lon == 0) {
		la, lo := *lat, *lon
		if strings.HasPrefix(latRef, "S") {
			la = -la
		}
		if strings.HasPrefix(lonRef, "W") {
			lo = -lo
		}
		meta.GPSLat, meta.GPSLon = &la, &lo
	}
	if alt != nil {
		a := *alt
		if altBelow {
			a = -a
		}
		meta.GPSAlt = &a
	}
}

// dms reads a degrees / minutes / seconds RATIONAL triple as degrees.
func (t *tiffFile) dms(e ifdEntry) (float64, bool) {
	r := t.rationals(e)
	if len(r) < 3 {
		return 0, false
	}
	var v float64
	for i, div := range []float64{1, 60, 3600} {
		if r[i][1] == 0 {
			return 0, false
		}
		v += float64(r[i][0]) / float64(r[i][1]) / div
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// extractTIFF walks a TIFF-shaped RAW (CR2, NEF, ARW, DNG, ORF, RW2, PEF):
// the IFD0 chain and every sub-IFD it references.
func extractTIFF(r io.ReaderAt, size int64, meta *Meta) []candidate {
	t, first, ok := newTIFF(r, 0, size)
	if !ok {
		return nil
	}
	var cands []candidate
	rel := first
	for i := 0; rel > 0 && i < maxIFDs; i++ {
		entries, next := t.readIFD(rel)
		if entries == nil {
			break
		}
		kind := kindOther
		if i == 0 {
			kind = kindIFD0
		}
		t.visitEntries(entries, kind, meta, &cands, 0)
		rel = next
	}
	return cands
}

// exifFromJPEG reads the EXIF block in a JPEG's APP1 segment.
func exifFromJPEG(buf []byte) (Meta, bool) {
	var meta Meta
	p := 2
	for p+4 <= len(buf) {
		if buf[p] != 0xFF {
			return meta, false
		}
		m := buf[p+1]
		if m == 0xDA || m == 0xD9 {
			return meta, false
		}
		segLen := int(binary.BigEndian.Uint16(buf[p+2 : p+4]))
		if segLen < 2 || p+2+segLen > len(buf) {
			return meta, false
		}
		seg := buf[p+4 : p+2+segLen]
		if m == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			tiff := seg[6:]
			r := bytes.NewReader(tiff)
			t, first, ok := newTIFF(r, 0, int64(len(tiff)))
			if !ok {
				return meta, false
			}
			var cands []candidate
			entries, _ := t.readIFD(first)
			t.visitEntries(entries, kindIFD0, &meta, &cands, 0)
			return meta, true
		}
		p += 2 + segLen
	}
	return meta, false
}
