package transcode

import (
	"fmt"
	"strings"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/i18n"
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
// in English, e.g. "HEVC not supported by client" or "7.1 audio → client max
// 5.1". Shown to admins on the Now Playing cards; not a machine contract.
// ReasonMsg is the same reason before it is put in any language.
type Reason struct {
	Kind ReasonKind
	Text string
}

// ReasonMsg is a Reason as a message, written later in the language of
// whoever reads it (ReasonMessage.Text).
type ReasonMsg struct {
	Kind ReasonKind
	Msg  ReasonMessage
}

// ReasonMessage is one Now Playing line as data: a catalog key (reason.…, in
// internal/i18n/locales/*/transcode.json) and the values for its
// placeholders. Sessions store reasons this way because they are worked out
// when a stream starts but read later, by an admin whose language may not be
// the viewer's.
//
// Params are written out already and never translated: codec and format
// names, "3840×2160", "5.1", "Main 10", "25 Mbps". Terms are values that are
// words themselves ("stereo", "4-channel"), each a message of its own.
type ReasonMessage struct {
	Key    string                   `json:"key"`
	Params map[string]string        `json:"params,omitempty"`
	Terms  map[string]ReasonMessage `json:"terms,omitempty"`
}

// NewReasonMessage is the message for key with its values, given as name,
// value pairs. A value is a ReasonMessage (a term) or anything else, which is
// written as fmt.Sprint writes it: an int in plain digits, never grouped
// ("3840", not "3,840").
func NewReasonMessage(key string, nameValues ...any) ReasonMessage {
	m := ReasonMessage{Key: key}
	for i := 0; i+1 < len(nameValues); i += 2 {
		name := fmt.Sprint(nameValues[i])
		if t, ok := nameValues[i+1].(ReasonMessage); ok {
			if m.Terms == nil {
				m.Terms = map[string]ReasonMessage{}
			}
			m.Terms[name] = t
			continue
		}
		if m.Params == nil {
			m.Params = map[string]string{}
		}
		m.Params[name] = fmt.Sprint(nameValues[i+1])
	}
	return m
}

// Text is m written in l.
func (m ReasonMessage) Text(l i18n.Lang) string {
	p := make(i18n.Params, len(m.Params)+len(m.Terms))
	for k, v := range m.Params {
		p[k] = v
	}
	for k, t := range m.Terms {
		p[k] = t.Text(l)
	}
	return i18n.T(l, m.Key, p)
}

// String is m in English: the text Now Playing showed before it was
// translated, which logs and TranscodeReasons keep.
func (m ReasonMessage) String() string { return m.Text(i18n.English) }

// known reports whether this build's catalog has m's key and its terms'. A
// session a newer node stored may name one it doesn't.
func (m ReasonMessage) known() bool {
	if !i18n.Has(m.Key) {
		return false
	}
	for _, t := range m.Terms {
		if !t.known() {
			return false
		}
	}
	return true
}

// MessageTexts writes ms in l.
func MessageTexts(ms []ReasonMessage, l i18n.Lang) []string {
	if len(ms) == 0 {
		return nil
	}
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Text(l)
	}
	return out
}

// SetTranscodeReasons stores the Now Playing reasons on s: the messages,
// which ReasonsIn writes in the reader's language, and their English in
// TranscodeReasons, which logs and nodes from before translations read.
func (s *Session) SetTranscodeReasons(ms []ReasonMessage) {
	s.TranscodeReasonMessages = ms
	s.TranscodeReasons = MessageTexts(ms, i18n.English)
}

// ReasonsIn is the session's Now Playing reasons written in l. A session
// stored before translations has only the English text, which is shown as it
// is, and so is a reason whose key this build doesn't know.
func (s *Session) ReasonsIn(l i18n.Lang) []string {
	ms := s.TranscodeReasonMessages
	if len(ms) == 0 || len(ms) != len(s.TranscodeReasons) {
		return s.TranscodeReasons
	}
	out := make([]string, len(ms))
	for i, m := range ms {
		if m.known() {
			out[i] = m.Text(l)
		} else {
			out[i] = s.TranscodeReasons[i]
		}
	}
	return out
}

