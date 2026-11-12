package transcode

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// MKV Cues reader: the keyframe index a Matroska muxer writes (mkvmerge,
// ffmpeg, MakeMKV, HandBrake), read without touching the clusters. A VOD
// remux plan needs the source's video keyframes up front, and scanning
// packets with ffprobe reads the whole file (minutes for a 40 GB remux on a
// NAS); the Cues element is a few hundred KB near the end of the file and
// says where every indexed keyframe is.
//
// Only the elements this needs are parsed: the Segment's SeekHead (where
// the Cues and Tracks are), Info (TimestampScale), Tracks (which track is
// video) and Cues (CueTime per CueTrackPositions for that track). Anything
// malformed or missing is an error, and the caller falls back to the
// growing EVENT remux.
//
// A muxer is free to index only some keyframes (one per cluster is common).
// That is fine for the plan, which only ever cuts at indexed keyframes; the
// segment handler maps segments to the pieces ffmpeg actually wrote, so
// unindexed keyframes in between are harmless.

// EBML element IDs (with their length-marker bits, as they appear on disk).
const (
	mkvIDEBML           = 0x1A45DFA3
	mkvIDSegment        = 0x18538067
	mkvIDSeekHead       = 0x114D9B74
	mkvIDSeek           = 0x4DBB
	mkvIDSeekID         = 0x53AB
	mkvIDSeekPosition   = 0x53AC
	mkvIDInfo           = 0x1549A966
	mkvIDTimestampScale = 0x2AD7B1
	mkvIDTracks         = 0x1654AE6B
	mkvIDTrackEntry     = 0xAE
	mkvIDTrackNumber    = 0xD7
	mkvIDTrackType      = 0x83
	mkvIDCues           = 0x1C53BB6B
	mkvIDCuePoint       = 0xBB
	mkvIDCueTime        = 0xB3
	mkvIDCueTrackPos    = 0xB7
	mkvIDCueTrack       = 0xF7
	mkvIDCluster        = 0x1F43B675
	mkvTrackTypeVideo   = 1
	mkvDefaultTSScale   = 1_000_000 // ns per timestamp unit (1 ms)
	mkvMaxIndexElement  = 64 << 20  // refuse a Cues/Tracks/SeekHead bigger than this
	mkvMaxTopLevelScan  = 4096      // top-level elements walked looking for Cues
	mkvUnknownSize      = -1
)

// ErrNoMKVCues means the file is Matroska but has no usable video index
// (no Cues element, or none for the video track).
var ErrNoMKVCues = errors.New("mkv: no video cues")

// MKVVideoKeyframes returns the indexed video keyframe times (seconds,
// ascending, deduplicated) of the Matroska/WebM file at path.
func MKVVideoKeyframes(path string) ([]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return ReadMKVVideoKeyframes(f, st.Size())
}

// ReadMKVVideoKeyframes is MKVVideoKeyframes over any ReaderAt of the given
// size (tests build the bytes in memory).
func ReadMKVVideoKeyframes(r io.ReaderAt, size int64) ([]float64, error) {
	p := mkvParser{r: r, size: size}
	return p.videoKeyframes()
}

type mkvParser struct {
	r    io.ReaderAt
	size int64
}

type mkvElem struct {
	id       uint32
	dataPos  int64 // first byte of the element's data
	dataSize int64 // mkvUnknownSize when the size field is all ones
}

func (e mkvElem) end() int64 { return e.dataPos + e.dataSize }

// readVint reads an EBML variable-length integer at pos. keepMarker keeps the
// length-marker bit (element IDs) instead of masking it (sizes). It returns
// the value, its byte length, and whether every value bit was 1 (the
// "unknown size" encoding).
func (p *mkvParser) readVint(pos int64, keepMarker bool) (uint64, int, bool, error) {
	var first [1]byte
	if _, err := p.r.ReadAt(first[:], pos); err != nil {
		return 0, 0, false, err
	}
	b := first[0]
	if b == 0 {
		return 0, 0, false, fmt.Errorf("mkv: invalid vint at %d", pos)
	}
	n := 1
	for mask := byte(0x80); b&mask == 0; mask >>= 1 {
		n++
	}
	if n > 8 {
		return 0, 0, false, fmt.Errorf("mkv: vint too long at %d", pos)
	}
	buf := make([]byte, n)
	if _, err := p.r.ReadAt(buf, pos); err != nil {
		return 0, 0, false, err
	}
	val := uint64(buf[0])
	if !keepMarker {
		val &= uint64(0xFF >> n)
	}
	allOnes := val == uint64(0xFF>>n)
	for _, c := range buf[1:] {
		val = val<<8 | uint64(c)
		if c != 0xFF {
			allOnes = false
		}
	}
	return val, n, allOnes, nil
}

