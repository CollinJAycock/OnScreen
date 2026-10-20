package trickplay

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
)

// BIF ("Base Index Frames") is the seek-thumbnail container Roku firmware
// renders natively on the Video node's trick-play bar (content HDBifUrl /
// SDBifUrl / FHDBifUrl). OnScreen doesn't generate it alongside the sprites:
// the first request for an item cuts the frames out of the existing sprite
// sheets (using the VTT's #xywh cues) and caches the result next to them as
// BIFFileName. A regeneration wipes the item directory, cache included.
//
// Layout (all integers little-endian uint32):
//
//	0   magic 89 42 49 46 0d 0a 1a 0a
//	8   version (0)
//	12  number of images
//	16  timestamp multiplier in ms (one index unit = this many ms)
//	20  reserved, zero, up to offset 64
//	64  index: (timestamp, absolute offset) per image, then a terminator
//	    entry (0xffffffff, offset of the end of the last image)
//	…   the JPEG frames back to back

// BIFFileName is the cached BIF's name inside an item's trickplay directory.
const BIFFileName = "index.bif"

// BIFMaxWidth is the widest frame written into a BIF. Roku's guideline for
// HD trick-play thumbnails is 320 px wide (16:9 → 320x180), which is also
// the default sprite thumbnail width, so frames are normally copied 1:1;
// a wider custom spec is scaled down to this.
const BIFMaxWidth = 320

// bifJPEGQuality is the re-encode quality for frames cut from a sprite.
const bifJPEGQuality = 85

var bifMagic = [8]byte{0x89, 0x42, 0x49, 0x46, 0x0d, 0x0a, 0x1a, 0x0a}

const (
	bifHeaderSize = 64
	bifIndexEnd   = 0xffffffff
)

// BIFFrame is one image in a BIF: its position on the index timeline (in
// units of the file's timestamp multiplier) and its JPEG bytes.
type BIFFrame struct {
	Timestamp uint32
	JPEG      []byte
}

// WriteBIF writes a BIF holding frames to w. multiplierMS is the duration of
// one timestamp unit (the thumbnail interval, so frame i has timestamp i).
// Timestamps must strictly increase — the firmware binary-searches the index.
func WriteBIF(w io.Writer, multiplierMS uint32, frames []BIFFrame) error {
	if multiplierMS == 0 {
		return errors.New("trickplay: bif timestamp multiplier must be > 0")
	}
	if len(frames) == 0 {
		return errors.New("trickplay: bif needs at least one frame")
	}
	for i := 1; i < len(frames); i++ {
		if frames[i].Timestamp <= frames[i-1].Timestamp {
			return fmt.Errorf("trickplay: bif timestamps not increasing at frame %d", i)
		}
	}
	if frames[len(frames)-1].Timestamp == bifIndexEnd {
		return errors.New("trickplay: bif timestamp collides with the index terminator")
	}

	indexSize := uint64(len(frames)+1) * 8
	total := uint64(bifHeaderSize) + indexSize
	for _, f := range frames {
		if len(f.JPEG) == 0 {
			return errors.New("trickplay: bif frame has no image data")
		}
		total += uint64(len(f.JPEG))
	}
	if total > 0xffffffff {
		return errors.New("trickplay: bif larger than 4 GiB")
	}

	hdr := make([]byte, bifHeaderSize, bifHeaderSize+indexSize)
	copy(hdr, bifMagic[:])
	binary.LittleEndian.PutUint32(hdr[8:], 0) // version
	binary.LittleEndian.PutUint32(hdr[12:], uint32(len(frames)))
	binary.LittleEndian.PutUint32(hdr[16:], multiplierMS)
	// 20..63 reserved: already zero.

	offset := uint32(bifHeaderSize + indexSize)
	var entry [8]byte
	for _, f := range frames {
		binary.LittleEndian.PutUint32(entry[0:], f.Timestamp)
		binary.LittleEndian.PutUint32(entry[4:], offset)
		hdr = append(hdr, entry[:]...)
		offset += uint32(len(f.JPEG))
	}
	binary.LittleEndian.PutUint32(entry[0:], bifIndexEnd)
	binary.LittleEndian.PutUint32(entry[4:], offset)
	hdr = append(hdr, entry[:]...)

	if _, err := w.Write(hdr); err != nil {
		return err
	}
	for _, f := range frames {
		if _, err := w.Write(f.JPEG); err != nil {
			return err
		}
	}
	return nil
}

