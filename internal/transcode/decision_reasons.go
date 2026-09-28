package transcode

import (
	"fmt"
	"strings"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// ReasonKind says which part of the stream a Reason is about, so a caller can
// keep only the ones relevant to what it actually did — a remux copies the
// video, so a video-side reason can't be why a remux happened.
type ReasonKind string

const (
	ReasonFile      ReasonKind = "file"
	ReasonVideo     ReasonKind = "video"
	ReasonAudio     ReasonKind = "audio"
	ReasonContainer ReasonKind = "container"
)

// Reason is one human-readable incompatibility between a file and a client,
// e.g. "HEVC not supported by client" or "7.1 audio → client max 5.1". Shown
// to admins on the Now Playing cards; not a machine contract.
type Reason struct {
	Kind ReasonKind
	Text string
}

// DecideWithReasons is Decide plus the reasons the verdict isn't DirectPlay.
// The Decision is exactly Decide's (this never changes a verdict); reasons are
// nil for DirectPlay and may be empty when the verdict hinges on something
// DecisionReasons doesn't describe.
func DecideWithReasons(file media.File, caps ClientCapabilities, serverCaps ServerCaps) (Decision, []string) {
	d := Decide(file, caps, serverCaps)
	if d == DecisionDirectPlay {
		return d, nil
	}
	return d, ReasonTexts(DecisionReasons(file, caps))
}

// ReasonTexts flattens reasons to their display text.
func ReasonTexts(rs []Reason) []string {
	if len(rs) == 0 {
		return nil
	}
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Text
	}
	return out
}

// DecisionReasons lists every property of file that a client with caps can't
// play as-is — all of them, not just the first one Decide stopped at, so a
// card can say both "HEVC not supported by client" and "7.1 audio → client
// max 5.1". Empty when the file direct-plays.
//
// Codec support is read the way the transcode Start path resolves it: the
// declared codec list, or the SupportsHEVC / SupportsAV1 flags a per-request
// body override can set without touching the list.
func DecisionReasons(file media.File, caps ClientCapabilities) []Reason {
	if file.IntegrityStatus == media.IntegrityDamaged {
		return []Reason{{ReasonFile, "File is damaged"}}
	}
	var out []Reason
	add := func(k ReasonKind, format string, args ...any) {
		out = append(out, Reason{k, fmt.Sprintf(format, args...)})
	}

	videoAlias := canonicalVideoCodec(deref(file.VideoCodec))
	audioAlias := CanonicalAudioCodec(deref(file.AudioCodec))
	containerAlias := canonicalContainer(deref(file.Container))
	hdrType := deref(file.HDRType)
	audioOnly := videoAlias == "" && audioAlias != ""

	if !audioOnly && videoAlias != "" {
		switch {
		case strings.EqualFold(hdrType, "dolby_vision") && !caps.SupportsDV:
			add(ReasonVideo, "Dolby Vision not supported by client")
		case isHDR(hdrType) && !clientSupportsHDR(caps, hdrType):
			add(ReasonVideo, "%s not supported by client (tonemapped to SDR)", hdrLabel(hdrType))
		}
		if !videoCodecSupported(caps, videoAlias) {
			add(ReasonVideo, "%s not supported by client", videoCodecLabel(videoAlias))
		}
		if bd := derefInt(file.VideoBitDepth); bd >= 10 {
			switch videoAlias {
			case "h264":
				add(ReasonVideo, "%d-bit H.264 not decodable by client", bd)
			case "h265":
				if caps.MaxVideoBitDepth < bd {
					add(ReasonVideo, "%d-bit HEVC → client max %d-bit", bd, caps.MaxVideoBitDepth)
				}
			}
		}
		w, h := derefInt(file.ResolutionW), derefInt(file.ResolutionH)
		if (w > 0 && caps.MaxWidth > 0 && w > caps.MaxWidth) ||
			(h > 0 && caps.MaxHeight > 0 && h > caps.MaxHeight) {
			add(ReasonVideo, "%s exceeds client max %s", dims(w, h), dims(caps.MaxWidth, caps.MaxHeight))
		}
	}

	audioBad, containerBad := false, false
	if audioAlias != "" {
		srcCh := SourceAudioChannels(file.AudioStreams, -1)
		switch {
		case !caps.SupportsAudioCodec(audioAlias):
			audioBad = true
			add(ReasonAudio, "%s audio not supported by client", audioCodecLabel(audioAlias))
		case caps.MaxAudioChannels > 0 && srcCh > caps.MaxAudioChannels:
			audioBad = true
			add(ReasonAudio, "%s audio → client max %s", ChannelLabel(srcCh), ChannelLabel(caps.MaxAudioChannels))
		}
	}
	if containerAlias != "" && !caps.SupportsContainer(containerAlias) {
		containerBad = true
		add(ReasonContainer, "%s container not supported by client", containerLabel(containerAlias))
	}
	// Decodable video that still has to be re-encoded because HLS can't carry
	// it (VP9, MPEG-4 Part 2…) once the audio or container needs adapting.
	if (audioBad || containerBad) && !audioOnly && videoAlias != "" && videoCodecSupported(caps, videoAlias) &&
		videoAlias != "h264" && videoAlias != "h265" && videoAlias != "av1" {
		add(ReasonVideo, "%s can't be remuxed for streaming", videoCodecLabel(videoAlias))
	}
	return out
}

