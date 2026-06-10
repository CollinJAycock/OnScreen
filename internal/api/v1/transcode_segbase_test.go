package v1

import (
	"testing"

	"github.com/onscreen/onscreen/internal/config"
)

// TestSegBase guards against the regression where segBase() called itself
// (infinite recursion → stack-overflow crash) on the ABR playback path. The
// non-nil-cfg case is the one that would have crashed under the old bug.
func TestSegBase(t *testing.T) {
	// nil cfg (test-handler path) → empty.
	if got := (&NativeTranscodeHandler{}).segBase(); got != "" {
		t.Fatalf("nil cfg: want empty, got %q", got)
	}
	// non-nil cfg → the configured base URL (NOT a recursive call).
	h := &NativeTranscodeHandler{cfg: &config.Config{PublicSegmentBaseURL: "https://cdn.example.com"}}
	if got := h.segBase(); got != "https://cdn.example.com" {
		t.Fatalf("segBase: want https://cdn.example.com, got %q", got)
	}
	// non-nil cfg with no base configured → empty (same-origin).
	if got := (&NativeTranscodeHandler{cfg: &config.Config{}}).segBase(); got != "" {
		t.Fatalf("empty base: want empty, got %q", got)
	}
}
