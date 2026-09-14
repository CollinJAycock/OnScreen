package transcode

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSeekedCopy_AudioAndVideoStartTogether is the end-to-end guard for the
// "audio runs ahead of the picture after a seek" bug. It is deliberately a
// CONTENT check, not a timestamp check: the whole reason that bug survived so
// long is that the output's timestamps are perfectly self-consistent — audio
// and video are stamped as if aligned — while the frames and the samples come
// from points up to a GOP apart. Every PTS-based assertion passes on a broken
// stream.
//
// How the content is pinned:
//
//   - VIDEO: the source is stream-copied, so a decoded output frame is
//     bit-identical to its source frame. `-f framemd5` on both sides turns
//     "which frame is this?" into an exact hash lookup, giving the true content
//     time the output starts at.
//   - AUDIO: the synthetic source is silent until toneStartSec and a tone
//     afterwards, so the onset in the decoded output says which content time
//     the audio began at.
//
// If the two agree, a viewer hears the sound that belongs to the picture.
//
// HONEST LIMITATION, verified by mutation: removing `-noaccurate_seek` does NOT
// make this test fail. The original bug needed ffmpeg's seek to MISS the
// keyframe the probe predicted, which happens on real matroska rips (cluster
// granularity — nudging the target by up to 100 ms did not move the landing
// point) and not on a clean synthetic clip, where the seek lands on 20.000 and
// both streams line up with or without the flag. So this guards the INVARIANT
// — audio and video must begin at the same content time — and would catch a
// future change that reintroduces an audio trim on a copy path. The flag itself
// is pinned by TestBuildHLS_SeekedCopy_NoAccurateSeek and friends, which do
// fail without the fix; the real-world behaviour was verified against QA.
func TestSeekedCopy_AudioAndVideoStartTogether(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}

	const (
		gopSec       = 10.0 // sparse GOP, like the BD/WEB rips this bug shows up on
		durSec       = 40.0
		toneStartSec = 22.0 // silence before, 1 kHz after
		seekSec      = 20.0 // exactly a keyframe — the shape the server produces
	)

	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	// Matroska + sparse GOP + AC3 audio: the combination the report came from.
	// testsrc gives every frame distinct content so frame hashes are unique.
	if out, err := exec.Command("ffmpeg", "-v", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=duration=%.0f:size=320x240:rate=25", durSec),
		"-f", "lavfi", "-i", fmt.Sprintf(
			"aevalsrc=sin(2*PI*1000*t)*gt(t\\,%.0f):d=%.0f:s=48000:c=stereo", toneStartSec, durSec),
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-g", strconv.Itoa(int(gopSec*25)), "-keyint_min", strconv.Itoa(int(gopSec*25)),
		"-sc_threshold", "0",
		"-c:a", "ac3", "-b:a", "192k",
		"-y", src,
	).CombinedOutput(); err != nil {
		t.Fatalf("synthesise source: %v: %s", err, out)
	}

	sessDir := filepath.Join(dir, "sess")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	args := BuildHLS(BuildArgs{
		InputPath:     src,
		StartOffset:   seekSec,
		Encoder:       "copy", // remux: the path that carries the bug
		AudioCodec:    "aac",
		AudioChannels: 2,
		SessionDir:    sessDir,
		SegmentPrefix: "seg",
	})
	cmd := exec.Command("ffmpeg", args...)
	cmd.Dir = sessDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("transcode: %v: %s", err, out)
	}

	joined := filepath.Join(dir, "all.ts")
	if err := concatSegments(sessDir, joined); err != nil {
		t.Fatalf("join segments: %v", err)
	}

	videoStart, err := videoContentStart(t, joined, src)
	if err != nil {
		t.Fatalf("locate output video in the source: %v", err)
	}
	audioStart, err := audioContentStart(joined, toneStartSec)
	if err != nil {
		t.Fatalf("locate the tone onset: %v", err)
	}

	skew := audioStart - videoStart
	t.Logf("video starts at content %.3fs, audio at content %.3fs (skew %+.3fs)",
		videoStart, audioStart, skew)
	// A GOP here is 10 s; the bug produced a skew of roughly that. 250 ms is
	// far tighter than anything audible and far looser than encoder priming.
	if math.Abs(skew) > 0.25 {
		t.Errorf("audio and video start %.3fs apart (audio content %.3f, video content %.3f); "+
			"the sound does not belong to the picture — see -noaccurate_seek in BuildHLS",
			skew, audioStart, videoStart)
	}
}

