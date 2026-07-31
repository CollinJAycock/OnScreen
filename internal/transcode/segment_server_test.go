package transcode

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withSegmentDir points segmentBaseDir at a temp dir for the duration of a test.
func withSegmentDir(t *testing.T) string {
	t.Helper()
	prev := segmentBaseDir
	dir := t.TempDir()
	segmentBaseDir = dir
	t.Cleanup(func() { segmentBaseDir = prev })
	return dir
}

// A segment ffmpeg has not reached yet is TRANSIENT. It used to answer 404,
// which an HLS player reads as "this will never exist" — so instead of
// retrying, the player skips the segment and leaves a gap. That is what made a
// slow transcode alternate picture and black on screen. Every other not-ready
// path in the tree (playlist, ABR init, ABR segment) already says 503.
func TestSegmentServer_NotYetEncodedIs503(t *testing.T) {
	withSegmentDir(t)
	prev := segmentWaitTimeout
	segmentWaitTimeout = 150 * time.Millisecond
	t.Cleanup(func() { segmentWaitTimeout = prev })
	w := &Worker{}

	req := httptest.NewRequest("GET", "/segments/sess-1/seg00007.ts", nil)
	rec := httptest.NewRecorder()
	start := time.Now()
	w.segmentHandler()(rec, req)
	elapsed := time.Since(start)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("not-yet-encoded segment answered 404 after %v — the player "+
			"treats that as permanently gone and skips it, leaving black", elapsed)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Error("no Retry-After on the 503; the player has no back-off hint")
	}
}

// A segment that exists must still be served normally.
func TestSegmentServer_ExistingSegmentIsServed(t *testing.T) {
	dir := withSegmentDir(t)
	sess := filepath.Join(dir, "sess-2")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte{0x47, 0x40, 0x00, 0x10} // TS sync byte + a few
	if err := os.WriteFile(filepath.Join(sess, "seg00000.ts"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Worker{}

	rec := httptest.NewRecorder()
	w.segmentHandler()(rec, httptest.NewRequest("GET", "/segments/sess-2/seg00000.ts", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if got := rec.Body.Bytes(); len(got) != len(payload) || got[0] != 0x47 {
		t.Errorf("body: got %v, want the TS payload", got)
	}
}
