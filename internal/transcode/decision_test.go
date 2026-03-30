package transcode

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// helpers
func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func baseFile() media.File {
	return media.File{
		ID:          uuid.New(),
		MediaItemID: uuid.New(),
		FilePath:    "/media/movie.mkv",
		VideoCodec:  strPtr("h264"),
		AudioCodec:  strPtr("aac"),
		Container:   strPtr("mkv"),
		ResolutionW: intPtr(1920),
		ResolutionH: intPtr(1080),
	}
}

var defaultServerCaps = ServerCaps{
	MaxBitrateKbps: 40000,
	MaxWidth:       3840,
	MaxHeight:      2160,
}

func TestDecide_DirectPlay(t *testing.T) {
	file := baseFile()
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac:ac3,protocols=mkv:mp4")
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectPlay {
		t.Errorf("want DirectPlay, got %s", got)
	}
}

func TestDecide_DirectStream_ContainerMismatch(t *testing.T) {
	file := baseFile()
	// Client supports h264+aac but not mkv — should DirectStream (remux).
	caps := ParseCapabilities("videoDecoder=h264,audioDecoder=aac,protocols=ts:mp4")
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectStream {
		t.Errorf("want DirectStream, got %s", got)
	}
}

func TestDecide_Transcode_UnsupportedVideo(t *testing.T) {
	file := baseFile()
	*file.VideoCodec = "hevc"
	// Client only supports h264 — must transcode.
	caps := ParseCapabilities("videoDecoder=h264,audioDecoder=aac,protocols=mkv:mp4:ts")
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionTranscode {
		t.Errorf("want Transcode, got %s", got)
	}
}

func TestDecide_Transcode_HDR10_ClientNoHDR(t *testing.T) {
	file := baseFile()
	file.HDRType = strPtr("hdr10")
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mkv:mp4")
	caps.SupportsHDR = false
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionTranscode {
		t.Errorf("want Transcode for HDR without client support, got %s", got)
	}
}

func TestDecide_DirectPlay_HDR10_ClientSupportsHDR(t *testing.T) {
	file := baseFile()
	file.HDRType = strPtr("hdr10")
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mkv:mp4")
	caps.SupportsHDR = true
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectPlay {
		t.Errorf("want DirectPlay for HDR with client HDR support, got %s", got)
	}
}

func TestDecide_Transcode_DolbyVision_ClientNoDV(t *testing.T) {
	file := baseFile()
	file.HDRType = strPtr("dolby_vision")
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mkv:mp4")
	caps.SupportsHDR = true // HDR10 support doesn't cover DV
	caps.SupportsDV = false
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionTranscode {
		t.Errorf("want Transcode for DV without DV support, got %s", got)
	}
}

func TestDecide_DirectPlay_DolbyVision_ClientSupportsDV(t *testing.T) {
	file := baseFile()
	file.HDRType = strPtr("dolby_vision")
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mkv:mp4")
	caps.SupportsDV = true
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectPlay {
		t.Errorf("want DirectPlay for DV with DV support, got %s", got)
	}
}

func TestDecide_Transcode_ResolutionExceedsClient(t *testing.T) {
	file := baseFile()
	*file.ResolutionW = 3840
	*file.ResolutionH = 2160
	// Client caps only 1080p.
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mkv:mp4")
	caps.MaxWidth = 1920
	caps.MaxHeight = 1080
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionTranscode {
		t.Errorf("want Transcode when 4K exceeds 1080p client, got %s", got)
	}
}

func TestDecide_NilCodecs(t *testing.T) {
	// File with no codec info — should not panic.
	file := media.File{
		ID:          uuid.New(),
		MediaItemID: uuid.New(),
		FilePath:    "/media/unknown.bin",
	}
	caps := ParseCapabilities("videoDecoder=h264,audioDecoder=aac,protocols=mkv")
	got := Decide(file, caps, defaultServerCaps)
	// With no codecs, clientSupportsVideo is false → Transcode.
	if got != DecisionTranscode {
		t.Errorf("want Transcode for nil codecs, got %s", got)
	}
}

func TestDecision_String(t *testing.T) {
	cases := []struct {
		d    Decision
		want string
	}{
		{DecisionDirectPlay, "directPlay"},
		{DecisionDirectStream, "directStream"},
		{DecisionTranscode, "transcode"},
	}
	for _, tc := range cases {
		if got := tc.d.String(); got != tc.want {
			t.Errorf("Decision(%d).String(): want %q, got %q", tc.d, tc.want, got)
		}
	}
}

func TestCanonicalCodecs(t *testing.T) {
	videoTests := []struct{ in, want string }{
		{"h264", "h264"},
		{"avc", "h264"},
		{"avc1", "h264"},
		{"hevc", "h265"},
		{"hvc1", "h265"},
		{"H265", "h265"},
		{"av1", "av1"},
		{"vp9", "vp9"},
		{"mpeg4", "mpeg4"},
		{"unknown_codec", "unknown_codec"},
	}
	for _, tc := range videoTests {
		if got := canonicalVideoCodec(tc.in); got != tc.want {
			t.Errorf("canonicalVideoCodec(%q): want %q, got %q", tc.in, tc.want, got)
		}
	}

	audioTests := []struct{ in, want string }{
		{"aac", "aac"},
		{"ac3", "ac3"},
		{"ac-3", "ac3"},
		{"eac3", "eac3"},
		{"e-ac-3", "eac3"},
		{"eac-3", "eac3"},
		{"dts-hd", "dts"},
		{"dtshd", "dts"},
		{"dts", "dts"},
		{"mp2", "mp3"},
		{"mp3", "mp3"},
		{"truehd", "truehd"},
		{"flac", "flac"},
		{"vorbis", "vorbis"},
		{"opus", "opus"},
		{"unknown_audio", "unknown_audio"},
	}
	for _, tc := range audioTests {
		if got := canonicalAudioCodec(tc.in); got != tc.want {
			t.Errorf("canonicalAudioCodec(%q): want %q, got %q", tc.in, tc.want, got)
		}
	}

	containerTests := []struct{ in, want string }{
		{"matroska", "mkv"},
		{"mkv", "mkv"},
		{"mp4", "mp4"},
		{"m4v", "mp4"},
		{"isom", "mp4"},
		{"mov", "mp4"},
		{"avi", "avi"},
		{"ts", "ts"},
		{"mpeg-ts", "ts"},
	}
	for _, tc := range containerTests {
		if got := canonicalContainer(tc.in); got != tc.want {
			t.Errorf("canonicalContainer(%q): want %q, got %q", tc.in, tc.want, got)
		}
	}
}

func TestClientSupportsHDR(t *testing.T) {
	capsHDR := ClientCapabilities{SupportsHDR: true}
	capsDV := ClientCapabilities{SupportsDV: true}
	capsNone := ClientCapabilities{}

	cases := []struct {
		caps    ClientCapabilities
		hdrType string
		want    bool
	}{
		{capsHDR, "hdr10", true},
		{capsHDR, "hdr10plus", true},
		{capsHDR, "hlg", true},
		{capsNone, "hdr10", false},
		{capsDV, "dolby_vision", true},
		{capsHDR, "dolby_vision", false}, // HDR10 support ≠ DV support
		{capsNone, "dolby_vision", false},
		{capsNone, "unknown_hdr", false},
	}
	for _, tc := range cases {
		if got := clientSupportsHDR(tc.caps, tc.hdrType); got != tc.want {
			t.Errorf("clientSupportsHDR(%q): want %v, got %v", tc.hdrType, tc.want, got)
		}
	}
}
