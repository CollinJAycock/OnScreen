//go:build integration

// Integration test for the static-ABR encoder: needs a real ffmpeg. Generates a
// tiny clip, pre-encodes its ladder through a Local store, and asserts the
// master/rung playlists, segments, and hash sidecar all land. Run with:
//
//	go test -tags=integration ./internal/preencode/...
package preencode

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/mediastore"
	"github.com/onscreen/onscreen/internal/staticabr"
)

func TestEncoder_Integration_FFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	ctx := context.Background()

	// Generate a 2s 480p clip with audio.
	src := filepath.Join(t.TempDir(), "clip.mp4")
	gen := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=640x480:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-shortest", "-pix_fmt", "yuv420p", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate clip: %v: %s", err, out)
	}

	root := t.TempDir() // local static root (object storage would use "")
	store := mediastore.Local{}
	enc := New(store, root, "ffmpeg", 1 /*1s segments*/, 0, discardLogger())

	fileID := uuid.New()
	source := staticabr.Source{
		FileID: fileID, FilePath: src, Hash: "abc123",
		Width: 640, Height: 480, BitrateKbps: 1500, Codec: "h264",
	}
	if err := enc.Encode(ctx, source); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	read := func(relKey string) string {
		f, err := store.Open(ctx, filepath.Join(root, relKey))
		if err != nil {
			t.Fatalf("Open %s: %v", relKey, err)
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		return string(b)
	}

	// Master playlist references rung playlists.
	master := read(staticabr.MasterKey(fileID))
	if !strings.Contains(master, "#EXT-X-STREAM-INF") || !strings.Contains(master, "index.m3u8") {
		t.Errorf("master playlist malformed:\n%s", master)
	}
	// Hash sidecar matches the source hash (so StoreChecker considers it fresh).
	if h := strings.TrimSpace(read(staticabr.HashKey(fileID))); h != "abc123" {
		t.Errorf("hash sidecar = %q, want abc123", h)
	}

	// At least one rung playlist + one segment landed.
	var segs, playlists int
	if err := store.Walk(ctx, filepath.Join(root, staticabr.Prefix(fileID)), func(o mediastore.ObjectInfo) error {
		switch {
		case strings.HasSuffix(o.Key, ".ts"):
			segs++
		case strings.HasSuffix(o.Key, "index.m3u8"):
			playlists++
		}
		return nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if segs == 0 {
		t.Error("no segments were written")
	}
	if playlists == 0 {
		t.Error("no rung playlist was written")
	}
}
