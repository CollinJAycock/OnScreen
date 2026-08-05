package transcode

import (
	"strings"
	"testing"
)

// ── audio-only sources ───────────────────────────────────────────────────────

// An audio-only source (music in an undeclared container, .dsf/.ape, an
// audiobook) must build a playable audio HLS session. The pipeline used to
// hard-map `0:v:0`, so ffmpeg died instantly on every such file — the decision
// engine deliberately routes them here, and the audit found the client-side
// container allowlists were the only thing papering over it.
func TestBuildHLS_AudioOnly(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/album/track.dsf",
		Encoder:       EncoderCopy, // remux verdict — irrelevant to audio-only
		AudioOnly:     true,
		AudioCodec:    "aac",
		AudioChannels: 2,
		SessionDir:    "/tmp/s",
		SegmentPrefix: "seg",
	})
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "-map 0:v:0") {
		t.Errorf("audio-only args map a video stream — ffmpeg dies on 'matches no streams':\n%s", joined)
	}
	if strings.Contains(joined, "-c:v") {
		t.Errorf("audio-only args carry a video codec:\n%s", joined)
	}
	if !strings.Contains(joined, "-map 0:a:0") {
		t.Errorf("audio-only args missing the audio map:\n%s", joined)
	}
	if !strings.Contains(joined, "seg%05d.ts") || strings.Contains(joined, "-tag:v") {
		t.Errorf("audio-only output should be untagged MPEG-TS:\n%s", joined)
	}
	// Audio encodes far outrun real time; EVENT keeps hls.js at segment 0
	// instead of chasing the live edge.
	if !strings.Contains(joined, "-hls_playlist_type event") {
		t.Errorf("audio-only playlist should be EVENT:\n%s", joined)
	}
}

// ── audio-less sources ───────────────────────────────────────────────────────

// The mirror image: an audio-less video (screen recording, timelapse) must
// not map an audio stream it doesn't have.
func TestBuildHLS_NoAudio(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath: "/media/timelapse.mp4",
		Encoder:   EncoderSoftware,
		NoAudio:   true,
		Width:     1280, Height: 720, BitrateKbps: 4000,
		SessionDir: "/tmp/s", SegmentPrefix: "seg",
	})
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "-map 0:a") {
		t.Errorf("no-audio args map an audio stream — ffmpeg dies on 'matches no streams':\n%s", joined)
	}
	if strings.Contains(joined, "-c:a") {
		t.Errorf("no-audio args carry an audio codec:\n%s", joined)
	}
	if !strings.Contains(joined, "-map 0:v:0") {
		t.Errorf("no-audio args missing the video map:\n%s", joined)
	}
}

// ── bit-depth ceiling ────────────────────────────────────────────────────────

// A bit-depth-forced transcode must not hand back the property that forced
// it: with Force8Bit, the HEVC encoder path strips to yuv420p, and the
// libx265 Main-profile pin (valid only for 8-bit input) is applied.
func TestBuildHLS_Force8Bit_HEVC(t *testing.T) {
	base := BuildArgs{
		InputPath: "/media/anime_10bit.mkv",
		Encoder:   EncoderHEVCSoftware,
		IsHEVC:    true,
		Width:     1920, Height: 1080, BitrateKbps: 8000,
		AudioCodec: "aac", SessionDir: "/tmp/s", SegmentPrefix: "seg",
	}

	forced := base
	forced.Force8Bit = true
	joined := strings.Join(BuildHLS(forced), " ")
	if !strings.Contains(joined, "format=yuv420p") {
		t.Errorf("Force8Bit HEVC encode does not strip to 8-bit — an 8-bit-only "+
			"client gets Main 10 output, the exact property that forced the transcode:\n%s", joined)
	}
	if !strings.Contains(joined, "-profile:v main") {
		t.Errorf("Force8Bit x265 should pin Main profile (input is 8-bit-stripped):\n%s", joined)
	}

	// WITHOUT Force8Bit — a 10-bit-capable client — the Main pin must be gone:
	// libx265 REFUSES 10-bit input under `-profile:v main` and died at encoder
	// init, killing the software last-resort of every HEVC fallback chain on
	// the most common HEVC content.
	joined = strings.Join(BuildHLS(base), " ")
	if strings.Contains(joined, "-profile:v main") {
		t.Errorf("unforced x265 pins Main profile — 10-bit input dies at encoder init:\n%s", joined)
	}
	if strings.Contains(joined, "format=yuv420p") {
		t.Errorf("unforced HEVC encode should preserve source depth (Main 10):\n%s", joined)
	}
}