// DecideWithReasons is Decide plus the reasons the verdict isn't DirectPlay,
// in English. The Decision is exactly Decide's (this never changes a
// verdict); reasons are nil for DirectPlay and may be empty when the verdict
// hinges on something DecisionReasons doesn't describe.
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
// max 5.1". Empty when the file direct-plays. In English; DecisionReasonMsgs
// has them as messages.
//
// Codec support is read the way the transcode Start path resolves it: the
// declared codec list, or the SupportsHEVC / SupportsAV1 flags a per-request
// body override can set without touching the list.
func DecisionReasons(file media.File, caps ClientCapabilities) []Reason {
	ms := DecisionReasonMsgs(file, caps)
	if len(ms) == 0 {
		return nil
	}
	out := make([]Reason, len(ms))
	for i, r := range ms {
		out[i] = Reason{r.Kind, r.Msg.String()}
	}
	return out
}

// DecisionReasonMsgs is DecisionReasons as messages, for a caller that keeps
// them to be read later in another language (transcode Start).
func DecisionReasonMsgs(file media.File, caps ClientCapabilities) []ReasonMsg {
	if file.IntegrityStatus == media.IntegrityDamaged {
		return []ReasonMsg{{ReasonFile, NewReasonMessage("reason.fileDamaged")}}
	}
	var out []ReasonMsg
	add := func(k ReasonKind, key string, nameValues ...any) {
		out = append(out, ReasonMsg{k, NewReasonMessage(key, nameValues...)})
	}

	videoAlias := canonicalVideoCodec(deref(file.VideoCodec))
	audioAlias := CanonicalAudioCodec(deref(file.AudioCodec))
	containerAlias := canonicalContainer(deref(file.Container))
	hdrType := deref(file.HDRType)
	audioOnly := videoAlias == "" && audioAlias != ""

	if !audioOnly && videoAlias != "" {
		switch {
		case strings.EqualFold(hdrType, "dolby_vision") && !caps.SupportsDVProfile("5"):
			add(ReasonVideo, "reason.dolbyVisionProfile5")
		case isHDR(hdrType) && !clientSupportsHDR(caps, hdrType):
			add(ReasonVideo, "reason.hdrTonemapped", "hdr", hdrLabel(hdrType))
		}
		if !videoCodecSupported(caps, videoAlias) {
			add(ReasonVideo, "reason.videoCodec", "codec", videoCodecLabel(videoAlias))
		}
		if bd := derefInt(file.VideoBitDepth); bd >= 10 {
			switch videoAlias {
			case "h264":
				add(ReasonVideo, "reason.h264BitDepth", "depth", bd)
			case "h265":
				if caps.MaxVideoBitDepth < bd {
					add(ReasonVideo, "reason.bitDepthOverMax",
						"depth", bd, "codec", videoCodecLabel(videoAlias), "max", caps.MaxVideoBitDepth)
				}
			case "vp9":
				if caps.MaxVP9BitDepth > 0 && caps.MaxVP9BitDepth < bd {
					add(ReasonVideo, "reason.bitDepthOverMax",
						"depth", bd, "codec", videoCodecLabel(videoAlias), "max", caps.MaxVP9BitDepth)
				}
			}
		}
		if profileOver, levelOver := videoLimitExceeded(file, caps); profileOver || levelOver {
			lim := caps.VideoLimitFor(videoAlias)
			if profileOver {
				add(ReasonVideo, "reason.profileOverMax", "codec", videoCodecLabel(videoAlias),
					"profile", deref(file.VideoProfile), "max", profileLabel(videoAlias, lim.Profile))
			}
			if levelOver {
				add(ReasonVideo, "reason.levelOverMax", "codec", videoCodecLabel(videoAlias),
					"level", FormatLevel(VideoLevelTenths(videoAlias, derefInt(file.VideoLevel))),
					"max", FormatLevel(lim.Level))
			}
		}
		w, h := derefInt(file.ResolutionW), derefInt(file.ResolutionH)
		if (w > 0 && caps.MaxWidth > 0 && w > caps.MaxWidth) ||
			(h > 0 && caps.MaxHeight > 0 && h > caps.MaxHeight) {
			add(ReasonVideo, "reason.resolutionOverMax",
				"resolution", dimsValue(w, h), "max", dimsValue(caps.MaxWidth, caps.MaxHeight))
		}
	}

	audioBad, containerBad := false, false
	if audioAlias != "" {
		srcCh := SourceAudioChannels(file.AudioStreams, -1)
		// A passed-through codec is the receiver's to decode, so neither the
		// client's decoder list nor its channel cap is a reason (as in Decide).
		passthrough := caps.PassesThroughAudio(audioAlias)
		switch {
		case !caps.SupportsAudioCodec(audioAlias) && !passthrough:
			audioBad = true
			add(ReasonAudio, "reason.audioCodec", "codec", audioCodecLabel(audioAlias))
		case !containerCarriesAudio(containerAlias, audioAlias):
			audioBad = true
			add(ReasonAudio, "reason.audioInContainer",
				"codec", audioCodecLabel(audioAlias), "container", containerLabel(containerAlias))
		case !passthrough && caps.MaxAudioChannels > 0 && srcCh > caps.MaxAudioChannels:
			audioBad = true
			add(ReasonAudio, "reason.audioChannelsOverMax",
				"channels", channelValue(srcCh), "max", channelValue(caps.MaxAudioChannels))
		}
	}
	if containerAlias != "" && !caps.SupportsContainer(containerAlias) {
		containerBad = true
		add(ReasonContainer, "reason.container", "container", containerLabel(containerAlias))
	}
	// Decodable video that still has to be re-encoded because HLS can't carry
	// it (VP9, MPEG-4 Part 2…) once the audio or container needs adapting.
	if (audioBad || containerBad) && !audioOnly && videoAlias != "" && videoCodecSupported(caps, videoAlias) &&
		videoAlias != "h264" && videoAlias != "h265" && videoAlias != "av1" {
		add(ReasonVideo, "reason.videoNotRemuxable", "codec", videoCodecLabel(videoAlias))
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

// ChannelLabel renders a channel count the way people say it, in English:
// 8 → "7.1", 6 → "5.1", 2 → "stereo", 1 → "mono".
func ChannelLabel(ch int) string { return valueText(channelValue(ch), i18n.English) }

// channelValue is a channel count as a message value: the layout in digits
// ("5.1"), which no language translates, or a term for the words ("stereo",
// "4-channel").
func channelValue(ch int) any {
	switch ch {
	case 1:
		return NewReasonMessage("reason.layout.mono")
	case 2:
		return NewReasonMessage("reason.layout.stereo")
	case 3:
		return "2.1"
	case 6:
		return "5.1"
	case 7:
		return "6.1"
	case 8:
		return "7.1"
	}
	return NewReasonMessage("reason.layout.channels", "channels", ch)
}

// valueText writes a message value (channelValue, dimsValue) in l.
func valueText(v any, l i18n.Lang) string {
	if m, ok := v.(ReasonMessage); ok {
		return m.Text(l)
	}
	return fmt.Sprint(v)
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

// dimsValue is a resolution as a message value: "3840×2160" or "2160p",
// never translated, or a term for a width alone ("3840px wide").
func dimsValue(w, h int) any {
	switch {
	case w > 0 && h > 0:
		return fmt.Sprintf("%d×%d", w, h)
	case h > 0:
		return fmt.Sprintf("%dp", h)
	}
	return NewReasonMessage("reason.size.width", "width", w)
}
