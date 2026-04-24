package transcode

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestFindPreviousKeyframe synthesizes a clip with a known 4 s GOP and
// asks for a keyframe near a non-aligned time. The probe should snap
// back to the previous keyframe boundary, not return the request as-is.
func TestFindPreviousKeyframe(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "gop4.mp4")
	// 25 fps × 4 s GOP = keyframes at 0, 4, 8, 12, … s.
	if out, err := exec.Command("ffmpeg", "-v", "error",
		"-f", "lavfi", "-i", "testsrc=duration=20:size=320x240:rate=25",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-g", "100", "-keyint_min", "100", "-sc_threshold", "0",
		"-y", src,
	).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg synth failed: %v: %s", err, out)
	}

	// Asking for 11.5 s should snap back to the keyframe at 8 s.
	got := FindPreviousKeyframe(context.Background(), src, 11.5)
	if math.Abs(got-8.0) > 0.5 {
		t.Errorf("FindPreviousKeyframe(11.5) = %f, want ~8.0", got)
	}
}

// TestFindPreviousKeyframe_Zero short-circuits without invoking ffprobe
// — start-of-file resumes don't need a keyframe lookup.
func TestFindPreviousKeyframe_Zero(t *testing.T) {
	if got := FindPreviousKeyframe(context.Background(), "/nonexistent", 0); got != 0 {
		t.Errorf("FindPreviousKeyframe(0) = %f, want 0", got)
	}
}

// TestFindPreviousKeyframe_MissingFile falls back to the requested time
// rather than failing the session startup. Source quirks shouldn't
// crash playback.
func TestFindPreviousKeyframe_MissingFile(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}
	got := FindPreviousKeyframe(context.Background(), "/nonexistent.mp4", 60)
	if got != 60 {
		t.Errorf("FindPreviousKeyframe(missing, 60) = %f, want 60 (fallback)", got)
	}
}