// concatSegments joins the session's segments (in order) into one file.
func concatSegments(sessDir, dst string) error {
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		return err
	}
	var init string
	var segs []string
	for _, e := range entries {
		n := e.Name()
		switch {
		case n == "init.mp4":
			init = filepath.Join(sessDir, n)
		case strings.HasPrefix(n, "seg") && (strings.HasSuffix(n, ".ts") || strings.HasSuffix(n, ".m4s")):
			segs = append(segs, filepath.Join(sessDir, n))
		}
	}
	if len(segs) == 0 {
		return fmt.Errorf("no segments in %s", sessDir)
	}
	sortStrings(segs)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	files := segs
	if init != "" {
		files = append([]string{init}, segs...)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if _, err := out.Write(b); err != nil {
			return err
		}
	}
	return nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// frameHashes returns (pts seconds, md5) per decoded video frame.
func frameHashes(path string, extra ...string) ([][2]string, float64, error) {
	args := append([]string{"-v", "error"}, extra...)
	args = append(args, "-i", path, "-map", "0:v:0", "-f", "framemd5", "-")
	out, err := exec.Command("ffmpeg", args...).Output()
	if err != nil {
		return nil, 0, err
	}
	var rows [][2]string
	tb := 0.0
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#tb 0:") {
			frac := strings.TrimSpace(strings.TrimPrefix(line, "#tb 0:"))
			if p := strings.SplitN(frac, "/", 2); len(p) == 2 {
				num, _ := strconv.ParseFloat(p[0], 64)
				den, _ := strconv.ParseFloat(p[1], 64)
				if den != 0 {
					tb = num / den
				}
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, ",")
		if len(f) < 6 {
			continue
		}
		rows = append(rows, [2]string{strings.TrimSpace(f[2]), strings.TrimSpace(f[5])})
	}
	return rows, tb, nil
}

// videoContentStart reports which source content time the output's video
// begins at, by matching decoded frames by hash (exact, because the video is
// stream-copied). Only hashes unique within the source are trusted.
func videoContentStart(t *testing.T, outPath, srcPath string) (float64, error) {
	outRows, outTB, err := frameHashes(outPath)
	if err != nil {
		return 0, err
	}
	srcRows, srcTB, err := frameHashes(srcPath, "-copyts")
	if err != nil {
		return 0, err
	}
	seen := map[string]int{}
	at := map[string]float64{}
	for _, r := range srcRows {
		pts, err := strconv.ParseFloat(r[0], 64)
		if err != nil {
			continue
		}
		seen[r[1]]++
		at[r[1]] = pts * srcTB
	}
	for _, r := range outRows {
		if seen[r[1]] != 1 {
			continue // ambiguous frame — skip
		}
		pts, err := strconv.ParseFloat(r[0], 64)
		if err != nil {
			continue
		}
		// content at output time 0 = (this frame's source time) - (its output time)
		return at[r[1]] - pts*outTB, nil
	}
	return 0, fmt.Errorf("no output frame matched a unique source frame")
}

// audioContentStart reports which source content time the output's audio begins
// at, using the synthetic source's silence→tone transition as the marker.
func audioContentStart(outPath string, toneStartSec float64) (float64, error) {
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", outPath,
		"-vn", "-ac", "1", "-ar", "8000", "-f", "s16le", "-").Output()
	if err != nil {
		return 0, err
	}
	const rate = 8000
	const win = 400 // 50 ms
	var onset = -1.0
	for i := 0; i+win*2 <= len(out); i += win * 2 {
		var sum float64
		for j := i; j < i+win*2; j += 2 {
			v := float64(int16(binary.LittleEndian.Uint16(out[j : j+2])))
			sum += v * v
		}
		if math.Sqrt(sum/float64(win)) > 2000 { // tone is full-scale; silence is ~0
			onset = float64(i/2) / rate
			break
		}
	}
	if onset < 0 {
		return 0, fmt.Errorf("tone never appeared in the output audio")
	}
	// The tone begins at content toneStartSec, and it appeared `onset` seconds
	// into the stream, so the audio began at content (toneStartSec - onset).
	return toneStartSec - onset, nil
}
