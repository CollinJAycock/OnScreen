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

// receiverAudioEncode decides whether audio Start would otherwise encode to
// AAC (a remux or a re-encode whose track can't be copied) is encoded for the
// client's receiver instead: E-AC-3 when its audioPassthrough lists eac3,
// else AC-3 when it lists ac3, at transcode.DolbyEncodeChannels(sourceCh)
// channels and transcode.DolbyEncodeKbps. AAC can't be bitstreamed, so a
// TrueHD track on a client that asked for MPEG-TS segments (Roku), or on an
// old fleet, otherwise reaches a home theater with no surround at all.
//
// codec is "" (AAC as before) for a stereo or unknown-layout source (no
// surround to gain, and AAC is what every client decodes), an audio-only
// source (music and audiobooks keep their own path), under a bitrate limit
// (the same notion as the passthrough copy), and when no worker advertises
// the audio_dolby_encode feature. Both codecs ride MPEG-TS and fMP4, so the
// segment container is left alone.
func receiverAudioEncode(caps transcode.ClientCapabilities, sourceCh int,
	sourceAudioOnly, bitrateLimited bool, fleetHasFeature func() bool,
) (codec string, channels int) {
	if sourceAudioOnly || bitrateLimited || sourceCh <= 2 {
		return "", 0
	}
	switch {
	case caps.PassesThroughAudio("eac3"):
		codec = "eac3"
	case caps.PassesThroughAudio("ac3"):
		codec = "ac3"
	default:
		return "", 0
	}
	if !fleetHasFeature() {
		return "", 0
	}
	return codec, transcode.DolbyEncodeChannels(sourceCh)
}

// passthroughReason is the Now Playing line for a re-encode whose audio is
// copied for the client's receiver.
func passthroughReason(codec string) string {
	return transcode.AudioCodecLabel(codec) + " audio passed through to the receiver"
}

// receiverEncodeReason is the Now Playing line for audio encoded to E-AC-3
// / AC-3 for the client's receiver (receiverAudioEncode).
func receiverEncodeReason(src, codec string) string {
	if src == "" {
		return "Audio converted to " + transcode.AudioCodecLabel(codec) + " for the receiver"
	}
	return transcode.AudioCodecLabel(src) + " audio converted to " + transcode.AudioCodecLabel(codec) + " for the receiver"
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
