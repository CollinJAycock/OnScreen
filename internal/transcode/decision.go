package transcode

import (
	"strings"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// Decision is the play decision for a given file + client combination.
type Decision int

const (
	// DecisionDirectPlay — serve the file as-is. Zero server CPU.
	DecisionDirectPlay Decision = iota
	// DecisionDirectStream — remux container only. Audio/video pass through.
	DecisionDirectStream
	// DecisionTranscode — full video (and optionally audio) transcode.
	DecisionTranscode
)

func (d Decision) String() string {
	switch d {
	case DecisionDirectPlay:
		return "directPlay"
	case DecisionDirectStream:
		return "directStream"
	default:
		return "transcode"
	}
}

// Decide returns the optimal play decision for a media file + client capabilities.
// Decision order (ADR-016):
//  1. Client supports container + all streams → DirectPlay
//  2. Client supports all streams but not container → DirectStream
//  3. Otherwise → Transcode
//
// HDR handling (ADR-030): HDR content forces Transcode if the client doesn't
// support HDR, even when it would otherwise DirectStream.
func Decide(file media.File, caps ClientCapabilities, serverCaps ServerCaps) Decision {
	videoCodec := deref(file.VideoCodec)
	audioCodec := deref(file.AudioCodec)
	container := deref(file.Container)
	hdrType := deref(file.HDRType)

	// Resolve canonical codec names to what clients advertise.
	videoAlias := canonicalVideoCodec(videoCodec)
	audioAlias := canonicalAudioCodec(audioCodec)
	containerAlias := canonicalContainer(container)

	// Audio-only files (no video stream but a known audio codec) skip the
	// video check; otherwise the empty videoAlias would never match a client
	// codec and music would always fall through to Transcode.
	audioOnly := videoAlias == "" && audioAlias != ""
	clientSupportsVideo := audioOnly || caps.SupportsVideoCodec(videoAlias)
	// Audio is playable as-is only if the client decodes the codec AND can
	// render the channel layout. A source with more channels than the client's
	// cap (e.g. 7.1 to a 5.1/stereo client) can't direct-play or remux — the
	// layout is fixed in the bitstream, and most decoders reject >5.1 AAC
	// outright (the 7.1-AAC browser finding) — so it must transcode to downmix.
	// The downmix target is chosen separately by TargetAudioChannels.
	srcChannels := SourceAudioChannels(file.AudioStreams, -1)
	channelsFit := caps.MaxAudioChannels <= 0 || srcChannels <= 0 || srcChannels <= caps.MaxAudioChannels
	clientSupportsAudio := audioAlias == "" || (caps.SupportsAudioCodec(audioAlias) && channelsFit)
	clientSupportsContainer := caps.SupportsContainer(containerAlias)

	// HDR check: if source is HDR and client doesn't support it, must transcode.
	if isHDR(hdrType) && !clientSupportsHDR(caps, hdrType) {
		return DecisionTranscode
	}

	// Bit-depth check: a 10-bit (or deeper) video stream the client can't decode
	// must transcode — otherwise the client direct-plays/remuxes and silently
	// fails. Codec-aware, mirroring the web client's videoBitDepthOK: H.264 Hi10P
	// is undecodable by browsers regardless of any declared depth; HEVC Main 10
	// needs a declared 10-bit capability; AV1/VP9 10-bit decode tracks their
	// 8-bit support, so it isn't gated here. Uses VideoBitDepth (the video
	// stream's depth), not BitDepth (audio). Both DirectPlay and DirectStream
	// preserve source bit depth, so only a re-encode fixes it.
	if bd := derefInt(file.VideoBitDepth); bd >= 10 {
		switch videoAlias {
		case "h264":
			return DecisionTranscode // Hi10P — no browser decodes it
		case "h265":
			if caps.MaxVideoBitDepth < 10 {
				return DecisionTranscode // Main 10 on an 8-bit-only decoder
			}
		}
	}

	// Resolution check: if source exceeds client's declared max, must transcode.
	w := derefInt(file.ResolutionW)
	h := derefInt(file.ResolutionH)
	if (w > 0 && caps.MaxWidth > 0 && w > caps.MaxWidth) ||
		(h > 0 && caps.MaxHeight > 0 && h > caps.MaxHeight) {
		return DecisionTranscode
	}

	// If client supports everything → DirectPlay.
	if clientSupportsVideo && clientSupportsAudio && clientSupportsContainer {
		return DecisionDirectPlay
	}

	// If client supports streams but not container → DirectStream (remux).
	if clientSupportsVideo && clientSupportsAudio {
		return DecisionDirectStream
	}

	// Otherwise must transcode.
	return DecisionTranscode
}

// ServerCaps holds server-level transcode limits (from hot-reloadable config).
type ServerCaps struct {
	MaxBitrateKbps int
	MaxWidth       int
	MaxHeight      int
}

// canonicalVideoCodec normalises codec names from ffprobe to standard identifiers.
func canonicalVideoCodec(codec string) string {
	switch strings.ToLower(codec) {
	case "h264", "avc", "avc1":
		return "h264"
	case "hevc", "h265", "hvc1":
		return "h265"
	case "av1":
		return "av1"
	case "vp9":
		return "vp9"
	case "mpeg4":
		return "mpeg4"
	default:
		return strings.ToLower(codec)
	}
}

func canonicalAudioCodec(codec string) string {
	switch strings.ToLower(codec) {
	case "aac":
		return "aac"
	case "ac3", "ac-3":
		return "ac3"
	case "eac3", "e-ac-3", "eac-3":
		return "eac3"
	case "truehd":
		return "truehd"
	case "dts", "dts-hd", "dtshd":
		return "dts"
	case "mp3", "mp2":
		return "mp3"
	case "flac":
		return "flac"
	case "vorbis":
		return "vorbis"
	case "opus":
		return "opus"
	default:
		return strings.ToLower(codec)
	}
}

func canonicalContainer(container string) string {
	switch strings.ToLower(container) {
	case "matroska", "mkv":
		return "mkv"
	case "mp4", "m4v", "mov", "isom":
		return "mp4"
	case "avi":
		return "avi"
	case "ts", "mpeg-ts":
		return "ts"
	default:
		return strings.ToLower(container)
	}
}

func isHDR(hdrType string) bool {
	switch strings.ToLower(hdrType) {
	case "hdr10", "hdr10plus", "hlg", "dolby_vision":
		return true
	}
	return false
}

func clientSupportsHDR(caps ClientCapabilities, hdrType string) bool {
	switch strings.ToLower(hdrType) {
	case "dolby_vision":
		return caps.SupportsDV
	case "hdr10", "hdr10plus", "hlg":
		return caps.SupportsHDR
	}
	return false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}
