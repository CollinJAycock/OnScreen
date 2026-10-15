// Package rawpreview reads camera RAW files (CR2, CR3, NEF, ARW, DNG, RAF,
// ORF, RW2, PEF) without decoding the sensor data.
//
// Virtually every RAW carries a JPEG rendering of itself made by the camera:
// a full-size or near-full-size preview plus a small thumbnail. This package
// finds those in pure Go — TIFF/IFD walking for the TIFF-shaped formats, the
// ISO-BMFF PRVW / THMB boxes for CR3, the header offset for RAF, and a
// bounded JPEG carve for formats that hide the preview in a maker note (ORF,
// PEF) — and reads the EXIF fields the photo library shows (date, camera,
// lens, exposure, GPS, orientation).
//
// RAW files are untrusted input. Every offset is checked against the file
// size before it is read, every count is capped, IFD chains are walked with a
// visited set, and the total bytes read are bounded, so a hostile file can
// neither make us allocate without limit nor loop.
package rawpreview

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"path/filepath"
	"strings"
	"sync"
)

// extensions is the set of RAW formats recognised in photo libraries.
var extensions = map[string]bool{
	".cr2": true, ".cr3": true, ".nef": true, ".arw": true, ".dng": true,
	".raf": true, ".orf": true, ".rw2": true, ".pef": true,
}

// Extensions returns the recognised RAW extensions (lower case, with dot).
func Extensions() []string {
	out := make([]string, 0, len(extensions))
	for e := range extensions {
		out = append(out, e)
	}
	return out
}

// IsRAW reports whether path has a recognised RAW extension.
func IsRAW(path string) bool {
	return extensions[strings.ToLower(filepath.Ext(path))]
}

// Limits on what a single file may make us read or allocate.
const (
	// MaxPreviewBytes caps one embedded JPEG. A 60 MP camera's full-size
	// preview at high quality is ~25 MB.
	MaxPreviewBytes = 48 << 20
	// MaxPreviewPixels caps a preview's declared dimensions, matching the
	// photo image server's in-process decode ceiling.
	MaxPreviewPixels = 100_000_000
	// maxCarveBytes bounds the prefix searched for JPEG streams when the
	// structure gives no usable preview.
	maxCarveBytes = 64 << 20
	// maxCarveJPEGs bounds how many JPEG streams one carve validates.
	maxCarveJPEGs = 8
	// maxIFDs bounds the IFDs visited in one file (chain + sub-IFDs).
	maxIFDs = 64
	// maxIFDEntries bounds the entries read from one IFD.
	maxIFDEntries = 1024
	// maxValueBytes caps a single tag value read (strings, arrays).
	maxValueBytes = 64 << 10
	// maxBoxes bounds the ISO-BMFF boxes visited for CR3.
	maxBoxes = 512
	// minGoodLongEdge is the long edge below which a structural preview is
	// treated as a thumbnail and the carve runs to look for a larger one.
	minGoodLongEdge = 1280
)

// ErrNoPreview is returned when a file carries no embedded JPEG we can use.
var ErrNoPreview = errors.New("rawpreview: no embedded preview")

// ErrNotRAW is returned for data that isn't a recognised RAW container.
var ErrNotRAW = errors.New("rawpreview: unrecognised RAW container")

// Preview is the embedded JPEG chosen for display. ExtractMetadata leaves
// JPEG nil: it reads only the preview's header, for the dimensions.
type Preview struct {
	JPEG   []byte
	Width  int
	Height int
}

// Meta is the EXIF subset the photo library uses. Absent fields stay zero /
// nil.
type Meta struct {
	Make, Model, LensModel string
	// DateTimeOriginal is the raw EXIF string ("2006:01:02 15:04:05"); the
	// scanner parses it with the same layouts as JPEG EXIF.
	DateTimeOriginal string
	FocalLength      *float64
	FNumber          *float64
	// ExposureNum / ExposureDen is ExposureTime as a fraction (0/0 = absent).
	ExposureNum, ExposureDen uint32
	ISO                      int
	Flash                    *int
	Orientation              int
	GPSLat, GPSLon, GPSAlt   *float64
}

// Result is what Extract finds in one file.
type Result struct {
	Format  string // "tiff", "cr3", "raf"
	Preview *Preview
	Meta    Meta
}

// Extract reads the preview and EXIF metadata from a RAW file. size is the
// file size; reads never go past it. A file with metadata but no usable
// preview returns a Result with a nil Preview and ErrNoPreview.
func Extract(r io.ReaderAt, size int64) (*Result, error) {
	return extract(r, size, true)
}

// ExtractMetadata is Extract without the carve: the EXIF fields plus the
// preview the container's structure names (which gives the dimensions). It
// never reads more than the structure points at, so the scanner can call it
// on every RAW cheaply.
func ExtractMetadata(r io.ReaderAt, size int64) (*Result, error) {
	return extract(r, size, false)
}

