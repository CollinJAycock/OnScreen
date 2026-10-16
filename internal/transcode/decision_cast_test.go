package transcode

import (
	"testing"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// The Chromecast (Default Media Receiver) profiles, exactly as the web sender
// (web/src/lib/cast.ts) and the phone app send them on a cast's
// playback-decision and transcode-start calls. docs/capability-profiles.md,
// "Chromecast (Default Media Receiver)".
const (
	castProfileHD = "videoDecoder=h264,audioDecoder=aac:mp3,protocols=mp4:hls,maxWidth=1920,maxHeight=1080,maxAudioChannels=2,maxbitdepth=8,hdr=0"
	castProfile4K = "videoDecoder=h264:h265,audioDecoder=aac:mp3,protocols=mp4:hls,maxWidth=3840,maxHeight=2160,maxAudioChannels=6,maxbitdepth=10,hdr=1"
)

// Pins what the cast profiles decide for the files people cast. `protocols`
// must list mp4: with only `hls` no container ever matched, so even an MP4
// the receiver plays as-is came back directStream and was remuxed instead of
// cast directly.
func TestDecide_ChromecastProfiles(t *testing.T) {
	stereo := []byte(`[{"index":1,"codec":"aac","channels":2}]`)
	surround := []byte(`[{"index":1,"codec":"aac","channels":6}]`)
	mp4 := func(vc string, w, h int, audio []byte) media.File {
		f := baseFile()
		f.Container = strPtr("mp4")
		f.VideoCodec = strPtr(vc)
		f.ResolutionW, f.ResolutionH = intPtr(w), intPtr(h)
		f.AudioStreams = audio
		return f
	}
	hdr10 := mp4("hevc", 3840, 2160, surround)
	hdr10.HDRType = strPtr("hdr10")
	hdr10.VideoBitDepth = intPtr(10)
	mkv := baseFile()
	mkv.AudioStreams = stereo
	dts := mp4("h264", 1920, 1080, nil)
	dts.AudioCodec = strPtr("dts")

	for _, tc := range []struct {
		name    string
		profile string
		file    media.File
		want    Decision
	}{
		{"HD: 1080p H.264/AAC stereo MP4 casts directly", castProfileHD, mp4("h264", 1920, 1080, stereo), DecisionDirectPlay},
		{"HD: MKV remuxes", castProfileHD, mkv, DecisionDirectStream},
		{"HD: 5.1 AAC is downmixed", castProfileHD, mp4("h264", 1920, 1080, surround), DecisionDirectStream},
		{"HD: DTS audio is converted", castProfileHD, dts, DecisionDirectStream},
		{"HD: HEVC re-encodes", castProfileHD, mp4("hevc", 1920, 1080, stereo), DecisionTranscode},
		{"HD: 4K H.264 scales down", castProfileHD, mp4("h264", 3840, 2160, stereo), DecisionTranscode},
		{"HD: HDR10 tone-maps", castProfileHD, hdr10, DecisionTranscode},
		{"4K: HEVC MP4 casts directly", castProfile4K, mp4("hevc", 3840, 2160, surround), DecisionDirectPlay},
		{"4K: HDR10 HEVC casts directly", castProfile4K, hdr10, DecisionDirectPlay},
		{"4K: AV1 re-encodes", castProfile4K, mp4("av1", 3840, 2160, surround), DecisionTranscode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.file, ParseCapabilities(tc.profile), defaultServerCaps)
			if got != tc.want {
				t.Errorf("Decide = %s, want %s", got, tc.want)
			}
		})
	}
}
