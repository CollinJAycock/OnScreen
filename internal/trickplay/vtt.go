package trickplay

import (
	"fmt"
	"strings"
)

// Spec describes the geometry of a trickplay sprite set: interval between
// thumbnails, thumbnail dimensions, and grid size per sprite sheet. The same
// spec is used both to drive ffmpeg and to write the VTT index.
type Spec struct {
	IntervalSec int
	ThumbWidth  int
	ThumbHeight int
	GridCols    int
	GridRows    int
}

// Default matches the values persisted in trickplay_status defaults and is
// what the generator uses unless an admin overrides.
var Default = Spec{
	IntervalSec: 10,
	ThumbWidth:  320,
	ThumbHeight: 180,
	GridCols:    10,
	GridRows:    10,
}

// ThumbsPerSprite is the number of thumbnails packed into one sprite sheet.
func (s Spec) ThumbsPerSprite() int { return s.GridCols * s.GridRows }

// SpriteCount returns how many sprite sheets are needed to cover durationSec
// of content at the spec's interval.
func (s Spec) SpriteCount(durationSec int) int {
	if durationSec <= 0 || s.IntervalSec <= 0 {
		return 0
	}
	thumbs := durationSec / s.IntervalSec
	if durationSec%s.IntervalSec != 0 {
		thumbs++
	}
	per := s.ThumbsPerSprite()
	count := thumbs / per
	if thumbs%per != 0 {
		count++
	}
	return count
}

// WriteVTT composes a WebVTT trickplay index for a video of durationSec
// seconds. Each cue covers one interval and points to a region inside one of
// the spriteNames files via the #xywh fragment. spriteNames must have length
// >= SpriteCount(durationSec); extra entries are ignored.
//
// The WebVTT spec allows an image URL with xywh fragment in the cue payload;
// HLS.js and most web players read this format natively.
func (s Spec) WriteVTT(durationSec int, spriteNames []string) (string, error) {
	if s.IntervalSec <= 0 || s.ThumbWidth <= 0 || s.ThumbHeight <= 0 ||
		s.GridCols <= 0 || s.GridRows <= 0 {
		return "", fmt.Errorf("trickplay: invalid spec %+v", s)
	}
	need := s.SpriteCount(durationSec)
	if need > len(spriteNames) {
		return "", fmt.Errorf("trickplay: have %d sprites, need %d for %ds", len(spriteNames), need, durationSec)
	}

	var b strings.Builder
	b.WriteString("WEBVTT\n\n")

	per := s.ThumbsPerSprite()
	for i := 0; ; i++ {
		start := i * s.IntervalSec
		if start >= durationSec {
			break
		}
		end := start + s.IntervalSec
		if end > durationSec {
			end = durationSec
		}

		spriteIdx := i / per
		within := i % per
		col := within % s.GridCols
		row := within / s.GridCols
		x := col * s.ThumbWidth
		y := row * s.ThumbHeight

		fmt.Fprintf(&b, "%s --> %s\n", formatVTTTime(start), formatVTTTime(end))
		fmt.Fprintf(&b, "%s#xywh=%d,%d,%d,%d\n\n",
			spriteNames[spriteIdx], x, y, s.ThumbWidth, s.ThumbHeight)
	}
	return b.String(), nil
}

// formatVTTTime renders seconds as HH:MM:SS.000 for WebVTT cues.
func formatVTTTime(sec int) string {
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	return fmt.Sprintf("%02d:%02d:%02d.000", h, m, s)
}
