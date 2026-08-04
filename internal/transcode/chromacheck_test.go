package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseSignalstatsChroma(t *testing.T) {
	tests := []struct {
		name       string
		out        string
		wantFrames int
		wantDead   bool
	}{
		{
			// The exact shape observed from the corrupted QA output: luma has
			// structure, chroma planes are literally zero on every frame.
			name: "dead chroma on every frame",
			out: "lavfi.signalstats.YAVG=74.24\nlavfi.signalstats.UAVG=0\nlavfi.signalstats.VAVG=0\n" +
				"lavfi.signalstats.YAVG=74.24\nlavfi.signalstats.UAVG=0\nlavfi.signalstats.VAVG=0\n",
			wantFrames: 2,
			wantDead:   true,
		},
		{
			// Real content: neutral/grayscale chroma sits at 128, so even a
			// black-and-white film is nowhere near the dead ceiling.
			name: "live chroma",
			out: "lavfi.signalstats.UAVG=126.51\nlavfi.signalstats.VAVG=130.02\n" +
				"lavfi.signalstats.UAVG=127.98\nlavfi.signalstats.VAVG=128.44\n",
			wantFrames: 2,
			wantDead:   false,
		},
		{
			// A single live frame clears the stream — partial corruption is
			// not this detector's job, and killing a playing session needs an
			// unambiguous signal.
			name: "one live frame among dead ones",
			out: "lavfi.signalstats.UAVG=0\nlavfi.signalstats.VAVG=0\n" +
				"lavfi.signalstats.UAVG=128\nlavfi.signalstats.VAVG=128\n",
			wantFrames: 2,
			wantDead:   false,
		},
		{
			// Dither around a zeroed plane still counts as dead (< the 2.0
			// ceiling), but 2.0 itself does not.
			name:       "dither below ceiling is dead",
			out:        "lavfi.signalstats.UAVG=0.31\nlavfi.signalstats.VAVG=1.99\n",
			wantFrames: 1,
			wantDead:   true,
		},
		{
			name:       "at the ceiling is alive",
			out:        "lavfi.signalstats.UAVG=2.0\nlavfi.signalstats.VAVG=0\n",
			wantFrames: 1,
			wantDead:   false,
		},
		{
			// No frames analyzed must NOT read as dead — the caller treats
			// zero frames as an analysis error and fails open.
			name:       "no stats",
			out:        "some unrelated ffmpeg output\n",
			wantFrames: 0,
			wantDead:   false,
		},
		{
			name:       "empty",
			out:        "",
			wantFrames: 0,
			wantDead:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frames, dead := parseSignalstatsChroma(tc.out)
			if frames != tc.wantFrames || dead != tc.wantDead {
				t.Errorf("parseSignalstatsChroma: want (frames=%d, dead=%v), got (frames=%d, dead=%v)",
					tc.wantFrames, tc.wantDead, frames, dead)
			}
		})
	}
}

// TestSegmentChromaDead_RealSegments runs the detector against real encoder
// output: a normal clip must read alive, and a clip with its chroma planes
// forced to zero (lutyuv u=0:v=0 — synthesizing exactly the green-frame
// corruption) must read dead. Requires ffmpeg on PATH.
func TestSegmentChromaDead_RealSegments(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	ctx := context.Background()
	dir := t.TempDir()

	gen := func(name, vf string) string {
		out := filepath.Join(dir, name)
		args := []string{"-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc2=s=192x108:r=10:d=1"}
		if vf != "" {
			args = append(args, "-vf", vf)
		}
		args = append(args, "-c:v", "libx264", "-preset", "ultrafast",
			"-f", "mpegts", out)
		if err := exec.CommandContext(ctx, "ffmpeg", args...).Run(); err != nil {
			t.Fatalf("generate %s: %v", name, err)
		}
		return out
	}

	alive := gen("alive.ts", "")
	dead, err := SegmentChromaDead(ctx, alive, "")
	if err != nil {
		t.Fatalf("alive clip: %v", err)
	}
	if dead {
		t.Error("testsrc2 (saturated color) must not read as dead chroma")
	}

	green := gen("green.ts", "lutyuv=u=0:v=0")
	dead, err = SegmentChromaDead(ctx, green, "")
	if err != nil {
		t.Fatalf("green clip: %v", err)
	}
	if !dead {
		t.Error("zeroed-chroma clip must read as dead")
	}
}

// TestSegmentChromaDead_FMP4Init covers the fMP4 leg: a bare .m4s fragment has
// no decoder config, so the check must decode it via concat with the init
// segment — the same shape the worker hands it for HEVC/AV1 sessions.
func TestSegmentChromaDead_FMP4Init(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	ctx := context.Background()
	dir := t.TempDir()

	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=192x108:r=10:d=1",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-f", "hls", "-hls_time", "1", "-hls_segment_type", "fmp4",
		"-hls_segment_filename", filepath.Join(dir, "seg%05d.m4s"),
		filepath.Join(dir, "index.m3u8"))
	// The hls muxer writes the bare-named init.mp4 relative to the process
	// CWD, not the playlist dir — same quirk the worker anchors with cmd.Dir.
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("generate fmp4 segments: %v", err)
	}
	seg := filepath.Join(dir, "seg00000.m4s")
	if _, err := os.Stat(seg); err != nil {
		t.Fatalf("expected seg00000.m4s: %v", err)
	}
	dead, err := SegmentChromaDead(ctx, seg, filepath.Join(dir, "init.mp4"))
	if err != nil {
		t.Fatalf("fmp4 with init: %v", err)
	}
	if dead {
		t.Error("healthy fmp4 fragment must not read as dead")
	}

	// Without the init segment the fragment is undecodable — that must
	// surface as an ERROR (fail open at the caller), never as a verdict.
	if _, err := SegmentChromaDead(ctx, seg, ""); err == nil {
		t.Error("bare .m4s without init should error, not return a verdict")
	}
}