func extract(r io.ReaderAt, size int64, carve bool) (*Result, error) {
	if size < 16 {
		return nil, ErrNotRAW
	}
	head := make([]byte, 16)
	if _, err := r.ReadAt(head, 0); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("rawpreview: read header: %w", err)
	}
	res := &Result{}
	var cands []candidate
	switch {
	case bytes.HasPrefix(head, []byte("FUJIFILMCCD-RAW")):
		res.Format = "raf"
		cands = extractRAF(r, size, &res.Meta)
	case string(head[4:8]) == "ftyp" && string(head[8:12]) == "crx ":
		res.Format = "cr3"
		cands = extractCR3(r, size, &res.Meta)
	case isTIFFMagic(head):
		res.Format = "tiff"
		cands = extractTIFF(r, size, &res.Meta)
	default:
		return nil, ErrNotRAW
	}

	// Candidates are compared by their headers alone; only the winner is
	// read in full, so a file naming many (or huge) ranges costs a few
	// header reads, not a read of each range.
	var best *Preview
	var previewHead []byte
	if p := pickBest(r, size, cands); p != nil {
		previewHead = p.head
		if !carve {
			best = &Preview{Width: p.w, Height: p.h}
		} else {
			best = readCandidate(r, size, p.c)
		}
	}
	if carve && (best == nil || longEdge(best) < minGoodLongEdge) {
		if c := carveBest(r, size); c != nil && (best == nil || area(c) > area(best)) {
			best = c
			previewHead = c.JPEG
		}
	}
	if best == nil {
		return res, ErrNoPreview
	}
	res.Preview = best
	// A preview's own EXIF orientation only counts when the container
	// carried none (RAF keeps its EXIF inside the preview).
	if res.Meta.Orientation == 0 {
		if m, ok := exifFromJPEG(previewHead); ok {
			res.Meta.Orientation = m.Orientation
		}
	}
	return res, nil
}

// candidate is a byte range that might hold a JPEG.
type candidate struct {
	off, n int64
}

func longEdge(p *Preview) int {
	if p.Width > p.Height {
		return p.Width
	}
	return p.Height
}

func area(p *Preview) int64 { return int64(p.Width) * int64(p.Height) }

// maxProbedCandidates bounds the candidates whose headers one file may make
// us read.
const maxProbedCandidates = 32

// probed is a candidate whose JPEG header has been read and checked.
type probed struct {
	c    candidate
	w, h int
	head []byte // the bytes read: SOI, APP segments (EXIF), frame header
}

func (p *probed) area() int64 { return int64(p.w) * int64(p.h) }

// pickBest probes each candidate's header and returns the largest one that
// is a JPEG Go can decode.
func pickBest(r io.ReaderAt, size int64, cands []candidate) *probed {
	var best *probed
	seen := map[int64]bool{}
	tried := 0
	for _, c := range cands {
		if seen[c.off] || tried >= maxProbedCandidates {
			continue
		}
		seen[c.off] = true
		tried++
		if p := probeCandidate(r, size, c); p != nil && (best == nil || p.area() > best.area()) {
			best = p
		}
	}
	return best
}

// headerChunks are the prefix sizes probeCandidate reads to find a JPEG's
// frame header: most sit in the first few KB, a preview behind a large
// EXIF / maker-note APP segment needs more.
var headerChunks = []int64{256 << 10, 4 << 20}

// probeCandidate reads the start of a candidate range and checks it is a
// JPEG within the pixel ceiling, without reading the rest.
func probeCandidate(r io.ReaderAt, size int64, c candidate) *probed {
	if !candidateInFile(c, size) {
		return nil
	}
	for _, chunk := range headerChunks {
		n := chunk
		if n > c.n {
			n = c.n
		}
		buf := make([]byte, n)
		if _, err := r.ReadAt(buf, c.off); err != nil && !errors.Is(err, io.EOF) {
			return nil
		}
		if buf[0] != 0xFF || buf[1] != 0xD8 {
			return nil
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(buf))
		if err == nil {
			if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPreviewPixels {
				return nil
			}
			return &probed{c: c, w: cfg.Width, h: cfg.Height, head: buf}
		}
		if n == c.n {
			return nil
		}
	}
	return nil
}

func candidateInFile(c candidate, size int64) bool {
	return c.off > 0 && c.n >= 128 && c.n <= MaxPreviewBytes && c.off <= size && c.n <= size-c.off
}

// readCandidate reads and validates one candidate range in full.
func readCandidate(r io.ReaderAt, size int64, c candidate) *Preview {
	if !candidateInFile(c, size) {
		return nil
	}
	buf := make([]byte, c.n)
	if _, err := r.ReadAt(buf, c.off); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	return validateJPEG(buf)
}

// validateJPEG checks buf is a baseline/progressive JPEG Go can decode and
// within the pixel ceiling. Lossless JPEG (the raw data of many DNGs and
// CR2s) fails DecodeConfig and is rejected here.
func validateJPEG(buf []byte) *Preview {
	if len(buf) < 4 || buf[0] != 0xFF || buf[1] != 0xD8 {
		return nil
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(buf))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPreviewPixels {
		return nil
	}
	return &Preview{JPEG: buf, Width: cfg.Width, Height: cfg.Height}
}

