package rawpreview

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"io"
)

// Canon CR3 is ISO-BMFF. The parts used here:
//
//	moov / uuid 85c0b687-820f-11e0-8111-f4ce462b6a48
//	    CMT1  TIFF: IFD0 (make, model, orientation)
//	    CMT2  TIFF: EXIF IFD (date, exposure, lens)
//	    CMT4  TIFF: GPS IFD
//	    THMB  160x120 JPEG thumbnail
//	uuid eaf42b5e-1c98-4b88-b9fb-b7dc406e4d16
//	    PRVW  1620x1080 JPEG preview
//
// The full-size JPEG lives in the first track's sample data; the carve in
// Extract finds it when wanted.
var (
	cr3CanonUUID   = mustHex("85c0b687820f11e08111f4ce462b6a48")
	cr3PreviewUUID = mustHex("eaf42b5e1c984b88b9fbb7dc406e4d16")
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

type bmffBox struct {
	typ            string
	start, payload int64 // absolute box start and payload start
	end            int64 // absolute end (exclusive)
}

// readBoxes lists the boxes in [start, end).
func readBoxes(r io.ReaderAt, start, end int64, budget *int) []bmffBox {
	var out []bmffBox
	p := start
	for p+8 <= end && *budget > 0 {
		*budget--
		var h [16]byte
		if _, err := r.ReadAt(h[:8], p); err != nil {
			break
		}
		size := int64(binary.BigEndian.Uint32(h[0:4]))
		typ := string(h[4:8])
		hdr := int64(8)
		switch size {
		case 0:
			size = end - p
		case 1:
			if p+16 > end {
				return out
			}
			if _, err := r.ReadAt(h[8:16], p+8); err != nil {
				return out
			}
			s := binary.BigEndian.Uint64(h[8:16])
			if s > uint64(end-p) {
				return out
			}
			size = int64(s)
			hdr = 16
		}
		if size < hdr || size > end-p {
			break
		}
		out = append(out, bmffBox{typ: typ, start: p, payload: p + hdr, end: p + size})
		p += size
	}
	return out
}

// uuidOf reads the 16-byte user type of a "uuid" box and returns the box
// with its payload advanced past it.
func uuidOf(r io.ReaderAt, b bmffBox) ([]byte, bmffBox, bool) {
	if b.typ != "uuid" || b.payload+16 > b.end {
		return nil, b, false
	}
	id := make([]byte, 16)
	if _, err := r.ReadAt(id, b.payload); err != nil {
		return nil, b, false
	}
	b.payload += 16
	return id, b, true
}

func extractCR3(r io.ReaderAt, size int64, meta *Meta) []candidate {
	budget := maxBoxes
	var cands []candidate
	for _, top := range readBoxes(r, 0, size, &budget) {
		switch top.typ {
		case "moov":
			for _, b := range readBoxes(r, top.payload, top.end, &budget) {
				id, ub, ok := uuidOf(r, b)
				if !ok || !bytes.Equal(id, cr3CanonUUID) {
					continue
				}
				for _, c := range readBoxes(r, ub.payload, ub.end, &budget) {
					switch c.typ {
					case "CMT1", "CMT2", "CMT4":
						cr3Metadata(r, c, meta)
					case "THMB":
						if cand, ok := jpegInBox(r, c); ok {
							cands = append(cands, cand)
						}
					}
				}
			}
		case "uuid":
			id, ub, ok := uuidOf(r, top)
			if !ok || !bytes.Equal(id, cr3PreviewUUID) {
				continue
			}
			// The preview uuid carries 8 bytes of its own header before
			// the PRVW box.
			for _, c := range readBoxes(r, ub.payload+8, ub.end, &budget) {
				if c.typ == "PRVW" {
					if cand, ok := jpegInBox(r, c); ok {
						cands = append(cands, cand)
					}
				}
			}
		}
	}
	return cands
}

// cr3Metadata reads one CMT box: each is a standalone TIFF holding a single
// IFD of the named kind.
func cr3Metadata(r io.ReaderAt, b bmffBox, meta *Meta) {
	t, first, ok := newTIFF(r, b.payload, b.end)
	if !ok {
		return
	}
	entries, _ := t.readIFD(first)
	kind := kindIFD0
	switch b.typ {
	case "CMT2":
		kind = kindExif
	case "CMT4":
		kind = kindGPS
	}
	// Pointers inside CMT boxes (ExifIFD in CMT1) would point outside the
	// box; the other CMT boxes carry those IFDs, so don't follow them.
	var cands []candidate
	filtered := entries[:0]
	for _, e := range entries {
		if e.tag == tagExifIFD || e.tag == tagGPSIFD || e.tag == tagSubIFDs {
			continue
		}
		filtered = append(filtered, e)
	}
	t.visitEntries(filtered, kind, meta, &cands, 0)
}

// jpegInBox locates the JPEG stream inside a THMB / PRVW box. The boxes
// carry a few header fields (version, dimensions, length) before the JPEG;
// rather than trusting those, find the SOI in the first bytes and walk the
// JPEG structure to its end, bounded by the box.
func jpegInBox(r io.ReaderAt, b bmffBox) (candidate, bool) {
	n := b.end - b.payload
	if n <= 0 || n > MaxPreviewBytes {
		return candidate{}, false
	}
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, b.payload); err != nil && err != io.EOF {
		return candidate{}, false
	}
	head := buf
	if len(head) > 64 {
		head = head[:64]
	}
	i := bytes.Index(head, soi)
	if i < 0 {
		return candidate{}, false
	}
	end, ok := jpegEnd(buf, i)
	if !ok {
		return candidate{}, false
	}
	return candidate{off: b.payload + int64(i), n: int64(end - i)}, true
}
