package v1

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// atom builds an 8-byte ISOBMFF header (size 8, no body).
func atom(typ string) []byte { return append([]byte{0, 0, 0, 8}, []byte(typ)...) }

func TestItemHandler_Faststart(t *testing.T) {
	dir := t.TempDir()
	fast := filepath.Join(dir, "fast.mp4")
	if err := os.WriteFile(fast, append(atom("ftyp"), atom("moov")...), 0o644); err != nil {
		t.Fatal(err)
	}
	slow := filepath.Join(dir, "slow.mp4")
	if err := os.WriteFile(slow, append(atom("ftyp"), atom("mdat")...), 0o644); err != nil {
		t.Fatal(err)
	}

	h := &ItemHandler{} // nil store → defaults to mediastore.Local

	if !h.faststart(context.Background(), fast) {
		t.Error("moov-before-mdat .mp4 should be faststart")
	}
	if h.faststart(context.Background(), slow) {
		t.Error("mdat-before-moov .mp4 should not be faststart")
	}
	if !h.faststart(context.Background(), filepath.Join(dir, "x.mkv")) {
		t.Error("non-MP4 container should short-circuit to true without opening")
	}
	if !h.faststart(context.Background(), filepath.Join(dir, "missing.mp4")) {
		t.Error("unreadable file should assume faststart (true), not block playback")
	}
}
