package preencode

import (
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/transcode"
)

func TestMasterCodecs(t *testing.T) {
	e := &Encoder{} // masterCodecs uses no receiver state
	if got := e.masterCodecs("h264", 1080); got != h264MasterCodecs {
		t.Errorf("h264: got %q want %q", got, h264MasterCodecs)
	}
	if got := e.masterCodecs("something-else", 1080); got != h264MasterCodecs {
		t.Errorf("unknown codec should fall back to h264, got %q", got)
	}
	if got, want := e.masterCodecs(transcode.LadderHEVC, 1080), transcode.HEVCMasterCodecs(1080); got != want {
		t.Errorf("hevc: got %q want %q", got, want)
	}
	if got, want := e.masterCodecs(transcode.LadderAV1, 2160), transcode.AV1MasterCodecs(2160); got != want {
		t.Errorf("av1: got %q want %q", got, want)
	}
}

func TestTail(t *testing.T) {
	if got := tail([]byte("  hello  "), 100); got != "hello" {
		t.Errorf("short+trim: got %q want hello", got)
	}
	if got := tail([]byte("abcdefghij"), 3); got != "hij" {
		t.Errorf("truncate to last n: got %q want hij", got)
	}
	if got := tail([]byte(""), 5); got != "" {
		t.Errorf("empty: got %q want empty", got)
	}
	if got := tail([]byte("line1\nline2"), 100); !strings.Contains(got, "line1") {
		t.Errorf("multiline tail dropped content: %q", got)
	}
}