func videoCodecSupported(caps ClientCapabilities, alias string) bool {
	if caps.SupportsVideoCodec(alias) {
		return true
	}
	switch alias {
	case "h265":
		return caps.SupportsHEVC || caps.SupportsVideoCodec("hevc")
	case "av1":
		return caps.SupportsAV1
	}
	return false
}

// ChannelLabel renders a channel count the way people say it: 8 → "7.1",
// 6 → "5.1", 2 → "stereo", 1 → "mono".
func ChannelLabel(ch int) string {
	switch ch {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 3:
		return "2.1"
	case 6:
		return "5.1"
	case 7:
		return "6.1"
	case 8:
		return "7.1"
	}
	return fmt.Sprintf("%d-channel", ch)
}

// VideoCodecLabel is the display name for a codec as ffprobe or a client
// names it ("hevc" → "HEVC").
func VideoCodecLabel(codec string) string { return videoCodecLabel(canonicalVideoCodec(codec)) }

// AudioCodecLabel is the display name for an audio codec ("eac3" → "E-AC-3").
func AudioCodecLabel(codec string) string { return audioCodecLabel(CanonicalAudioCodec(codec)) }

func videoCodecLabel(alias string) string {
	switch alias {
	case "h264":
		return "H.264"
	case "h265":
		return "HEVC"
	case "av1":
		return "AV1"
	case "vp9":
		return "VP9"
	case "vp8":
		return "VP8"
	case "mpeg4":
		return "MPEG-4"
	case "mpeg2video":
		return "MPEG-2"
	case "vc1":
		return "VC-1"
	}
	return strings.ToUpper(alias)
}

func audioCodecLabel(alias string) string {
	switch alias {
	case "aac":
		return "AAC"
	case "ac3":
		return "AC-3"
	case "eac3":
		return "E-AC-3"
	case "truehd":
		return "TrueHD"
	case "dts":
		return "DTS"
	case "mp3":
		return "MP3"
	case "flac":
		return "FLAC"
	case "vorbis":
		return "Vorbis"
	case "opus":
		return "Opus"
	}
	return strings.ToUpper(alias)
}

func containerLabel(alias string) string {
	switch alias {
	case "mkv":
		return "MKV"
	case "mp4":
		return "MP4"
	case "avi":
		return "AVI"
	case "ts":
		return "MPEG-TS"
	}
	return strings.ToUpper(alias)
}

func hdrLabel(hdrType string) string {
	switch strings.ToLower(hdrType) {
	case "hdr10":
		return "HDR10"
	case "hdr10plus":
		return "HDR10+"
	case "hlg":
		return "HLG"
	}
	return "HDR"
}

func dims(w, h int) string {
	switch {
	case w > 0 && h > 0:
		return fmt.Sprintf("%d×%d", w, h)
	case h > 0:
		return fmt.Sprintf("%dp", h)
	}
	return fmt.Sprintf("%dpx wide", w)
}
