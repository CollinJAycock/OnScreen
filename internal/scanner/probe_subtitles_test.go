package scanner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The file's default subtitle track reaches the API (SubtitleStreamJSON.Default),
// so a client with no language preference can show it, as ExoPlayer does from
// the same flag: an anime release whose dialogue track is marked default
// opened with subtitles on Fire TV and without them on Samsung.
func TestProbeFile_SubtitleDefaultDisposition(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	srt := filepath.Join(dir, "a.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "x.mkv")
	cmd := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "color=c=black:s=64x64:d=1",
		"-i", srt, "-i", srt,
		"-map", "0:v", "-map", "1:s", "-map", "2:s",
		"-c:v", "libx264", "-c:s", "srt",
		"-metadata:s:s:0", "title=Signs", "-metadata:s:s:1", "title=Dialogue",
		"-disposition:s:0", "0", "-disposition:s:1", "default",
		out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not build the fixture: %v\n%s", err, b)
	}

	res, err := ProbeFile(context.Background(), out)
	if err != nil {
		t.Fatalf("ProbeFile: %v", err)
	}
	var subs []struct {
		Title   string `json:"title"`
		Default bool   `json:"default"`
	}
	if err := json.Unmarshal(res.SubtitleStreams, &subs); err != nil {
		t.Fatalf("subtitle streams: %v (%s)", err, res.SubtitleStreams)
	}
	if len(subs) != 2 {
		t.Fatalf("got %d subtitle streams, want 2: %s", len(subs), res.SubtitleStreams)
	}
	if subs[0].Default || !subs[1].Default {
		t.Errorf("default flags = [%s:%v %s:%v], want only Dialogue default",
			subs[0].Title, subs[0].Default, subs[1].Title, subs[1].Default)
	}
}
