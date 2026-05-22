//go:build integration

// Live-ffmpeg soak for the ABR arg builder. Runs the real BuildABRHLS
// argv against a real source (8 s slice) and asserts ffmpeg produces a
// master playlist + per-variant playlists + segments that exist and are
// non-empty. This is the validation the unit tests can't do — it catches
// filtergraph / -var_stream_map / %v mistakes that only surface when
// ffmpeg actually runs.
//
// Gated on ABR_TEST_INPUT (a path to any video file) so it skips in CI:
//
//	ABR_TEST_INPUT="/c/movies/.../foo.mkv" \
//	  go test -tags=integration -run TestBuildABRHLS_Soak ./internal/transcode/
package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildABRHLS_Soak(t *testing.T) {
	input := os.Getenv("ABR_TEST_INPUT")
	if input == "" {
		t.Skip("set ABR_TEST_INPUT to a video file to run the live-ffmpeg ABR soak")
	}

	dir := t.TempDir()
	ladder := BuildLadder(1280, 720, 0, false, 0) // 720p + 480p + 360p
	if len(ladder) != 3 {
		t.Fatalf("precondition: expected 3 rungs, got %d", len(ladder))
	}
	// ffmpeg's %v does not create the per-variant subdirs — the worker
	// must pre-create them, so the soak mirrors that.
	for i := range ladder {
		if err := os.MkdirAll(filepath.Join(dir, itoa(i)), 0o755); err != nil {
			t.Fatalf("mkdir variant %d: %v", i, err)
		}
	}

	args := BuildABRHLS(BuildArgs{
		InputPath:     input,
		SessionDir:    dir,
		AudioCodec:    "aac",
		AudioChannels: 2,
	}, ladder)

	// Limit the read to 8 s so a feature-length source doesn't encode in
	// full. Insert `-t 8` right after the input so it bounds the decode.
	args = injectAfterInput(args, input, "-t", "8")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg failed: %v\nargs: %v\noutput:\n%s", err, args, out)
	}

	// Master playlist must exist, be non-empty, and list every variant.
	master := mustReadNonEmpty(t, filepath.Join(dir, "master.m3u8"))
	for i := range ladder {
		// Per-variant playlist + at least one segment.
		mustReadNonEmpty(t, filepath.Join(dir, itoa(i), "index.m3u8"))
		seg := filepath.Join(dir, itoa(i), "seg00000.ts")
		if fi, err := os.Stat(seg); err != nil || fi.Size() == 0 {
			t.Errorf("variant %d: first segment missing/empty (%v)", i, err)
		}
	}
	if cnt := countSubstr(master, "#EXT-X-STREAM-INF"); cnt != len(ladder) {
		t.Errorf("master lists %d variants, want %d:\n%s", cnt, len(ladder), master)
	}
	t.Logf("ABR soak OK — master + %d variant ladders produced:\n%s", len(ladder), master)
}

func injectAfterInput(args []string, input, a, b string) []string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-i" && args[i+1] == input {
			out := make([]string, 0, len(args)+2)
			out = append(out, args[:i+2]...)
			out = append(out, a, b)
			out = append(out, args[i+2:]...)
			return out
		}
	}
	return args
}

func mustReadNonEmpty(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(b) == 0 {
		t.Fatalf("%s is empty", path)
	}
	return string(b)
}

func countSubstr(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}
