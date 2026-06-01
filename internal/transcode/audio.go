package transcode

import "encoding/json"

// maxAACChannels caps AAC output at 7.1. AAC-LC handles up to this cleanly;
// sources with more channels are downmixed to it by the encoder.
const maxAACChannels = 8

// SourceAudioChannels returns the channel count of the selected audio stream
// (audioStreamIdx; -1 = the first / default stream), parsed from a file's
// audio_streams JSONB. Returns 0 when the array is missing, unparseable, or
// the index is out of range — callers treat 0 as "unknown".
func SourceAudioChannels(audioStreamsJSON []byte, audioStreamIdx int) int {
	if len(audioStreamsJSON) == 0 {
		return 0
	}
	var streams []struct {
		Channels int `json:"channels"`
	}
	if err := json.Unmarshal(audioStreamsJSON, &streams); err != nil {
		return 0
	}
	i := audioStreamIdx
	if i < 0 {
		i = 0 // default stream is the first audio stream
	}
	if i < 0 || i >= len(streams) {
		return 0
	}
	return streams[i].Channels
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
