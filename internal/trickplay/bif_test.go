package trickplay

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteBIF_ByteExact(t *testing.T) {
	frames := []BIFFrame{
		{Timestamp: 0, JPEG: []byte{0xFF, 0xD8, 0x01, 0xFF, 0xD9}},
		{Timestamp: 1, JPEG: []byte{0xFF, 0xD8, 0x02, 0x03, 0xFF, 0xD9}},
		{Timestamp: 2, JPEG: []byte{0xFF, 0xD8, 0xFF, 0xD9}},
	}
	var buf bytes.Buffer
	if err := WriteBIF(&buf, 10000, frames); err != nil {
		t.Fatal(err)
	}

	le := func(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }
	var want []byte
	want = append(want, 0x89, 0x42, 0x49, 0x46, 0x0d, 0x0a, 0x1a, 0x0a)
	want = append(want, le(0)...)     // version
	want = append(want, le(3)...)     // images
	want = append(want, le(10000)...) // multiplier
	want = append(want, make([]byte, 44)...)
	// Index: 64 + 4 entries * 8 = 96 is where the first frame starts.
	want = append(want, le(0)...)
	want = append(want, le(96)...)
	want = append(want, le(1)...)
	want = append(want, le(101)...)
	want = append(want, le(2)...)
	want = append(want, le(107)...)
	want = append(want, le(0xffffffff)...)
	want = append(want, le(111)...)
	for _, f := range frames {
		want = append(want, f.JPEG...)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("bif bytes mismatch\n got % x\nwant % x", buf.Bytes(), want)
	}
	if buf.Len() != 111 {
		t.Fatalf("len = %d, want end offset 111", buf.Len())
	}
}

