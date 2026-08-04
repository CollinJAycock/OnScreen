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
	// DecisionDirectStream — stream-copy the video into a client-supported
	// container (MPEG-TS / fMP4) and transcode the audio only if the client
	// can't decode it. The video is never re-encoded. (ADR-016, widened: was
	// "remux container only, both streams pass through" — broadened to the
	// video-copy + audio-transcode path the clients actually use, since copying
	// H.264/HEVC while only re-encoding AC-3/DTS/TrueHD audio is far cheaper than
	// a full re-encode.)
	DecisionDirectStream
	// DecisionTranscode — full video re-encode (codec the client can't decode or
	// can't remux, HDR tonemap, bit-depth reduction, or downscale).
	DecisionTranscode
	// DecisionUnsupported — the content can't be played and must NOT be
	// transcoded either: the result would be broken. Today the only case is
	// Dolby Vision on a client that can't direct-play it — the only correct DV
	// tonemapper (libplacebo) can't initialize on the deployment host, and
	// tonemap_cuda produces the green/IPT cast (see docs/dolby-vision.md). The
	// client surfaces a clear "Dolby Vision is not supported" message instead of
	// playing a mangled stream.
	DecisionUnsupported
)

func (d Decision) String() string {
	switch d {
	case DecisionDirectPlay:
		return "directPlay"
	case DecisionDirectStream:
		return "directStream"
	case DecisionUnsupported:
		return "unsupported"
	default:
		return "transcode"
	}
}

// Decide returns the optimal play decision for a media file + client capabilities.
// Decision order (ADR-016, as widened for the video-copy path):
//  1. Video undecodable / HDR-on-SDR / too-deep / oversized → Transcode (re-encode)
//  2. Video decodable + audio + container all client-supported → DirectPlay
//  3. Video decodable + remux-carriable (h264/hevc, or audio-only) → DirectStream
//     (stream-copy video, adapt container, transcode audio if needed)
//  4. Video decodable but not remux-carriable (AV1/VP9 in a wrong container) → Transcode
//
// HDR handling (ADR-030): HDR content forces Transcode if the client doesn't
// support HDR. Note: faststart (progressive-download readiness) is NOT modeled
// here — it isn't on media.File; clients refine a DirectPlay verdict with their
// own faststart check (see docs/capability-profiles.md).
func Decide(file media.File, caps ClientCapabilities, serverCaps ServerCaps) Decision {
	videoCodec := deref(file.VideoCodec)
	audioCodec := deref(file.AudioCodec)
	container := deref(file.Container)
	hdrType := deref(file.HDRType)

	// Resolve canonical codec names to what clients advertise.
	videoAlias := canonicalVideoCodec(videoCodec)
	audioAlias := CanonicalAudioCodec(audioCodec)
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

	// Dolby Vision: we neither pass it through nor tonemap it. The only correct
	// DV tonemapper (libplacebo apply_dolbyvision) can't init on the deployment
	// host (no Vulkan), and tonemap_cuda mangles the colors — so a client that
	// can't direct-play DV gets an explicit "unsupported" verdict and shows a
	// clear message rather than a broken transcode (see docs/dolby-vision.md). A
	// future DV-capable client (dovi=1) direct-plays and never reaches here.
	if strings.EqualFold(hdrType, "dolby_vision") && !caps.SupportsDV {
		return DecisionUnsupported
	}
	// Other HDR (HDR10 / HDR10+ / HLG): if the client can't display it, tonemap
	// (transcode). These carry a standard base that tonemap_cuda handles fine.
	if isHDR(hdrType) && !clientSupportsHDR(caps, hdrType) {
		return DecisionTranscode
	}

	// Bit-depth check: a video stream deeper than the client's decoder can
	// handle must transcode — otherwise the client direct-plays/remuxes an
	// undecodable bitstream and silently fails (black screen). Compare the
	// SOURCE depth against the client's declared MaxVideoBitDepth, not a fixed
	// constant: a 12-bit HEVC (Main 12) source must transcode for a 10-bit
	// (Main 10) decoder just as a 10-bit source must for an 8-bit one. (Regression
	// fix: Fruits Basket S1E1 is HEVC Main 12 and was being directStream'd to
	// Main-10 clients like Fire TV, which can't decode it.) Codec-aware: H.264
	// Hi10P/Hi12P is undecodable by browsers/most devices regardless of declared
	// depth; AV1/VP9 high-bit-depth decode tracks their 8-bit support so isn't
	// gated here. Uses VideoBitDepth (video stream depth), not BitDepth (audio).
	// Both DirectPlay and DirectStream preserve source depth, so only a re-encode fixes it.
	if bd := derefInt(file.VideoBitDepth); bd >= 10 {
		switch videoAlias {
		case "h264":
			return DecisionTranscode // Hi10P/Hi12P — no browser/device decodes it
		case "h265":
			if caps.MaxVideoBitDepth < bd {
				return DecisionTranscode // e.g. Main 12 source on a Main 10 (or 8-bit) decoder
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

	// Video the client can't decode at all (unsupported codec) → full transcode.
	// (HDR tonemap, bit-depth reduction, and downscale already returned Transcode
	// above — those re-encode a codec the client otherwise supports.)
	if !clientSupportsVideo {
		return DecisionTranscode
	}

	// Video is client-decodable. If audio + container are also fine, play as-is.
	if clientSupportsAudio && clientSupportsContainer {
		return DecisionDirectPlay
	}

	// Video is decodable but the container and/or audio need adapting. Prefer a
	// stream-copy of the video (DirectStream) over a full re-encode — but only
	// when the video can be carried by the HLS remux container. Audio-only files
	// always remux (just the container). H.264 (MPEG-TS), HEVC and AV1 (fMP4 —
	// ResolveVideoOutput routes both to .m4s; the client decodes them, else
	// we'd have transcoded above) are remux-carriable. AV1 used to be refused
	// on the stale "TS can't carry AV1" rationale from before the fMP4 remux
	// path existed, which re-encoded pristine AV1 for clients that decode it
	// natively. VP9 stays out (WebM-only; no HLS carriage). Mirrors the web
	// client's remuxableVideoCodecs.
	if audioOnly || videoAlias == "h264" || videoAlias == "h265" || videoAlias == "av1" {
		return DecisionDirectStream
	}
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

// CanonicalAudioCodec normalises audio codec names from ffprobe to the
// identifiers clients advertise. Exported for the API layer's audio-copy
// eligibility check, which must speak the same aliases Decide does.
func CanonicalAudioCodec(codec string) string {
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
	// "mpegts" is what ffprobe actually reports for a .ts file (its demuxer
	// name); the scanner stores it verbatim. Without it here, a client
	// declaring ts support never matched and every .ts file remuxed into...
	// MPEG-TS.
	case "ts", "mpeg-ts", "mpegts":
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