func (p *mkvParser) readElem(pos int64) (mkvElem, error) {
	id, idLen, _, err := p.readVint(pos, true)
	if err != nil {
		return mkvElem{}, err
	}
	if idLen > 4 {
		return mkvElem{}, fmt.Errorf("mkv: element id too long at %d", pos)
	}
	sz, szLen, unknown, err := p.readVint(pos+int64(idLen), false)
	if err != nil {
		return mkvElem{}, err
	}
	e := mkvElem{id: uint32(id), dataPos: pos + int64(idLen) + int64(szLen)}
	if unknown {
		e.dataSize = mkvUnknownSize
	} else {
		if sz > uint64(p.size) {
			return mkvElem{}, fmt.Errorf("mkv: element size %d past end of file", sz)
		}
		e.dataSize = int64(sz)
	}
	return e, nil
}

// children parses the direct children of a fully-read master element body.
func mkvChildren(body []byte) ([]struct {
	id   uint32
	data []byte
}, error) {
	var out []struct {
		id   uint32
		data []byte
	}
	br := &mkvParser{r: bytesReaderAt(body), size: int64(len(body))}
	for pos := int64(0); pos < int64(len(body)); {
		e, err := br.readElem(pos)
		if err != nil {
			return nil, err
		}
		if e.dataSize == mkvUnknownSize || e.end() > int64(len(body)) {
			return nil, fmt.Errorf("mkv: child overruns its parent")
		}
		out = append(out, struct {
			id   uint32
			data []byte
		}{e.id, body[e.dataPos:e.end()]})
		pos = e.end()
	}
	return out, nil
}

func mkvUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

func (p *mkvParser) readBody(e mkvElem) ([]byte, error) {
	if e.dataSize == mkvUnknownSize || e.dataSize > mkvMaxIndexElement {
		return nil, fmt.Errorf("mkv: element 0x%X too large or unsized", e.id)
	}
	buf := make([]byte, e.dataSize)
	if _, err := p.r.ReadAt(buf, e.dataPos); err != nil {
		return nil, err
	}
	return buf, nil
}

func (p *mkvParser) videoKeyframes() ([]float64, error) {
	hdr, err := p.readElem(0)
	if err != nil {
		return nil, err
	}
	if hdr.id != mkvIDEBML || hdr.dataSize == mkvUnknownSize {
		return nil, errors.New("mkv: not an EBML file")
	}
	seg, err := p.readElem(hdr.end())
	if err != nil {
		return nil, err
	}
	if seg.id != mkvIDSegment {
		return nil, errors.New("mkv: no Segment")
	}
	segEnd := p.size
	if seg.dataSize != mkvUnknownSize && seg.end() < segEnd {
		segEnd = seg.end()
	}

	// Walk the Segment's top-level children: SeekHead / Info / Tracks come
	// before the first Cluster in every muxer; the SeekHead says where the
	// Cues are (usually the end of the file). Without a SeekHead entry,
	// keep walking, skipping sized Clusters, until the Cues turn up.
	var (
		tsScale  uint64 = mkvDefaultTSScale
		tracks   []byte
		cues     []byte
		seekPos        = map[uint32]int64{}
		tracksAt int64 = -1
		cuesAt   int64 = -1
	)
	pos := seg.dataPos
	for i := 0; pos < segEnd && i < mkvMaxTopLevelScan; i++ {
		e, err := p.readElem(pos)
		if err != nil {
			break
		}
		switch e.id {
		case mkvIDSeekHead:
			body, err := p.readBody(e)
			if err != nil {
				return nil, err
			}
			if err := mkvParseSeekHead(body, seg.dataPos, seekPos); err != nil {
				return nil, err
			}
		case mkvIDInfo:
			body, err := p.readBody(e)
			if err != nil {
				return nil, err
			}
			if s := mkvInfoTimestampScale(body); s > 0 {
				tsScale = s
			}
		case mkvIDTracks:
			if tracks, err = p.readBody(e); err != nil {
				return nil, err
			}
			tracksAt = pos
		case mkvIDCues:
			if cues, err = p.readBody(e); err != nil {
				return nil, err
			}
			cuesAt = pos
		}
		if tracks != nil && cues != nil {
			break
		}
		// Past the header elements: jump straight to what the SeekHead
		// points at instead of walking every Cluster.
		if e.id == mkvIDCluster {
			// A SeekHead that points at a second SeekHead (written at the
			// end, next to the Cues) — follow it once.
			if at, ok := seekPos[mkvIDSeekHead]; ok && at != pos {
				delete(seekPos, mkvIDSeekHead)
				if se, err := p.readElem(at); err == nil && se.id == mkvIDSeekHead {
					if body, err := p.readBody(se); err == nil {
						_ = mkvParseSeekHead(body, seg.dataPos, seekPos)
					}
				}
			}
			if tracks == nil {
				if at, ok := seekPos[mkvIDTracks]; ok && at != tracksAt {
					if te, err := p.readElem(at); err == nil && te.id == mkvIDTracks {
						if tracks, err = p.readBody(te); err != nil {
							return nil, err
						}
						tracksAt = at
					}
				}
			}
			if cues == nil {
				if at, ok := seekPos[mkvIDCues]; ok && at != cuesAt {
					if ce, err := p.readElem(at); err == nil && ce.id == mkvIDCues {
						if cues, err = p.readBody(ce); err != nil {
							return nil, err
						}
						cuesAt = at
					}
				}
			}
			if tracks != nil && cues != nil {
				break
			}
		}
		if e.dataSize == mkvUnknownSize {
			break // a live-written cluster: can't skip it
		}
		pos = e.end()
	}
	if tracks == nil {
		return nil, errors.New("mkv: no Tracks")
	}
	if cues == nil {
		return nil, ErrNoMKVCues
	}
	video, err := mkvVideoTrackNumber(tracks)
	if err != nil {
		return nil, err
	}
	times, err := mkvCueTimes(cues, video)
	if err != nil {
		return nil, err
	}
	if len(times) == 0 {
		return nil, ErrNoMKVCues
	}
	out := make([]float64, 0, len(times))
	for _, t := range times {
		out = append(out, float64(t)*float64(tsScale)/1e9)
	}
	sort.Float64s(out)
	dedup := out[:0]
	for i, t := range out {
		if i == 0 || t != dedup[len(dedup)-1] {
			dedup = append(dedup, t)
		}
	}
	return dedup, nil
}

