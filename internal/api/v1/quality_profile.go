package v1

import (
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

// QualityProfile is a user's own playback-quality choice, saved with PUT
// /users/me/quality-profile. Zero fields mean no preference. Unlike the
// admin's stream caps — limits placed on a user, which at home only clamp a
// transcode that happens anyway — this is what the viewer asked for, so it
// applies on every network and turns a direct play over it into a transcode.
type QualityProfile struct {
	// MaxVideoBitrateKbps lowers the video bitrate cap.
	MaxVideoBitrateKbps int
	// MaxVideoHeight is a resolution ceiling: a 16:9 box of this height
	// (720 → 1280×720), so a scope film at 1280×536 still fits 720p.
	MaxVideoHeight int
	// MaxAudioBitrateKbps caps the AAC bitrate of every transcode, and makes
	// an audio-only source above it transcode instead of direct-playing.
	MaxAudioBitrateKbps int
	// PreferredVideoCodec ("h264" / "hevc", "" = automatic) picks a
	// transcode's output codec where the client decodes it.
	PreferredVideoCodec string
}

// ReasonQualityProfile is the playback-decision reason for a direct play
// turned into a transcode by the viewer's own quality profile.
const ReasonQualityProfile = "quality_profile"

func minPositive(a, b int) int {
	if b > 0 && (a <= 0 || b < a) {
		return b
	}
	return a
}

// applyTo folds the resolution ceiling into the client's capabilities, so the
// decision, the output size and the ABR ladder all respect it.
func (q QualityProfile) applyTo(caps transcode.ClientCapabilities) transcode.ClientCapabilities {
	if q.MaxVideoHeight > 0 {
		caps.MaxHeight = minPositive(caps.MaxHeight, q.MaxVideoHeight)
		caps.MaxWidth = minPositive(caps.MaxWidth, q.MaxVideoHeight*16/9)
	}
	return caps
}

// withQuality lowers the cap to the viewer's video bitrate, for video only.
func (c streamCap) withQuality(q QualityProfile, file *media.File) streamCap {
	if q.MaxVideoBitrateKbps <= 0 || file == nil || file.VideoCodec == nil || *file.VideoCodec == "" {
		return c
	}
	if c.Kbps == 0 || q.MaxVideoBitrateKbps < c.Kbps {
		c.Kbps = q.MaxVideoBitrateKbps
		c.RemoteBinding = false
		c.QualityBinding = true
	}
	return c
}

// audioOver reports whether an audio-only source is above the viewer's audio
// bitrate (unknown bitrate = not over).
func (q QualityProfile) audioOver(file *media.File) bool {
	return q.MaxAudioBitrateKbps > 0 && file != nil && file.Bitrate != nil &&
		int(*file.Bitrate/1000) > q.MaxAudioBitrateKbps
}

// overQuality reports whether direct-playing file would exceed the profile:
// a video over the video bitrate, or an audio-only source over the audio
// bitrate. (The resolution ceiling reaches the decision through applyTo.)
func (q QualityProfile) overQuality(file *media.File) bool {
	if file == nil {
		return false
	}
	if file.VideoCodec != nil && *file.VideoCodec != "" {
		return q.MaxVideoBitrateKbps > 0 && file.Bitrate != nil && int(*file.Bitrate/1000) > q.MaxVideoBitrateKbps
	}
	return q.audioOver(file)
}

// minKbpsPerChannel is the AAC bitrate per channel below which a
// multichannel encode sounds worse than a stereo one at the same total.
const minKbpsPerChannel = 40

// audioChannels lowers a multichannel AAC output to stereo when the audio
// bitrate can't give each channel minKbpsPerChannel (96 kbps of 5.1 is
// 16 kbps a channel; as stereo it is 48).
func (q QualityProfile) audioChannels(channels int) int {
	if q.MaxAudioBitrateKbps > 0 && channels > 2 && q.MaxAudioBitrateKbps/channels < minKbpsPerChannel {
		return 2
	}
	return channels
}

// aacKbps is the AAC bitrate a transcode with this many output channels
// uses: the channel-count default, held to the audio bitrate. 0 (the
// worker's own default) when the profile sets no audio limit.
func (q QualityProfile) aacKbps(channels int) int {
	if q.MaxAudioBitrateKbps <= 0 {
		return 0
	}
	return min(q.MaxAudioBitrateKbps, transcode.AACBitrateKbps(channels))
}

// outputCodec applies the codec preference to Start's output choice: "h264"
// turns HEVC and AV1 output off; "hevc" asks for HEVC whenever the client
// decodes it and the video is re-encoded.
func (q QualityProfile) outputCodec(caps transcode.ClientCapabilities, videoCopy, preferHEVC, preferAV1 bool) (bool, bool) {
	switch q.PreferredVideoCodec {
	case "h264":
		return false, false
	case "hevc":
		return caps.SupportsHEVC && !videoCopy, false
	}
	return preferHEVC, preferAV1
}
