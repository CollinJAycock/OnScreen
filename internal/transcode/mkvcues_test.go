package transcode

import (
	"bytes"
	"errors"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ── tiny EBML writer for hand-built files ─────────────────────────────────

func ebmlID(id uint32) []byte {
	switch {
	case id > 0xFFFFFF:
		return []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
	case id > 0xFFFF:
		return []byte{byte(id >> 16), byte(id >> 8), byte(id)}
	case id > 0xFF:
		return []byte{byte(id >> 8), byte(id)}
	default:
		return []byte{byte(id)}
	}
}

// ebmlSize writes an 8-byte size vint (always valid, keeps offsets simple).
func ebmlSize(n int) []byte {
	b := []byte{0x01, 0, 0, 0, 0, 0, 0, 0}
	for i := 7; i >= 1; i-- {
		b[i] = byte(n)
		n >>= 8
	}
	return b
}

func ebmlEl(id uint32, body ...[]byte) []byte {
	data := bytes.Join(body, nil)
	return bytes.Join([][]byte{ebmlID(id), ebmlSize(len(data)), data}, nil)
}

func ebmlUint(id uint32, v uint64) []byte {
	return ebmlEl(id, []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func cuePoint(t uint64, track uint64) []byte {
	return ebmlEl(mkvIDCuePoint, ebmlUint(mkvIDCueTime, t),
		ebmlEl(mkvIDCueTrackPos, ebmlUint(mkvIDCueTrack, track), ebmlUint(0xF1, 0)))
}

// buildMKV lays out: EBML header, Segment{SeekHead, Info, Tracks, Cluster,
// Cues}. The SeekHead points at the Cues by its real offset, the way
// mkvmerge/ffmpeg write it after finishing the file.
func buildMKV(t *testing.T, tsScale uint64, cues []byte, withSeekEntry bool) []byte {
	t.Helper()
	header := ebmlEl(mkvIDEBML, ebmlEl(0x4282, []byte("matroska")))
	info := ebmlEl(mkvIDInfo, ebmlUint(mkvIDTimestampScale, tsScale))
	tracks := ebmlEl(mkvIDTracks,
		ebmlEl(mkvIDTrackEntry, ebmlUint(mkvIDTrackNumber, 2), ebmlUint(mkvIDTrackType, 2)), // audio
		ebmlEl(mkvIDTrackEntry, ebmlUint(mkvIDTrackNumber, 1), ebmlUint(mkvIDTrackType, 1)), // video
	)
	cluster := ebmlEl(mkvIDCluster, ebmlUint(0xE7, 0), bytes.Repeat([]byte{0xAA}, 300))
	// SeekHead size is fixed (one or zero entries with fixed-width fields),
	// so compute it with a placeholder position first.
	seekHead := func(cuesPos uint64) []byte {
		if !withSeekEntry {
			return ebmlEl(mkvIDSeekHead)
		}
		return ebmlEl(mkvIDSeekHead, ebmlEl(mkvIDSeek,
			ebmlEl(mkvIDSeekID, ebmlID(mkvIDCues)),
			ebmlUint(mkvIDSeekPosition, cuesPos)))
	}
	sh := seekHead(0)
	cuesRel := uint64(len(sh) + len(info) + len(tracks) + len(cluster))
	sh = seekHead(cuesRel)
	var segBody []byte
	segBody = append(segBody, sh...)
	segBody = append(segBody, info...)
	segBody = append(segBody, tracks...)
	segBody = append(segBody, cluster...)
	if cues != nil {
		segBody = append(segBody, cues...)
	}
	return append(header, ebmlEl(mkvIDSegment, segBody)...)
}

func TestReadMKVVideoKeyframes_HandBuilt(t *testing.T) {
	cues := ebmlEl(mkvIDCues,
		cuePoint(0, 1),
		cuePoint(1500, 2), // audio-only cue point: ignored
		cuePoint(4000, 1),
		cuePoint(4000, 1), // duplicate: deduped
		cuePoint(13208, 1),
	)
	for _, tc := range []struct {
		name      string
		seekEntry bool
	}{{"via SeekHead", true}, {"walking past the cluster", false}} {
		t.Run(tc.name, func(t *testing.T) {
			b := buildMKV(t, 1_000_000, cues, tc.seekEntry)
			got, err := ReadMKVVideoKeyframes(bytesReaderAt(b), int64(len(b)))
			if err != nil {
				t.Fatalf("ReadMKVVideoKeyframes: %v", err)
			}
			want := []float64{0, 4, 13.208}
			if len(got) != len(want) {
				t.Fatalf("got %v, want %v", got, want)
			}
			for i := range want {
				if math.Abs(got[i]-want[i]) > 1e-9 {
					t.Fatalf("got %v, want %v", got, want)
				}
			}
		})
	}
}

func TestReadMKVVideoKeyframes_TimestampScale(t *testing.T) {
	// 100 µs units: CueTime 25000 = 2.5 s.
	b := buildMKV(t, 100_000, ebmlEl(mkvIDCues, cuePoint(0, 1), cuePoint(25000, 1)), true)
	got, err := ReadMKVVideoKeyframes(bytesReaderAt(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || math.Abs(got[1]-2.5) > 1e-9 {
		t.Fatalf("got %v, want [0 2.5]", got)
	}
}

func TestReadMKVVideoKeyframes_NoCues(t *testing.T) {
	b := buildMKV(t, 1_000_000, nil, false)
	if _, err := ReadMKVVideoKeyframes(bytesReaderAt(b), int64(len(b))); !errors.Is(err, ErrNoMKVCues) {
		t.Fatalf("err = %v, want ErrNoMKVCues", err)
	}
	// Cues that index only the audio track are no video index either.
	b = buildMKV(t, 1_000_000, ebmlEl(mkvIDCues, cuePoint(0, 2)), true)
	if _, err := ReadMKVVideoKeyframes(bytesReaderAt(b), int64(len(b))); !errors.Is(err, ErrNoMKVCues) {
		t.Fatalf("err = %v, want ErrNoMKVCues", err)
	}
}

func TestReadMKVVideoKeyframes_NotMKV(t *testing.T) {
	junk := []byte("\x00\x00\x00\x20ftypisom not a matroska file at all")
	if _, err := ReadMKVVideoKeyframes(bytesReaderAt(junk), int64(len(junk))); err == nil {
		t.Fatal("want an error for a non-EBML file")
	}
	trunc := buildMKV(t, 1_000_000, ebmlEl(mkvIDCues, cuePoint(0, 1)), true)
	trunc = trunc[:len(trunc)-10]
	if _, err := ReadMKVVideoKeyframes(bytesReaderAt(trunc), int64(len(trunc))); err == nil {
		t.Fatal("want an error for a truncated file")
	}
}

// TestMKVVideoKeyframes_MatchesFFprobe checks the reader against ffprobe's
// packet scan on a real ffmpeg-muxed file with irregular keyframes.
func TestMKVVideoKeyframes_MatchesFFprobe(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}
	src := filepath.Join(t.TempDir(), "irregular.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "30", "-c:v", "libx264", "-preset", "ultrafast", "-g", "1000",
		"-force_key_frames", "0,1.5,4,4.5,9,13.2,14,20,21,27.5",
		"-c:a", "aac", "-y", src).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg synth: %v: %s", err, out)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", src).Output()
	if err != nil {
		t.Fatal(err)
	}
	var want []float64
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(strings.TrimSpace(line), ",")
		if len(parts) == 2 && strings.HasPrefix(parts[1], "K") {
			v, _ := strconv.ParseFloat(parts[0], 64)
			want = append(want, v)
		}
	}
	got, err := MKVVideoKeyframes(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d keyframes %v, ffprobe %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 0.0015 {
			t.Fatalf("keyframe %d: got %.3f, ffprobe %.3f", i, got[i], want[i])
		}
	}
}