func mkvParseSeekHead(body []byte, segData int64, into map[uint32]int64) error {
	kids, err := mkvChildren(body)
	if err != nil {
		return err
	}
	for _, k := range kids {
		if k.id != mkvIDSeek {
			continue
		}
		fields, err := mkvChildren(k.data)
		if err != nil {
			return err
		}
		var id uint32
		pos := int64(-1)
		for _, f := range fields {
			switch f.id {
			case mkvIDSeekID:
				id = uint32(mkvUint(f.data))
			case mkvIDSeekPosition:
				pos = int64(mkvUint(f.data))
			}
		}
		if id != 0 && pos >= 0 {
			if _, dup := into[id]; !dup {
				into[id] = segData + pos
			}
		}
	}
	return nil
}

func mkvInfoTimestampScale(body []byte) uint64 {
	kids, err := mkvChildren(body)
	if err != nil {
		return 0
	}
	for _, k := range kids {
		if k.id == mkvIDTimestampScale {
			return mkvUint(k.data)
		}
	}
	return 0
}

// mkvVideoTrackNumber returns the TrackNumber of the first video track.
func mkvVideoTrackNumber(tracks []byte) (uint64, error) {
	kids, err := mkvChildren(tracks)
	if err != nil {
		return 0, err
	}
	for _, k := range kids {
		if k.id != mkvIDTrackEntry {
			continue
		}
		fields, err := mkvChildren(k.data)
		if err != nil {
			return 0, err
		}
		var num, typ uint64
		for _, f := range fields {
			switch f.id {
			case mkvIDTrackNumber:
				num = mkvUint(f.data)
			case mkvIDTrackType:
				typ = mkvUint(f.data)
			}
		}
		if typ == mkvTrackTypeVideo && num > 0 {
			return num, nil
		}
	}
	return 0, errors.New("mkv: no video track")
}

// mkvCueTimes returns the CueTime (in TimestampScale units) of every
// CuePoint that indexes the given track.
func mkvCueTimes(cues []byte, track uint64) ([]uint64, error) {
	points, err := mkvChildren(cues)
	if err != nil {
		return nil, err
	}
	var out []uint64
	for _, cp := range points {
		if cp.id != mkvIDCuePoint {
			continue
		}
		fields, err := mkvChildren(cp.data)
		if err != nil {
			return nil, err
		}
		var (
			t       uint64
			hasTime bool
			forUs   bool
		)
		for _, f := range fields {
			switch f.id {
			case mkvIDCueTime:
				t, hasTime = mkvUint(f.data), true
			case mkvIDCueTrackPos:
				pos, err := mkvChildren(f.data)
				if err != nil {
					return nil, err
				}
				for _, pf := range pos {
					if pf.id == mkvIDCueTrack && mkvUint(pf.data) == track {
						forUs = true
					}
				}
			}
		}
		if hasTime && forUs {
			out = append(out, t)
		}
	}
	return out, nil
}

// bytesReaderAt adapts a byte slice to io.ReaderAt.
type bytesReaderAt []byte

func (b bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
