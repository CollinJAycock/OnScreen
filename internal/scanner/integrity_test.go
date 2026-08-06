package scanner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCountAnalyzedFrames(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want int
	}{
		{
			// One stats block per decoded frame; YAVG appears exactly once
			// per block regardless of the other stats around it.
			name: "three frames",
			out: "frame:0    pts:7      pts_time:0.007\n" +
				"lavfi.signalstats.YMIN=16\nlavfi.signalstats.YAVG=74.24\n" +
				"frame:1    pts:8      pts_time:0.008\n" +
				"lavfi.signalstats.YAVG=74.30\n" +
				"frame:2    pts:9      pts_time:0.009\n" +
				"lavfi.signalstats.YAVG=74.11\n",
			want: 3,
		},
		{name: "empty", out: "", want: 0},
		{name: "unrelated output", out: "some ffmpeg chatter\n", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := countAnalyzedFrames(tc.out); got != tc.want {
				t.Errorf("countAnalyzedFrames = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSummarizeDecodeErrors(t *testing.T) {
	tests := []struct {
		name          string
		out           string
		wantCount     int
		wantFirst     string
		wantNoStreams bool
	}{
		{
			// The exact shape the QA fake produces: per-slice NALU failures.
			name: "nalu spew",
			out: "[hevc @ 0x55] Failed to parse header of NALU (type 0/1)\n" +
				"[hevc @ 0x55] Failed to parse header of NALU (type 0/1)\n" +
				"[hevc @ 0x55] Invalid NAL unit size (0 > 141).\n",
			wantCount: 3,
			wantFirst: "[hevc @ 0x55] Failed to parse header of NALU (type 0/1)",
		},
		{name: "clean", out: "", wantCount: 0, wantFirst: ""},
		{name: "blank lines only", out: "\n \n\n", wantCount: 0, wantFirst: ""},
		{
			// Audio-only file hit the video map — a caller bug, not damage.
			name:          "no video stream",
			out:           "Stream map '0:v:0' matches no streams.\nTo ignore this, add a trailing '?' to the map.\n",
			wantCount:     1, // the advice line still counts; noStreams is what matters
			wantFirst:     "To ignore this, add a trailing '?' to the map.",
			wantNoStreams: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			count, first, noStreams := summarizeDecodeErrors(tc.out)
			if count != tc.wantCount || first != tc.wantFirst || noStreams != tc.wantNoStreams {
				t.Errorf("summarizeDecodeErrors = (%d, %q, %v), want (%d, %q, %v)",
					count, first, noStreams, tc.wantCount, tc.wantFirst, tc.wantNoStreams)
			}
		})
	}
}

func TestIntegrityProbePoints(t *testing.T) {
	// Two-hour movie: three distinct offsets at 10/50/90%.
	pts := integrityProbePoints(2 * 60 * 60 * 1000)
	if len(pts) != 3 {
		t.Fatalf("want 3 points, got %d", len(pts))
	}
	wantSec := []int64{720, 3600, 6480}
	for i, p := range pts {
		if p.offsetSec != wantSec[i] {
			t.Errorf("point %d offset = %ds, want %ds", i, p.offsetSec, wantSec[i])
		}
	}

	// Short clip / unknown duration: single head probe.
	for _, dur := range []int64{0, -1, 10_000, 44_000} {
		pts := integrityProbePoints(dur)
		if len(pts) != 1 || pts[0].offsetSec != 0 {
			t.Errorf("duration %dms: want single head probe, got %+v", dur, pts)
		}
	}
}

func TestIntegrityDetail_Bounded(t *testing.T) {
	long := integrityPoint{offsetSec: 3600, pct: 50, frames: 0,
		errLines: 214, firstErr: strings.Repeat("x", 600)}
	detail := integrityDetail([]integrityPoint{long, long, long})
	if len(detail) > integrityDetailMax {
		t.Errorf("detail length %d exceeds cap %d", len(detail), integrityDetailMax)
	}
	if !strings.Contains(detail, "at 50% (t=3600s)") {
		t.Errorf("detail missing offset summary: %q", detail)
	}
}

// genIntegrityClip encodes a testsrc2 MPEG-TS clip. TS is the fixture
// container of choice for corruption tests: it has no index, so damage in
// the middle can't be routed around by a seek table, and byte-level
// corruption behaves like real damage instead of like a broken header.
// Short GOP (-g 20 at r=10 → 2 s) keeps every probe window decodable.
func genIntegrityClip(t *testing.T, dir string, durSec int) string {
	t.Helper()
	out := filepath.Join(dir, "clip.ts")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=192x108:r=10:d="+strconv.Itoa(durSec),
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "20",
		"-f", "mpegts", out)
	if err := cmd.Run(); err != nil {
		t.Fatalf("generate clip: %v", err)
	}
	return out
}

// TestCheckIntegrity_Healthy: a clean clip must read ok at all three
// offsets. Requires ffmpeg on PATH.
func TestCheckIntegrity_Healthy(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	ctx := context.Background()
	clip := genIntegrityClip(t, t.TempDir(), 50)

	rep, err := CheckIntegrity(ctx, clip, 50_000)
	if err != nil {
		t.Fatalf("healthy clip: %v", err)
	}
	if rep.Status != IntegrityOK {
		t.Errorf("healthy clip: status %q (detail %v), want ok", rep.Status, detailStr(rep.Detail))
	}
}

// TestCheckIntegrity_CorruptMiddle: garbage the middle half of the video
// BITSTREAM while keeping the container intact — the damaged-release shape.
// (Zeroing whole byte ranges is the wrong synthesis: that kills the TS sync
// bytes, the demuxer resyncs past the hole, and accurate-seek serves clean
// frames from the far side. Real fakes demux fine and fail NALU parsing,
// so corrupt the packet payloads and preserve the 188-byte packet headers.)
func TestCheckIntegrity_CorruptMiddle(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	ctx := context.Background()
	dir := t.TempDir()
	clip := genIntegrityClip(t, dir, 50)

	data, err := os.ReadFile(clip)
	if err != nil {
		t.Fatal(err)
	}
	const tsPacket = 188
	start := (len(data) * 40 / 100 / tsPacket) * tsPacket
	end := (len(data) * 90 / 100 / tsPacket) * tsPacket
	for p := start; p+tsPacket <= end; p += tsPacket {
		// Keep the first 8 bytes (sync byte, PID, flags) so the demuxer
		// still delivers the packet; fill the payload with junk the H.264
		// parser will choke on.
		for j := p + 8; j < p+tsPacket; j++ {
			data[j] = 0xAA
		}
	}
	corrupt := filepath.Join(dir, "corrupt.ts")
	if err := os.WriteFile(corrupt, data, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := CheckIntegrity(ctx, corrupt, 50_000)
	if err != nil {
		t.Fatalf("corrupt clip: %v", err)
	}
	if rep.Status != IntegrityDamaged {
		t.Errorf("corrupt clip: status %q, want damaged", rep.Status)
	}
	if rep.Detail == nil || !strings.Contains(*rep.Detail, "spot-decode failed") {
		t.Errorf("corrupt clip: detail = %v, want failure summary", detailStr(rep.Detail))
	}
}

// TestCheckIntegrity_Truncated: keep only the first 30% of the bytes while
// still claiming the full duration — the shape of an interrupted download
// whose container header (written up front) advertises the whole runtime.
func TestCheckIntegrity_Truncated(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	ctx := context.Background()
	dir := t.TempDir()
	clip := genIntegrityClip(t, dir, 50)

	data, err := os.ReadFile(clip)
	if err != nil {
		t.Fatal(err)
	}
	truncated := filepath.Join(dir, "truncated.ts")
	if err := os.WriteFile(truncated, data[:len(data)*30/100], 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := CheckIntegrity(ctx, truncated, 50_000)
	if err != nil {
		t.Fatalf("truncated clip: %v", err)
	}
	if rep.Status != IntegrityDamaged {
		t.Errorf("truncated clip: status %q, want damaged", rep.Status)
	}
}

// TestCheckIntegrity_AudioOnly: no video stream must surface as an ERROR
// (fail open — the work-queue filter should never send these), not as a
// damage verdict.
func TestCheckIntegrity_AudioOnly(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "audio.ts")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=5",
		"-c:a", "aac", "-f", "mpegts", out)
	if err := cmd.Run(); err != nil {
		t.Fatalf("generate audio clip: %v", err)
	}

	if _, err := CheckIntegrity(ctx, out, 5_000); err == nil {
		t.Error("audio-only file should error, not return a verdict")
	}
}

// TestCheckIntegrity_MissingFile: a path that doesn't exist is a probe
// error, never a verdict.
func TestCheckIntegrity_MissingFile(t *testing.T) {
	if _, err := CheckIntegrity(context.Background(),
		filepath.Join(t.TempDir(), "nope.mkv"), 60_000); err == nil {
		t.Error("missing file should error, not return a verdict")
	}
}

func detailStr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
