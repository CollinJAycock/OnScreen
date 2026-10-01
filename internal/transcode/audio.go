package transcode

import (
	"encoding/json"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// maxAACChannels caps transcoded AAC output at 5.1 (6ch). The encoder
// (libfdk_aac) handles 7.1 fine, but most CLIENTS can't DECODE 7.1 AAC —
// browsers via MSE/hls.js reject an 8-channel AAC track outright and playback
// never starts at all (not just muted). Validated on QA: every 7.1 source
// failed to play in-browser while every <=5.1 source played, independent of
// the source codec (TrueHD/DTS/AAC). 5.1 is universally supported; genuine 7.1
// surround is delivered to capable clients via audio passthrough (direct play),
// not this AAC transcode fallback.
const maxAACChannels = 6

// SourceAudioChannels returns the channel count of the selected audio stream
// (audioStreamIdx; -1 = the first / default stream), parsed from a file's
// audio_streams JSONB. Returns 0 when the array is missing, unparseable, or
// the index is out of range — callers treat 0 as "unknown".
func SourceAudioChannels(audioStreamsJSON []byte, audioStreamIdx int) int {
	streams := parseSourceAudioStreams(audioStreamsJSON)
	i := audioStreamIdx
	if i < 0 {
		i = 0 // default stream is the first audio stream
	}
	if i >= len(streams) {
		return 0
	}
	return streams[i].Channels
}

// SourceAudioStreamCount returns how many audio streams a file's
// audio_streams JSONB lists: the valid audio_stream_index values are
// 0..count-1. Returns 0 when the array is missing or unparseable (a row
// scanned before the column existed) — callers treat 0 as "unknown", not as
// "no audio".
func SourceAudioStreamCount(audioStreamsJSON []byte) int {
	return len(parseSourceAudioStreams(audioStreamsJSON))
}

// SourceAudioCodec returns the codec of the audio stream a session maps
// (audioStreamIdx; -1 = the default stream). The default reads the file's
// top-level audio_codec, which the scanner takes from the first audio stream:
// the one `-map 0:a:0` picks. A requested stream reads its own audio_streams
// entry instead, since that array is in ffprobe order, the order `0:a:N`
// counts in. Gating a remux's audio copy on the top-level codec copied a
// selected DTS track behind an AC3 first track straight through to a client
// that can't decode it.
//
// Returns "" when the stream can't be identified (an index past the end, or
// past the first stream on a row with no audio_streams). Callers treat "" as
// "unknown": never copyable.
func SourceAudioCodec(file *media.File, audioStreamIdx int) string {
	first := ""
	if file.AudioCodec != nil {
		first = *file.AudioCodec
	}
	if audioStreamIdx < 0 {
		return first
	}
	streams := parseSourceAudioStreams(file.AudioStreams)
	switch {
	case audioStreamIdx < len(streams):
		return streams[audioStreamIdx].Codec
	case audioStreamIdx == 0 && len(streams) == 0:
		return first // no stream list: stream 0 is still the first stream
	}
	return ""
}

// sourceAudioStream is the slice of an audio_streams JSONB entry the
// transcode path decides on.
type sourceAudioStream struct {
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
}

// parseSourceAudioStreams decodes a file's audio_streams JSONB; nil when the
// array is missing or unparseable.
func parseSourceAudioStreams(audioStreamsJSON []byte) []sourceAudioStream {
	if len(audioStreamsJSON) == 0 {
		return nil
	}
	var streams []sourceAudioStream
	if err := json.Unmarshal(audioStreamsJSON, &streams); err != nil {
		return nil
	}
	return streams
}

// TargetAudioChannels picks the AAC output channel count when audio is being
// transcoded. It preserves the source layout — so a 5.1 / 7.1 track isn't
// collapsed to stereo — capped by the client's declared maximum (when > 0)
// and a hard 7.1 ceiling. Falls back to stereo only when the source channel
// count is unknown (0). Clients that genuinely only output stereo can pass a
// max of 2 to force the downmix; everyone else gets surround preserved and
// lets their OS/AVR downmix as needed.
func TargetAudioChannels(sourceCh, clientMax int) int {
	if sourceCh <= 0 {
		return 2 // unknown source layout — safe stereo default
	}
	ch := sourceCh
	if clientMax > 0 && clientMax < ch {
		ch = clientMax
	}
	if ch > maxAACChannels {
		ch = maxAACChannels
	}
	if ch < 1 {
		ch = 1
	}
	return ch
}

// AACBitrateKbps returns the default AAC bitrate for a channel count when the
// caller hasn't set an explicit one: 128k for mono/stereo, then ~64k per
// channel so a 5.1 mix (→ 384k) / 7.1 (→ 512k) isn't starved the way a flat
// 128k would. Mirrors the per-channel budget Jellyfin uses.
func AACBitrateKbps(channels int) int {
	if channels > 2 {
		return 64 * channels
	}
	return 128
}
