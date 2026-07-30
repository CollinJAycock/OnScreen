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

// Ladder codecs — passed to BuildLadder to scale each rung's reference
// (H.264) bitrate for the chosen output codec's efficiency.
const (
	LadderH264 = "h264"
	LadderHEVC = "hevc"
	LadderAV1  = "av1"
)

// BuildLadder computes the ABR renditions for a source. Rungs above the
// source height are dropped (never upscale); rungs above maxHeightCap
// (an operator pin; 0 = no cap) are dropped; each rung's bitrate is
// scaled for the output codec (HEVC/AV1 more efficient than H.264) and
// never exceeds the source bitrate. There is always at least one rung —
// a tiny source collapses to a single source-resolution rendition.
//
// Returned highest-first, matching how players and the master playlist
// conventionally list variants.
// maxBitrateKbps applies the administered bitrate ceiling (per-user stream cap,
// or the server-wide TRANSCODE_MAX_BITRATE). 0 means unrestricted.
//
// Rungs are CLAMPED to the ceiling, not dropped, so a low cap can never produce
// an empty ladder; rungs that collapse onto the same bitrate are then deduped
// keeping the highest resolution, which preserves something for the player to
// adapt across instead of N identical-bandwidth variants.
func BuildLadder(sourceW, sourceH, sourceBitrateKbps int, codec string, maxHeightCap, maxBitrateKbps int) []Rendition {
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
		out = append(out, renditionAt(t.Height, t.BitrateKbps, aspect, codec, sourceBitrateKbps, maxBitrateKbps))
	}

	// Source smaller than the lowest standard rung (e.g. a 240p clip):
	// emit a single rendition at the source resolution so playback still
	// works rather than returning an empty ladder.
	if len(out) == 0 {
		br := ladderTiers[len(ladderTiers)-1].BitrateKbps
		out = append(out, renditionAt(sourceH, br, aspect, codec, sourceBitrateKbps, maxBitrateKbps))
	}
	return dedupeByBitrate(out)
}

// dedupeByBitrate drops rungs that clamped onto a bitrate an earlier (taller)
// rung already occupies. Input is highest-first, so the first occurrence is the
// highest resolution at that bitrate — the one worth keeping, since at equal
// bandwidth more pixels is strictly better.
func dedupeByBitrate(in []Rendition) []Rendition {
	if len(in) < 2 {
		return in
	}
	seen := make(map[int]struct{}, len(in))
	out := in[:0:0]
	for _, r := range in {
		if _, dup := seen[r.BitrateKbps]; dup {
			continue
		}
		seen[r.BitrateKbps] = struct{}{}
		out = append(out, r)
	}
	return out
}

// scaleBitrateForCodec scales a reference H.264 bitrate for the output codec.
func scaleBitrateForCodec(h264Bitrate int, codec string) int {
	switch codec {
	case LadderHEVC:
		return ScaleBitrateForHEVC(h264Bitrate)
	case LadderAV1:
		return ScaleBitrateForAV1(h264Bitrate)
	default:
		return h264Bitrate
	}
}

func renditionAt(height, refBitrateKbps int, aspect float64, codec string, sourceBitrateKbps, maxBitrateKbps int) Rendition {
	br := scaleBitrateForCodec(refBitrateKbps, codec)
	if sourceBitrateKbps > 0 && br > sourceBitrateKbps {
		br = sourceBitrateKbps
	}
	// The administered ceiling. The single-rendition path has always honoured
	// it via SelectQuality; the ladder took its bitrates straight from the
	// fixed tier table, so switching a user to Auto quality on an ABR server
	// silently voided their cap — a 2000 kbps user got a 1080p@8000 rung.
	if maxBitrateKbps > 0 && br > maxBitrateKbps {
		br = maxBitrateKbps
	}
	w := int(math.Round(float64(height) * aspect))
	if w%2 != 0 {
		w++ // H.264/HEVC/AV1 need even dimensions
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
// codecs, when non-empty, is emitted as the CODECS attribute on every
// variant (e.g. `hvc1.1.6.L153.B0,mp4a.40.2`). HEVC players often won't
// even attempt a variant without it; H.264 players probe fine, so the
// caller may pass "" there.
//
// BANDWIDTH is the rung's video bitrate + an audio allowance, in bits/s,
// per the HLS spec's "peak segment bit rate" field — players use it to
// pick the highest rung the measured throughput can sustain.
func BuildMasterPlaylist(rends []Rendition, codecs string, variantPath func(Rendition) string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:6\n")
	for _, r := range rends {
		bandwidth := (r.BitrateKbps + audioAllowanceKbps) * 1000
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d", bandwidth, r.Width, r.Height)
		if codecs != "" {
			fmt.Fprintf(&b, ",CODECS=%q", codecs)
		}
		b.WriteByte('\n')
		b.WriteString(variantPath(r))
		b.WriteString("\n")
	}
	return b.String()
}

// HEVCMasterCodecs returns the CODECS attribute value for an HEVC fMP4
// ladder: Main-profile hvc1 plus AAC-LC audio. The level is sized to the
// tallest rung so it covers every variant (over-advertising the level on
// the smaller rungs is harmless — players only reject when the advertised
// level is BELOW what the stream needs). L153 = level 5.1 (handles 4K).
func HEVCMasterCodecs(maxHeight int) string {
	level := "L123" // 4.1 — up to 1080p60
	switch {
	case maxHeight > 2160:
		level = "L180" // 6.0 — up to 8K
	case maxHeight > 1080:
		level = "L153" // 5.1 — up to 4K
	}
	return "hvc1.1.6." + level + ".B0,mp4a.40.2"
}

// AV1MasterCodecs returns the CODECS attribute value for an AV1 fMP4
// ladder: Main-profile av01 (8-bit, our SDR yuv420p output) plus AAC-LC.
// The seq_level_idx is sized to the tallest rung — 4.0 (1080p), 5.1 (4K),
// 6.0 (8K) — over-advertising on smaller rungs, which players tolerate.
func AV1MasterCodecs(maxHeight int) string {
	level := "08" // 4.0 — up to 1080p
	switch {
	case maxHeight > 2160:
		level = "16" // 6.0 — up to 8K
	case maxHeight > 1080:
		level = "13" // 5.1 — up to 4K
	}
	return "av01.0." + level + "M.08,mp4a.40.2"
}