func TestWriteBIF_Rejects(t *testing.T) {
	ok := []byte{0xFF, 0xD8, 0xFF, 0xD9}
	cases := map[string]struct {
		mult   uint32
		frames []BIFFrame
	}{
		"no frames":       {1000, nil},
		"zero multiplier": {0, []BIFFrame{{0, ok}}},
		"not increasing":  {1000, []BIFFrame{{1, ok}, {1, ok}}},
		"decreasing":      {1000, []BIFFrame{{2, ok}, {1, ok}}},
		"empty image":     {1000, []BIFFrame{{0, nil}}},
		"terminator ts":   {1000, []BIFFrame{{0xffffffff, ok}}},
	}
	for name, c := range cases {
		if err := WriteBIF(&bytes.Buffer{}, c.mult, c.frames); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestParseVTT_RoundTripsWriteVTT(t *testing.T) {
	spec := Spec{IntervalSec: 10, ThumbWidth: 320, ThumbHeight: 180, GridCols: 2, GridRows: 2}
	vtt, err := spec.WriteVTT(45, []string{"sprite_000.jpg", "sprite_001.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	cues, err := ParseVTT([]byte(vtt))
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 5 {
		t.Fatalf("cues = %d, want 5", len(cues))
	}
	want := []VTTCue{
		{0, 10 * time.Second, "sprite_000.jpg", image.Rect(0, 0, 320, 180)},
		{10 * time.Second, 20 * time.Second, "sprite_000.jpg", image.Rect(320, 0, 640, 180)},
		{20 * time.Second, 30 * time.Second, "sprite_000.jpg", image.Rect(0, 180, 320, 360)},
		{30 * time.Second, 40 * time.Second, "sprite_000.jpg", image.Rect(320, 180, 640, 360)},
		{40 * time.Second, 45 * time.Second, "sprite_001.jpg", image.Rect(0, 0, 320, 180)},
	}
	for i := range want {
		if cues[i] != want[i] {
			t.Errorf("cue %d = %+v, want %+v", i, cues[i], want[i])
		}
	}
}

func TestParseVTT_Tolerance(t *testing.T) {
	vtt := "WEBVTT\r\n\r\n1\r\n00:05.5 --> 00:10.000 align:start\r\nsprite_000.jpg#xywh=1,2,3,4\r\n\r\n" +
		"00:00:10.000 --> 00:00:20.000\r\nno-fragment.jpg\r\n\r\n" +
		"01:00:00.000 --> 01:00:10.000\r\nsprite_001.jpg#xywh=0,0,8,8\r\n"
	cues, err := ParseVTT([]byte(vtt))
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 {
		t.Fatalf("cues = %+v", cues)
	}
	if cues[0].Start != 5500*time.Millisecond || cues[0].End != 10*time.Second || cues[0].Rect != image.Rect(1, 2, 4, 6) {
		t.Errorf("cue 0 = %+v", cues[0])
	}
	if cues[1].Start != time.Hour {
		t.Errorf("cue 1 start = %s", cues[1].Start)
	}

	for _, bad := range []string{
		"WEBVTT\n\nxx:00.000 --> 00:10.000\nsprite_000.jpg#xywh=0,0,1,1\n",
		"WEBVTT\n\n00:00.000 --> 00:10.000\nsprite_000.jpg#xywh=0,0,1\n",
		"WEBVTT\n\n00:00.000 --> 00:10.000\nsprite_000.jpg#xywh=0,0,0,1\n",
		"WEBVTT\n\n00:00.000 --> 00:10.000\nsprite_000.jpg#xywh=-1,0,1,1\n",
	} {
		if _, err := ParseVTT([]byte(bad)); err == nil {
			t.Errorf("want error for %q", bad)
		}
	}
}

// writeSprite writes a cols x rows sprite sheet whose cells are filled with
// distinct solid colours (cell i gets cellColor(i)).
func writeSprite(t *testing.T, path string, cols, rows, w, h, first int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, cols*w, rows*h))
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			col := cellColor(first + r*cols + c)
			for y := r * h; y < (r+1)*h; y++ {
				for x := c * w; x < (c+1)*w; x++ {
					img.Set(x, y, col)
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cellColor(i int) color.RGBA {
	palette := []color.RGBA{
		{220, 30, 30, 255}, {30, 200, 30, 255}, {30, 30, 220, 255},
		{230, 230, 30, 255}, {30, 220, 220, 255}, {220, 30, 220, 255},
	}
	return palette[i%len(palette)]
}

func centerColor(t *testing.T, data []byte) (image.Rectangle, color.RGBA) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	r, g, bl, _ := img.At(b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2).RGBA()
	return b, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), 255}
}

func near(a, b color.RGBA) bool {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return d(a.R, b.R) < 24 && d(a.G, b.G) < 24 && d(a.B, b.B) < 24
}

func TestSliceSprites_CutsEachCell(t *testing.T) {
	dir := t.TempDir()
	spec := Spec{IntervalSec: 10, ThumbWidth: 64, ThumbHeight: 36, GridCols: 2, GridRows: 2}
	writeSprite(t, filepath.Join(dir, "sprite_000.jpg"), 2, 2, 64, 36, 0)
	writeSprite(t, filepath.Join(dir, "sprite_001.jpg"), 2, 2, 64, 36, 4)
	vtt, err := spec.WriteVTT(60, []string{"sprite_000.jpg", "sprite_001.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	cues, err := ParseVTT([]byte(vtt))
	if err != nil {
		t.Fatal(err)
	}
	frames, err := SliceSprites(dir, cues, BIFMaxWidth)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 6 {
		t.Fatalf("frames = %d, want 6", len(frames))
	}
	for i, f := range frames {
		b, c := centerColor(t, f)
		if b.Dx() != 64 || b.Dy() != 36 {
			t.Errorf("frame %d size %v, want 64x36", i, b.Size())
		}
		if !near(c, cellColor(i)) {
			t.Errorf("frame %d colour %v, want ~%v", i, c, cellColor(i))
		}
	}
}

func TestSliceSprites_ScalesDownWideThumbs(t *testing.T) {
	dir := t.TempDir()
	writeSprite(t, filepath.Join(dir, "sprite_000.jpg"), 2, 1, 640, 360, 0)
	cues := []VTTCue{
		{0, 10 * time.Second, "sprite_000.jpg", image.Rect(0, 0, 640, 360)},
		{10 * time.Second, 20 * time.Second, "sprite_000.jpg", image.Rect(640, 0, 1280, 360)},
	}
	frames, err := SliceSprites(dir, cues, 320)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range frames {
		b, c := centerColor(t, f)
		if b.Dx() != 320 || b.Dy() != 180 {
			t.Errorf("frame %d size %v, want 320x180", i, b.Size())
		}
		if !near(c, cellColor(i)) {
			t.Errorf("frame %d colour %v, want ~%v", i, c, cellColor(i))
		}
	}
}

func TestSliceSprites_StopsPastSheetEdge(t *testing.T) {
	dir := t.TempDir()
	writeSprite(t, filepath.Join(dir, "sprite_000.jpg"), 1, 1, 32, 18, 0)
	cues := []VTTCue{
		{0, 10 * time.Second, "sprite_000.jpg", image.Rect(0, 0, 32, 18)},
		{10 * time.Second, 20 * time.Second, "sprite_000.jpg", image.Rect(32, 0, 64, 18)},
	}
	frames, err := SliceSprites(dir, cues, 320)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1 (second cell is off the sheet)", len(frames))
	}
}

func TestSliceSprites_RejectsForeignSpriteNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../sprite_000.jpg", "sprite_0.jpg", "/etc/passwd", "chapter_000.jpg"} {
		cues := []VTTCue{{0, time.Second, name, image.Rect(0, 0, 1, 1)}}
		if _, err := SliceSprites(dir, cues, 320); err == nil {
			t.Errorf("%q: want error", name)
		}
	}
}

func TestEnsureBIF_BuildsCachesAndParses(t *testing.T) {
	dir := t.TempDir()
	spec := Spec{IntervalSec: 10, ThumbWidth: 64, ThumbHeight: 36, GridCols: 2, GridRows: 2}
	writeSprite(t, filepath.Join(dir, "sprite_000.jpg"), 2, 2, 64, 36, 0)
	vtt, _ := spec.WriteVTT(35, []string{"sprite_000.jpg"})
	if err := os.WriteFile(filepath.Join(dir, "index.vtt"), []byte(vtt), 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := EnsureBIF(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != BIFFileName {
		t.Fatalf("path = %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data[:8], bifMagic[:]) {
		t.Fatalf("magic = % x", data[:8])
	}
	n := binary.LittleEndian.Uint32(data[12:])
	if n != 4 {
		t.Fatalf("images = %d, want 4", n)
	}
	if m := binary.LittleEndian.Uint32(data[16:]); m != 10000 {
		t.Fatalf("multiplier = %d, want 10000", m)
	}
	for i := uint32(0); i <= n; i++ {
		ts := binary.LittleEndian.Uint32(data[64+8*i:])
		off := binary.LittleEndian.Uint32(data[64+8*i+4:])
		if i == n {
			if ts != 0xffffffff || int(off) != len(data) {
				t.Fatalf("terminator = (%x, %d), want (ffffffff, %d)", ts, off, len(data))
			}
			continue
		}
		if ts != i {
			t.Errorf("entry %d timestamp %d", i, ts)
		}
		end := binary.LittleEndian.Uint32(data[64+8*(i+1)+4:])
		_, c := centerColor(t, data[off:end])
		if !near(c, cellColor(int(i))) {
			t.Errorf("frame %d colour %v, want ~%v", i, c, cellColor(int(i)))
		}
	}

	// Second call is served from the cache, untouched.
	st, _ := os.Stat(path)
	if _, err := EnsureBIF(dir); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(path)
	if !st2.ModTime().Equal(st.ModTime()) {
		t.Error("cached bif was rebuilt")
	}
	// No temp files left behind.
	left, _ := filepath.Glob(filepath.Join(dir, ".index-*"))
	if len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestEnsureBIF_RebuildsWhenVTTIsNewer(t *testing.T) {
	dir := t.TempDir()
	spec := Spec{IntervalSec: 10, ThumbWidth: 64, ThumbHeight: 36, GridCols: 2, GridRows: 2}
	writeSprite(t, filepath.Join(dir, "sprite_000.jpg"), 2, 2, 64, 36, 0)
	vtt, _ := spec.WriteVTT(20, []string{"sprite_000.jpg"})
	vttPath := filepath.Join(dir, "index.vtt")
	if err := os.WriteFile(vttPath, []byte(vtt), 0o644); err != nil {
		t.Fatal(err)
	}
	// A stale cache from an older generation.
	bifPath := filepath.Join(dir, BIFFileName)
	if err := os.WriteFile(bifPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(bifPath, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureBIF(dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(bifPath)
	if !bytes.Equal(data[:8], bifMagic[:]) || binary.LittleEndian.Uint32(data[12:]) != 2 {
		t.Fatalf("bif not rebuilt: % x", data[:min(len(data), 16)])
	}
}

func TestEnsureBIF_NoVTTIsNotExist(t *testing.T) {
	if _, err := EnsureBIF(t.TempDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
	if _, err := EnsureBIF(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing dir err = %v, want fs.ErrNotExist", err)
	}
}

func TestEnsureBIF_MissingSpriteFails(t *testing.T) {
	dir := t.TempDir()
	spec := Spec{IntervalSec: 10, ThumbWidth: 64, ThumbHeight: 36, GridCols: 2, GridRows: 2}
	vtt, _ := spec.WriteVTT(20, []string{"sprite_000.jpg"})
	if err := os.WriteFile(filepath.Join(dir, "index.vtt"), []byte(vtt), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureBIF(dir); err == nil {
		t.Fatal("want error with the sprite missing")
	}
	if _, err := os.Stat(filepath.Join(dir, BIFFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a failed build left a bif behind")
	}
}