// VTTCue is one cue of a sprite-sheet trickplay index: the time range and
// the region of a sprite sheet (relative file name + #xywh) it shows.
type VTTCue struct {
	Start, End time.Duration
	Sprite     string
	Rect       image.Rectangle
}

// ParseVTT reads a trickplay WebVTT index as WriteVTT writes it. Cues
// without an #xywh fragment are skipped; a malformed timing or fragment is
// an error (the file is ours, so damage means a partial write).
func ParseVTT(data []byte) ([]VTTCue, error) {
	var cues []VTTCue
	sc := bufio.NewScanner(bytes.NewReader(data))
	var (
		pending    bool
		start, end time.Duration
	)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			pending = false
		case strings.Contains(line, "-->"):
			l, r, _ := strings.Cut(line, "-->")
			// The end time may be followed by cue settings.
			rf := strings.Fields(r)
			if len(rf) == 0 {
				return nil, fmt.Errorf("trickplay: vtt cue timing %q", line)
			}
			var err error
			if start, err = parseVTTTime(strings.TrimSpace(l)); err != nil {
				return nil, err
			}
			if end, err = parseVTTTime(rf[0]); err != nil {
				return nil, err
			}
			pending = true
		case pending:
			pending = false
			name, frag, ok := strings.Cut(line, "#xywh=")
			if !ok {
				continue
			}
			parts := strings.Split(frag, ",")
			if len(parts) != 4 {
				return nil, fmt.Errorf("trickplay: vtt cue fragment %q", line)
			}
			var v [4]int
			for i, p := range parts {
				n, err := strconv.Atoi(strings.TrimSpace(p))
				if err != nil || n < 0 {
					return nil, fmt.Errorf("trickplay: vtt cue fragment %q", line)
				}
				v[i] = n
			}
			if v[2] == 0 || v[3] == 0 {
				return nil, fmt.Errorf("trickplay: vtt cue fragment %q has an empty region", line)
			}
			cues = append(cues, VTTCue{
				Start:  start,
				End:    end,
				Sprite: name,
				Rect:   image.Rect(v[0], v[1], v[0]+v[2], v[1]+v[3]),
			})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return cues, nil
}

// parseVTTTime parses HH:MM:SS.mmm or MM:SS.mmm.
func parseVTTTime(s string) (time.Duration, error) {
	bad := fmt.Errorf("trickplay: vtt timestamp %q", s)
	clock, frac, _ := strings.Cut(s, ".")
	fields := strings.Split(clock, ":")
	if len(fields) < 2 || len(fields) > 3 {
		return 0, bad
	}
	var total time.Duration
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return 0, bad
		}
		total = total*60 + time.Duration(n)*time.Second
	}
	if frac != "" {
		if len(frac) > 3 {
			return 0, bad
		}
		ms, err := strconv.Atoi(frac + strings.Repeat("0", 3-len(frac)))
		if err != nil || ms < 0 {
			return 0, bad
		}
		total += time.Duration(ms) * time.Millisecond
	}
	return total, nil
}

// spriteNamePattern guards the sprite references read out of the VTT: only a
// sprite sheet in the same directory is ever opened.
var spriteNamePattern = regexp.MustCompile(`^sprite_\d{3}\.jpg$`)

// SliceSprites cuts each cue's region out of its sprite sheet in dir and
// returns the frames as JPEGs, one per cue, scaled down to maxWidth when
// wider (aspect kept). Each sheet is decoded once. A region that lies
// entirely outside its sheet ends the list there (a short final sheet) —
// the frames already cut stay valid.
func SliceSprites(dir string, cues []VTTCue, maxWidth int) ([][]byte, error) {
	var (
		curName string
		curImg  image.Image
		out     = make([][]byte, 0, len(cues))
		buf     bytes.Buffer
	)
	for _, c := range cues {
		if !spriteNamePattern.MatchString(c.Sprite) {
			return nil, fmt.Errorf("trickplay: vtt names unexpected sprite %q", c.Sprite)
		}
		if c.Sprite != curName {
			img, err := decodeJPEGFile(filepath.Join(dir, c.Sprite))
			if err != nil {
				return nil, err
			}
			curName, curImg = c.Sprite, img
		}
		r := c.Rect.Add(curImg.Bounds().Min).Intersect(curImg.Bounds())
		if r.Empty() {
			break
		}
		frame := cropImage(curImg, r)
		if maxWidth > 0 && r.Dx() > maxWidth {
			h := (r.Dy()*maxWidth + r.Dx()/2) / r.Dx()
			if h < 1 {
				h = 1
			}
			dst := image.NewRGBA(image.Rect(0, 0, maxWidth, h))
			xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), frame, frame.Bounds(), xdraw.Src, nil)
			frame = dst
		}
		buf.Reset()
		if err := jpeg.Encode(&buf, frame, &jpeg.Options{Quality: bifJPEGQuality}); err != nil {
			return nil, fmt.Errorf("trickplay: encode bif frame: %w", err)
		}
		out = append(out, bytes.Clone(buf.Bytes()))
	}
	return out, nil
}

func decodeJPEGFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := jpeg.Decode(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("trickplay: decode %s: %w", filepath.Base(path), err)
	}
	return img, nil
}

// cropImage returns the r region of img. Every type image/jpeg decodes to
// supports SubImage; the fallback copies.
func cropImage(img image.Image, r image.Rectangle) image.Image {
	if s, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	xdraw.Copy(dst, image.Point{}, img, r, xdraw.Src, nil)
	return dst
}

// BuildBIF assembles a BIF from the sprite sheets + index.vtt in dir and
// writes it to w. The thumbnail interval (the BIF timestamp multiplier) is
// read off the cues, so it follows whatever spec generated the sprites.
func BuildBIF(dir string, w io.Writer) error {
	data, err := os.ReadFile(filepath.Join(dir, "index.vtt"))
	if err != nil {
		return err
	}
	cues, err := ParseVTT(data)
	if err != nil {
		return err
	}
	if len(cues) == 0 {
		return errors.New("trickplay: vtt has no cues")
	}
	interval := cues[0].End - cues[0].Start
	if len(cues) > 1 {
		interval = cues[1].Start - cues[0].Start
	}
	intervalMS := interval.Milliseconds()
	if intervalMS <= 0 || intervalMS > 0xffffffff {
		return fmt.Errorf("trickplay: vtt interval %s unusable for bif", interval)
	}
	jpegs, err := SliceSprites(dir, cues, BIFMaxWidth)
	if err != nil {
		return err
	}
	frames := make([]BIFFrame, 0, len(jpegs))
	for i, j := range jpegs {
		// Nearest index unit; the generator's cues sit exactly on it.
		ts := (cues[i].Start.Milliseconds() + intervalMS/2) / intervalMS
		if len(frames) > 0 && uint32(ts) <= frames[len(frames)-1].Timestamp {
			return fmt.Errorf("trickplay: vtt cues not evenly spaced at cue %d", i)
		}
		frames = append(frames, BIFFrame{Timestamp: uint32(ts), JPEG: j})
	}
	if len(frames) == 0 {
		return errors.New("trickplay: no frames could be cut from the sprites")
	}
	return WriteBIF(w, uint32(intervalMS), frames)
}

// ErrBIFStale is returned by EnsureBIF when the item's sprites were
// regenerated while its BIF was being built; the half-old result is
// discarded. Callers treat it like "not generated yet".
var ErrBIFStale = errors.New("trickplay: sprites changed while building bif")

// EnsureBIF returns the path of dir's cached BIF, building it from the
// sprites first when it's missing or older than index.vtt. A missing VTT
// (no sprites yet, or a generation in progress — the VTT is written last)
// comes back as an fs.ErrNotExist error. The file is written through a
// temp file + rename, so concurrent readers (other API instances sharing
// the cache volume) never see a partial BIF. Callers should single-flight
// per item; two concurrent builds are wasteful but still correct.
func EnsureBIF(dir string) (string, error) {
	vttPath := filepath.Join(dir, "index.vtt")
	before, err := os.Stat(vttPath)
	if err != nil {
		return "", err
	}
	out := filepath.Join(dir, BIFFileName)
	if fi, err := os.Stat(out); err == nil && fi.Size() > 0 && !fi.ModTime().Before(before.ModTime()) {
		return out, nil
	}

	tmp, err := os.CreateTemp(dir, ".index-*.bif.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	bw := bufio.NewWriterSize(tmp, 256<<10)
	if err := BuildBIF(dir, bw); err != nil {
		tmp.Close()
		return "", err
	}
	if err := bw.Flush(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, out); err != nil {
		return "", err
	}

	// A regeneration that finished while we were cutting frames left a new
	// VTT (or none yet) behind: what we just wrote mixes old frames into the
	// new set's cache slot. Drop it; the next request builds a fresh one.
	after, err := os.Stat(vttPath)
	if err != nil || !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		_ = os.Remove(out)
		return "", ErrBIFStale
	}
	return out, nil
}