// ── RAF ──────────────────────────────────────────────────────────────────

// extractRAF reads the Fujifilm header's JPEG pointer. The preview carries
// the camera's full EXIF, so metadata comes from it.
func extractRAF(r io.ReaderAt, size int64, meta *Meta) []candidate {
	var hdr [92]byte
	if size < int64(len(hdr)) {
		return nil
	}
	if _, err := r.ReadAt(hdr[:], 0); err != nil {
		return nil
	}
	off := int64(binary.BigEndian.Uint32(hdr[84:88]))
	n := int64(binary.BigEndian.Uint32(hdr[88:92]))
	c := candidate{off: off, n: n}
	if p := probeCandidate(r, size, c); p != nil {
		if m, ok := exifFromJPEG(p.head); ok {
			*meta = m
		}
	}
	return []candidate{c}
}

// ── carve ────────────────────────────────────────────────────────────────

var soi = []byte{0xFF, 0xD8, 0xFF}

// carveBest searches a bounded prefix of the file for complete JPEG streams
// and returns the largest valid one. Used when the container's structure
// names no usable preview (ORF and PEF keep theirs in maker notes; CR3's
// full-size JPEG sits in the first track's sample data).
func carveBest(r io.ReaderAt, size int64) *Preview {
	n := size
	if n > maxCarveBytes {
		n = maxCarveBytes
	}
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	var best *Preview
	found := 0
	for i := 0; i < len(buf) && found < maxCarveJPEGs; {
		j := bytes.Index(buf[i:], soi)
		if j < 0 {
			break
		}
		start := i + j
		end, ok := jpegEnd(buf, start)
		if !ok {
			i = start + len(soi)
			continue
		}
		if p := validateJPEG(buf[start:end]); p != nil {
			found++
			if best == nil || area(p) > area(best) {
				// Copy out so the 64 MB carve buffer can be released.
				p.JPEG = append([]byte(nil), p.JPEG...)
				best = p
			}
		}
		i = end
	}
	return best
}

// jpegEnd walks the JPEG marker structure starting at buf[start] (an SOI)
// and returns the offset just past its EOI. It requires a frame header
// before the first scan, which rejects the random FF D8 FF runs found in
// sensor data. Segment lengths come from the data, so every step is bounds
// checked; the entropy scan is a linear search, so the walk is O(n).
func jpegEnd(buf []byte, start int) (int, bool) {
	p := start + 2
	sawSOF := false
	segments := 0
	for p+1 < len(buf) {
		if buf[p] != 0xFF {
			return 0, false
		}
		// Fill bytes.
		for p+1 < len(buf) && buf[p+1] == 0xFF {
			p++
		}
		if p+1 >= len(buf) {
			return 0, false
		}
		m := buf[p+1]
		switch {
		case m == 0xD9:
			return p + 2, sawSOF
		case m == 0x01 || (m >= 0xD0 && m <= 0xD7):
			p += 2
			continue
		case m == 0x00 || m == 0xD8:
			return 0, false
		}
		if p+4 > len(buf) {
			return 0, false
		}
		segLen := int(binary.BigEndian.Uint16(buf[p+2 : p+4]))
		if segLen < 2 {
			return 0, false
		}
		segments++
		if segments > 256 {
			return 0, false
		}
		if m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC {
			sawSOF = true
		}
		next := p + 2 + segLen
		if next > len(buf) {
			return 0, false
		}
		if m != 0xDA {
			p = next
			continue
		}
		if !sawSOF {
			return 0, false
		}
		// Entropy-coded data follows SOS: skip to the next marker that
		// isn't a stuffed zero or a restart marker.
		q := next
		for {
			k := bytes.IndexByte(buf[q:], 0xFF)
			if k < 0 || q+k+1 >= len(buf) {
				return 0, false
			}
			q += k
			b := buf[q+1]
			if b == 0x00 || (b >= 0xD0 && b <= 0xD7) || b == 0xFF {
				q++
				continue
			}
			break
		}
		p = q
	}
	return 0, false
}

// ── helpers ──────────────────────────────────────────────────────────────

// seekReaderAt adapts an io.ReadSeeker (the media store's file handle) to
// io.ReaderAt. Reads are serialised; the scanner and image server read one
// file from one goroutine anyway.
type seekReaderAt struct {
	mu sync.Mutex
	rs io.ReadSeeker
}

func (s *seekReaderAt) ReadAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.rs.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	return io.ReadFull(s.rs, p)
}

// ReaderAt returns rs as an io.ReaderAt plus its size (found by seeking to
// the end).
func ReaderAt(rs io.ReadSeeker) (io.ReaderAt, int64, error) {
	size, err := rs.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, 0, err
	}
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}
	if ra, ok := rs.(io.ReaderAt); ok {
		return ra, size, nil
	}
	return &seekReaderAt{rs: rs}, size, nil
}
