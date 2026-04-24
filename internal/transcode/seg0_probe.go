package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// WaitForSeg0Audio blocks until FFmpeg has produced enough output for us
// to locate the first audible frame, then returns that frame's offset
// (seconds) from the start of the stream. With mid-stream -ss and
// AC3 → AAC re-encode the encoder's first valid frame lands a few
// seconds after the seek point — sometimes partway into seg 0, often
// not until seg 1 or later. Scanning segments in order finds the
// audio whichever segment it lands in, and the offset we return is
// what the player sets as its HLS.js startPosition so playback begins
// at the first audible frame instead of showing silent video while
// the audio pipeline warms up.
//
// Returns (0, false) on timeout, probe failure, or when no audio is
// found in the first audioScanSegments segments. Caller falls back
// to starting at the stream head — user sees the old silent-head
// behavior, which is a no-op rather than a regression.
//
// The wait key is the existence of the next segment: HLS muxer only
// writes seg N+1 after seg N is closed, so its appearance is the
// cleanest signal that seg N is finalized and safe to probe.
const audioScanSegments = 4

func WaitForSeg0Audio(ctx context.Context, sessionDir string, timeout time.Duration) (float64, bool) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	seg0 := filepath.Join(sessionDir, "seg00000.ts")
	// We need seg 0's first video PTS to use as the stream's time zero.
	// Wait for seg 1 (which signals seg 0 is finalized) before probing.
	if !waitForFile(waitCtx, filepath.Join(sessionDir, "seg00001.ts")) {
		return 0, false
	}
	seg0Video, ok := probeFirstPacketPTS(waitCtx, seg0, "v:0")
	if !ok {
		return 0, false
	}

	for i := 0; i < audioScanSegments; i++ {
		segPath := filepath.Join(sessionDir, segmentName(i))
		// Make sure the segment we're about to probe is closed: its
		// successor must exist before we read it.
		if !waitForFile(waitCtx, filepath.Join(sessionDir, segmentName(i+1))) {
			return 0, false
		}
		if audioPts, ok := probeFirstPacketPTS(waitCtx, segPath, "a:0"); ok {
			gap := audioPts - seg0Video
			if gap < 0 {
				return 0, false
			}
			return gap, true
		}
	}
	return 0, false
}

func segmentName(index int) string {
	return "seg" + leftPad5(index) + ".ts"
}

func leftPad5(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 5 {
		s = "0" + s
	}
	return s
}

// waitForFile polls for path to exist, honoring the caller's context.
// Returns false when the context deadline is hit before the file
// appears. A 100 ms tick keeps the busy-loop cost low without adding
// noticeable latency on top of FFmpeg's segment-flush cadence.
func waitForFile(ctx context.Context, path string) bool {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// probeFirstPacketPTS runs ffprobe against a specific stream and
// returns the PTS (seconds) of its first packet. Uses -read_intervals
// "%+#1" to scope ffprobe to the first packet only — a full-segment
// scan would be wasteful here.
func probeFirstPacketPTS(ctx context.Context, path, stream string) (float64, bool) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{
		"-v", "error",
		"-select_streams", stream,
		"-show_packets",
		"-read_intervals", "%+#1",
		"-show_entries", "packet=pts_time",
		"-of", "csv=p=0",
		path,
	}
	out, err := exec.CommandContext(cctx, "ffprobe", args...).Output()
	if err != nil {
		return 0, false
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if line == "" {
		return 0, false
	}
	// Output format is `pts_time[,side_data…]` — take the first field.
	field := strings.SplitN(line, ",", 2)[0]
	v, err := strconv.ParseFloat(field, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
