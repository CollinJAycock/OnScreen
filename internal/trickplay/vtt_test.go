package trickplay

import (
	"strings"
	"testing"
)

func TestSpecSpriteCount(t *testing.T) {
	s := Default // 10s interval, 10x10 grid = 100 thumbs/sprite = 1000s per sheet
	tests := []struct {
		durationSec int
		want        int
	}{
		{0, 0},
		{9, 1},     // one partial thumb still needs a sheet
		{1000, 1},  // exactly fills one sheet (thumbs at 0..990 = 100 thumbs)
		{1001, 2},  // one extra thumb pushes to second sheet
		{5400, 6},  // 90 min → 540 thumbs → 6 sheets (ceil)
	}
	for _, tt := range tests {
		if got := s.SpriteCount(tt.durationSec); got != tt.want {
			t.Errorf("SpriteCount(%d) = %d, want %d", tt.durationSec, got, tt.want)
		}
	}
}

func TestWriteVTTCueLayout(t *testing.T) {
	s := Default
	names := []string{"sprite_0.jpg", "sprite_1.jpg"}
	// 120s → 12 thumbs at 10s intervals, all on sprite_0 (positions 0..11).
	out, err := s.WriteVTT(120, names)
	if err != nil {
		t.Fatalf("WriteVTT: %v", err)
	}
	if !strings.HasPrefix(out, "WEBVTT\n\n") {
		head := 20
		if len(out) < head {
			head = len(out)
		}
		t.Errorf("missing WEBVTT header: %q", out[:head])
	}

	// First cue: time 0–10, top-left of sprite 0.
	if !strings.Contains(out, "00:00:00.000 --> 00:00:10.000\nsprite_0.jpg#xywh=0,0,320,180") {
		t.Errorf("first cue layout wrong:\n%s", out)
	}
	// 11th thumb (index 10) → row 1, col 0 → y=180.
	if !strings.Contains(out, "00:01:40.000 --> 00:01:50.000\nsprite_0.jpg#xywh=0,180,320,180") {
		t.Errorf("11th cue should be row 1 col 0:\n%s", out)
	}
	// 12th thumb (index 11) → row 1, col 1.
	if !strings.Contains(out, "00:01:50.000 --> 00:02:00.000\nsprite_0.jpg#xywh=320,180,320,180") {
		t.Errorf("12th cue should be row 1 col 1:\n%s", out)
	}
}

func TestWriteVTTSpansMultipleSprites(t *testing.T) {
	s := Default // 100 thumbs per sheet
	names := []string{"a.jpg", "b.jpg"}
	// 1020s → 102 thumbs → sprite 0 holds 0..99, sprite 1 holds 100..101.
	out, err := s.WriteVTT(1020, names)
	if err != nil {
		t.Fatalf("WriteVTT: %v", err)
	}
	// Thumb 100 (time 1000) must reference b.jpg at top-left.
	if !strings.Contains(out, "00:16:40.000 --> 00:16:50.000\nb.jpg#xywh=0,0,320,180") {
		t.Errorf("thumb 100 should cross to sprite b:\n%s", out)
	}
}

func TestWriteVTTLastCueClampedToDuration(t *testing.T) {
	s := Default
	names := []string{"s.jpg"}
	// 25s with 10s interval → thumbs at 0,10,20. Last cue should end at 25, not 30.
	out, err := s.WriteVTT(25, names)
	if err != nil {
		t.Fatalf("WriteVTT: %v", err)
	}
	if !strings.Contains(out, "00:00:20.000 --> 00:00:25.000\n") {
		t.Errorf("last cue should clamp to duration:\n%s", out)
	}
	if strings.Contains(out, "00:00:30.000") {
		t.Errorf("cue should not extend past duration:\n%s", out)
	}
}

func TestWriteVTTNotEnoughSprites(t *testing.T) {
	s := Default
	// 2000s needs 2 sheets; only 1 provided.
	_, err := s.WriteVTT(2000, []string{"only.jpg"})
	if err == nil {
		t.Fatal("expected error when sprite names are insufficient")
	}
}

func TestWriteVTTRejectsInvalidSpec(t *testing.T) {
	bad := Spec{IntervalSec: 0, ThumbWidth: 320, ThumbHeight: 180, GridCols: 10, GridRows: 10}
	if _, err := bad.WriteVTT(60, []string{"a.jpg"}); err == nil {
		t.Fatal("zero interval should error")
	}
}