// ── audio passthrough on remux ───────────────────────────────────────────────

// AudioCodec "copy" must pass the source audio through untouched — no
// resample filter, no channel force, no bitrate. The remux path used to
// re-encode client-decodable AAC/AC3 to AAC unconditionally: CPU spent to
// strictly degrade audio.
func TestBuildHLS_AudioCopyPassthrough(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderCopy,
		AudioCodec:    "copy",
		SessionDir:    "/tmp/s",
		SegmentPrefix: "seg",
	})
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-c:a copy") {
		t.Errorf("want -c:a copy:\n%s", joined)
	}
	for _, banned := range []string{"-af ", "-ac ", "-b:a "} {
		if strings.Contains(joined, banned) {
			t.Errorf("audio copy must not carry %q — it forces a re-encode:\n%s", banned, joined)
		}
	}
}

// ── even dimensions ──────────────────────────────────────────────────────────

// Encoders require even dimensions; the source cap copies the source's exact
// size, so odd-dimension sources flowed odd numbers into scale/pad and the
// encoder failed at init on every Auto transcode of that file.
func TestSelectQuality_OddSourceDimensionsRoundedEven(t *testing.T) {
	q := SelectQuality(0, 0, 0, 1279, 719, ServerCaps{})
	if q.MaxWidth%2 != 0 || q.MaxHeight%2 != 0 {
		t.Errorf("odd source dims passed through: %dx%d — encoder init death", q.MaxWidth, q.MaxHeight)
	}
	if q.MaxWidth != 1278 || q.MaxHeight != 718 {
		t.Errorf("expected round-DOWN (never exceed source): got %dx%d", q.MaxWidth, q.MaxHeight)
	}
	// Odd CLIENT caps get the same treatment.
	q = SelectQuality(0, 1281, 721, 3840, 2160, ServerCaps{})
	if q.MaxWidth%2 != 0 || q.MaxHeight%2 != 0 {
		t.Errorf("odd client caps passed through: %dx%d", q.MaxWidth, q.MaxHeight)
	}
}

// ── decision-rule refreshes ──────────────────────────────────────────────────

// AV1 remux: the fMP4 remux path carries AV1, so an AV1-decoding client with
// a container/audio mismatch gets a stream copy, not a full re-encode of
// pristine AV1.
func TestDecide_AV1RemuxAllowed(t *testing.T) {
	file := baseFile()
	file.VideoCodec = strPtr("av1")
	file.AudioCodec = strPtr("flac")
	caps := ParseCapabilities("videoDecoder=h264:av1,audioDecoder=aac,protocols=mp4")
	if got := Decide(file, caps, ServerCaps{}); got != DecisionDirectStream {
		t.Errorf("AV1 source for an AV1 client = %v, want directStream — refusing it "+
			"re-encodes pristine AV1 on a stale pre-fMP4 rationale", got)
	}
}

// "mpegts" is what ffprobe reports for .ts; a client declaring ts support
// used to never match it, so every .ts file remuxed into MPEG-TS.
func TestDecide_MpegtsContainerMatches(t *testing.T) {
	file := baseFile()
	file.Container = strPtr("mpegts")
	caps := ParseCapabilities("videoDecoder=h264,audioDecoder=aac,protocols=ts")
	if got := Decide(file, caps, ServerCaps{}); got != DecisionDirectPlay {
		t.Errorf("h264/aac in mpegts for a ts-capable client = %v, want directPlay", got)
	}
}
