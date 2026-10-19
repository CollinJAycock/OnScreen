package v1

import (
	"math"

	"github.com/onscreen/onscreen/internal/transcode"
)

// transcodeAudioPassthrough decides the audio of a re-encode (the video is
// converted) for a client that bitstreams codecs to a receiver
// (audioPassthrough): copyAudio reports the mapped track is copied instead of
// encoded to AAC, fmp4 that it is TrueHD or DTS, which only fMP4 carries to
// a player (TranscodeJob.AudioPassthrough: fMP4 + -strict -2, routed to a
// worker with the audio_passthrough feature).
//
// AAC stays the answer when the client doesn't list the codec, for an
// audio-only source (no picture is re-encoded; its own rules apply), under a
// bitrate limit (a lossless track alone can be several Mbps), for a
// server-side speed change (atempo needs decoded audio), and for TrueHD/DTS
// when the client asked for MPEG-TS segments or no worker has the feature.
// A subtitle burn-in doesn't stop the copy: it only touches the picture.
func transcodeAudioPassthrough(alias string, caps transcode.ClientCapabilities,
	sourceAudioOnly, tsSegments, bitrateLimited bool, audioRate float64, fleetHasFeature func() bool,
) (copyAudio, fmp4 bool) {
	if sourceAudioOnly || bitrateLimited || !caps.PassesThroughAudio(alias) {
		return false, false
	}
	if audioRate > 0 && math.Abs(audioRate-1) >= 0.005 {
		return false, false
	}
	switch transcode.CanonicalAudioCodec(alias) {
	case "ac3", "eac3": // valid in both MPEG-TS and fMP4
		return true, false
	case "truehd", "dts":
		if !tsSegments && fleetHasFeature() {
			return true, true
		}
	}
	return false, false
}

// passthroughReason is the Now Playing line for a re-encode whose audio is
// copied for the client's receiver.
func passthroughReason(codec string) string {
	return transcode.AudioCodecLabel(codec) + " audio passed through to the receiver"
}

// fitOutputLevel shrinks a re-encode's output box and bitrate to what a
// codec's level (tenths; 0 = none) allows at the source frame rate. The box
// keeps the source's aspect ratio, which the software scaler pads to.
func fitOutputLevel(codec string, level, srcW, srcH int, fps float64, width, height, kbps int) (int, int, int) {
	if level <= 0 {
		return width, height, kbps
	}
	if maxH := transcode.LevelMaxHeight(codec, level, srcW, srcH, fps); maxH > 0 && (height == 0 || height > maxH) {
		height = maxH
		if srcW > 0 && srcH > 0 {
			w := int(math.Round(float64(maxH) * float64(srcW) / float64(srcH)))
			w += w & 1
			if width == 0 || w < width {
				width = w
			}
		}
	}
	if mk := transcode.LevelMaxKbps(codec, level); mk > 0 && (kbps == 0 || kbps > mk) {
		kbps = mk
	}
	return width, height, kbps
}
