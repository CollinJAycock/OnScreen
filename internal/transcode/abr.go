package transcode

import (
	"fmt"
	"math"
	"strings"
)

// Adaptive-bitrate (ABR) HLS ladder. The single-rendition path picks one
// QualityProfile (see quality.go); ABR instead emits SEVERAL renditions
// and a master playlist so the player's own logic (hls.js, AVPlay, …)
// switches rungs on real-time bandwidth.
//
// This file is the pure computation: turn a source's resolution +
// bitrate into a set of Renditions, and turn those into the master
// `#EXT-X-STREAM-INF` playlist. The orchestration that actually runs an
// ffmpeg per rendition lives elsewhere.

// Rendition is one rung of the ABR ladder: an encode target the player
// can switch to.
type Rendition struct {
	Label       string // "1080p", "720p", … — also the variant subdir name
	Height      int    // encoded height (never above source)
	Width       int    // encoded width, source aspect, rounded even
	BitrateKbps int    // video target (HEVC-scaled + source-capped)
}

// ladderTiers are the candidate rungs, highest first. Heights are the
// standard HLS ladder; bitrates are H.264 reference values (HEVC scales
// down via ScaleBitrateForHEVC). Mirrors quality.go's Profiles but as a
// clean per-height ladder for multi-rendition emission.
var ladderTiers = []struct {
	Height      int
	BitrateKbps int
}{
	{2160, 40000},
	{1440, 16000},
	{1080, 8000},
	{720, 4000},
	{480, 1500},
	{360, 800},
}

// audioAllowanceKbps is added to each rung's video bitrate when
// advertising BANDWIDTH so the master playlist's peak estimate covers
// the muxed audio track too.
const audioAllowanceKbps = 160

// BuildLadder computes the ABR renditions for a source. Rungs above the
// source height are dropped (never upscale); rungs above maxHeightCap
// (an operator pin; 0 = no cap) are dropped; each rung's bitrate is
// HEVC-scaled when hevc and never exceeds the source bitrate. There is
// always at least one rung — a tiny source collapses to a single
// source-resolution rendition.
//
// Returned highest-first, matching how players and the master playlist
// conventionally list variants.
func BuildLadder(sourceW, sourceH, sourceBitrateKbps int, hevc bool, maxHeightCap int) []Rendition {
	if sourceH <= 0 {
		sourceH = 1080
	}
	aspect := 16.0 / 9.0
	if sourceW > 0 && sourceH > 0 {
		aspect = float64(sourceW) / float64(sourceH)
	}

	out := make([]Rendition, 0, len(ladderTiers))
	for _, t := range ladderTiers {
		if t.Height > sourceH {
			continue // never upscale
		}
		if maxHeightCap > 0 && t.Height > maxHeightCap {
			continue // operator pinned the ceiling lower
		}
		out = append(out, renditionAt(t.Height, t.BitrateKbps, aspect, hevc, sourceBitrateKbps))
	}

	// Source smaller than the lowest standard rung (e.g. a 240p clip):
	// emit a single rendition at the source resolution so playback still
	// works rather than returning an empty ladder.
	if len(out) == 0 {
		br := ladderTiers[len(ladderTiers)-1].BitrateKbps
		out = append(out, renditionAt(sourceH, br, aspect, hevc, sourceBitrateKbps))
	}
	return out
}

func renditionAt(height, refBitrateKbps int, aspect float64, hevc bool, sourceBitrateKbps int) Rendition {
	br := refBitrateKbps
	if hevc {
		br = ScaleBitrateForHEVC(br)
	}
	if sourceBitrateKbps > 0 && br > sourceBitrateKbps {
		br = sourceBitrateKbps
	}
	w := int(math.Round(float64(height) * aspect))
	if w%2 != 0 {
		w++ // H.264/HEVC need even dimensions
	}
	return Rendition{
		Label:       fmt.Sprintf("%dp", height),
		Height:      height,
		Width:       w,
		BitrateKbps: br,
	}
}

// BuildMasterPlaylist renders the ABR master m3u8: one
// #EXT-X-STREAM-INF per rendition (highest BANDWIDTH first) pointing at
// each rung's media playlist. variantPath maps a rendition to the
// relative URL of its media playlist (e.g. "1080p/playlist.m3u8" plus
// whatever auth/segment-token query the caller appends).
//
// BANDWIDTH is the rung's video bitrate + an audio allowance, in bits/s,
// per the HLS spec's "peak segment bit rate" field — players use it to
// pick the highest rung the measured throughput can sustain.
func BuildMasterPlaylist(rends []Rendition, variantPath func(Rendition) string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:6\n")
	for _, r := range rends {
		bandwidth := (r.BitrateKbps + audioAllowanceKbps) * 1000
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\n", bandwidth, r.Width, r.Height)
		b.WriteString(variantPath(r))
		b.WriteString("\n")
	}
	return b.String()
}
