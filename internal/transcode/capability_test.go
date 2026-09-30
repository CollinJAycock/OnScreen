package transcode

import (
	"strconv"
	"testing"
)

func TestParseCapabilities_Empty(t *testing.T) {
	caps := ParseCapabilities("")
	if caps.MaxWidth != 1920 {
		t.Errorf("want MaxWidth 1920, got %d", caps.MaxWidth)
	}
	if caps.MaxHeight != 1080 {
		t.Errorf("want MaxHeight 1080, got %d", caps.MaxHeight)
	}
	if caps.MaxAudioChannels != 6 {
		t.Errorf("want MaxAudioChannels 6 (5.1 default), got %d", caps.MaxAudioChannels)
	}
	// A client that sent no profile still gets the universal h264/aac baseline so
	// the common case direct-plays instead of transcoding everything.
	if len(caps.VideoCodecs) != 1 || caps.VideoCodecs[0] != "h264" {
		t.Errorf("want default VideoCodecs [h264], got %v", caps.VideoCodecs)
	}
	if len(caps.AudioCodecs) != 1 || caps.AudioCodecs[0] != "aac" {
		t.Errorf("want default AudioCodecs [aac], got %v", caps.AudioCodecs)
	}
}

func TestParseCapabilities(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   ClientCapabilities
	}{
		{
			name:   "basic h264 aac",
			header: "videoDecoder=h264,audioDecoder=aac",
			want: ClientCapabilities{
				VideoCodecs:      []string{"h264"},
				AudioCodecs:      []string{"aac"},
				MaxWidth:         1920,
				MaxHeight:        1080,
				MaxAudioChannels: 6,
			},
		},
		{
			name:   "h265 sets SupportsHEVC",
			header: "videoDecoder=h264:h265,audioDecoder=aac:ac3",
			want: ClientCapabilities{
				VideoCodecs:      []string{"h264", "h265"},
				AudioCodecs:      []string{"aac", "ac3"},
				SupportsHEVC:     true,
				MaxWidth:         1920,
				MaxHeight:        1080,
				MaxAudioChannels: 6,
			},
		},
		{
			name:   "hevc alias sets SupportsHEVC",
			header: "videoDecoder=hevc",
			want: ClientCapabilities{
				VideoCodecs:      []string{"hevc"},
				SupportsHEVC:     true,
				MaxWidth:         1920,
				MaxHeight:        1080,
				MaxAudioChannels: 6,
			},
		},
		{
			name:   "av1 sets SupportsAV1",
			header: "videoDecoder=h264:av1",
			want: ClientCapabilities{
				VideoCodecs:      []string{"h264", "av1"},
				SupportsAV1:      true,
				MaxWidth:         1920,
				MaxHeight:        1080,
				MaxAudioChannels: 6,
			},
		},
		{
			name:   "custom resolution and channels",
			header: "videoDecoder=h264,maxWidth=3840,maxHeight=2160,maxAudioChannels=8",
			want: ClientCapabilities{
				VideoCodecs:      []string{"h264"},
				MaxWidth:         3840,
				MaxHeight:        2160,
				MaxAudioChannels: 8,
			},
		},
		{
			name:   "protocols as containers",
			header: "videoDecoder=h264,protocols=mkv:mp4",
			want: ClientCapabilities{
				VideoCodecs:      []string{"h264"},
				Containers:       []string{"mkv", "mp4"},
				MaxWidth:         1920,
				MaxHeight:        1080,
				MaxAudioChannels: 6,
			},
		},
		{
			name:   "ampersand separator",
			header: "videoDecoder=h264&audioDecoder=aac",
			want: ClientCapabilities{
				VideoCodecs:      []string{"h264"},
				AudioCodecs:      []string{"aac"},
				MaxWidth:         1920,
				MaxHeight:        1080,
				MaxAudioChannels: 6,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCapabilities(tc.header)
			if got.MaxWidth != tc.want.MaxWidth {
				t.Errorf("MaxWidth: want %d, got %d", tc.want.MaxWidth, got.MaxWidth)
			}
			if got.MaxHeight != tc.want.MaxHeight {
				t.Errorf("MaxHeight: want %d, got %d", tc.want.MaxHeight, got.MaxHeight)
			}
			if got.MaxAudioChannels != tc.want.MaxAudioChannels {
				t.Errorf("MaxAudioChannels: want %d, got %d", tc.want.MaxAudioChannels, got.MaxAudioChannels)
			}
			if got.SupportsHEVC != tc.want.SupportsHEVC {
				t.Errorf("SupportsHEVC: want %v, got %v", tc.want.SupportsHEVC, got.SupportsHEVC)
			}
			if got.SupportsAV1 != tc.want.SupportsAV1 {
				t.Errorf("SupportsAV1: want %v, got %v", tc.want.SupportsAV1, got.SupportsAV1)
			}
			if len(got.VideoCodecs) != len(tc.want.VideoCodecs) {
				t.Errorf("VideoCodecs len: want %d, got %d", len(tc.want.VideoCodecs), len(got.VideoCodecs))
			}
		})
	}
}

func TestParseCapabilities_BitDepth(t *testing.T) {
	if d := ParseCapabilities("").MaxVideoBitDepth; d != 8 {
		t.Errorf("default MaxVideoBitDepth: want 8, got %d", d)
	}
	if d := ParseCapabilities("maxbitdepth=10").MaxVideoBitDepth; d != 10 {
		t.Errorf("maxbitdepth=10: want 10, got %d", d)
	}
	if d := ParseCapabilities("videobitdepth=12").MaxVideoBitDepth; d != 12 {
		t.Errorf("videobitdepth=12 alias: want 12, got %d", d)
	}
}

func TestParseCapabilities_HDR(t *testing.T) {
	if ParseCapabilities("").SupportsHDR {
		t.Error("default SupportsHDR should be false")
	}
	if !ParseCapabilities("hdr=1").SupportsHDR {
		t.Error("hdr=1 should set SupportsHDR")
	}
	if ParseCapabilities("hdr=0").SupportsHDR {
		t.Error("hdr=0 should not set SupportsHDR")
	}
	if !ParseCapabilities("dovi=1").SupportsDV {
		t.Error("dovi=1 should set SupportsDV")
	}
}

// hlg is tri-state: absent (nil — hdr decides HLG, the pre-hlg behaviour),
// claimed, or denied. hdr is untouched by it either way.
func TestParseCapabilities_HLG(t *testing.T) {
	cases := []struct {
		header  string
		wantHLG *bool
		wantHDR bool
	}{
		{"", nil, false},
		{"hdr=1", nil, true},
		{"hdr=0", nil, false},
		{"hdr=1,hlg=1", boolPtr(true), true},
		{"hdr=1,hlg=0", boolPtr(false), true},
		{"hdr=1&HLG=true", boolPtr(true), true},
		{"hlg=false", boolPtr(false), false},
	}
	for _, tc := range cases {
		got := ParseCapabilities(tc.header)
		if (got.SupportsHLG == nil) != (tc.wantHLG == nil) ||
			(got.SupportsHLG != nil && *got.SupportsHLG != *tc.wantHLG) {
			t.Errorf("%q: SupportsHLG = %s, want %s", tc.header, fmtBoolPtr(got.SupportsHLG), fmtBoolPtr(tc.wantHLG))
		}
		if got.SupportsHDR != tc.wantHDR {
			t.Errorf("%q: SupportsHDR = %v, want %v", tc.header, got.SupportsHDR, tc.wantHDR)
		}
	}
}

// vp9MaxBitDepth is VP9's own depth ceiling, separate from the maxbitdepth the
// HEVC gate reads; 0 (absent or unusable) means "not declared".
func TestParseCapabilities_VP9BitDepth(t *testing.T) {
	cases := []struct {
		header    string
		wantVP9   int
		wantVideo int
	}{
		{"", 0, 8},
		{"maxbitdepth=10", 0, 10},
		{"vp9MaxBitDepth=10", 10, 8},
		{"vp9maxbitdepth=8,maxbitdepth=10", 8, 10},
		{"vp9MaxBitDepth=0", 0, 8},
		{"vp9MaxBitDepth=abc", 0, 8},
	}
	for _, tc := range cases {
		got := ParseCapabilities(tc.header)
		if got.MaxVP9BitDepth != tc.wantVP9 {
			t.Errorf("%q: MaxVP9BitDepth = %d, want %d", tc.header, got.MaxVP9BitDepth, tc.wantVP9)
		}
		if got.MaxVideoBitDepth != tc.wantVideo {
			t.Errorf("%q: MaxVideoBitDepth = %d, want %d", tc.header, got.MaxVideoBitDepth, tc.wantVideo)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

func fmtBoolPtr(b *bool) string {
	if b == nil {
		return "nil"
	}
	return strconv.FormatBool(*b)
}

func TestClientCapabilities_Supports(t *testing.T) {
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac:ac3,protocols=mkv:mp4")

	videoTests := []struct {
		codec string
		want  bool
	}{
		{"h264", true},
		{"h265", true},
		{"H264", true}, // case-insensitive
		{"av1", false},
		{"vp9", false},
	}
	for _, tc := range videoTests {
		if got := caps.SupportsVideoCodec(tc.codec); got != tc.want {
			t.Errorf("SupportsVideoCodec(%q): want %v, got %v", tc.codec, tc.want, got)
		}
	}

	audioTests := []struct {
		codec string
		want  bool
	}{
		{"aac", true},
		{"ac3", true},
		{"AAC", true},
		{"dts", false},
		{"truehd", false},
	}
	for _, tc := range audioTests {
		if got := caps.SupportsAudioCodec(tc.codec); got != tc.want {
			t.Errorf("SupportsAudioCodec(%q): want %v, got %v", tc.codec, tc.want, got)
		}
	}

	containerTests := []struct {
		container string
		want      bool
	}{
		{"mkv", true},
		{"mp4", true},
		{"MKV", true},
		{"avi", false},
		{"ts", false},
	}
	for _, tc := range containerTests {
		if got := caps.SupportsContainer(tc.container); got != tc.want {
			t.Errorf("SupportsContainer(%q): want %v, got %v", tc.container, tc.want, got)
		}
	}
}

func TestParseInt(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"1920", 1920},
		{"0", 0},
		{"", 0},
		{"abc", 0},    // non-digit start → 0
		{"12abc", 12}, // stops at first non-digit
	}
	for _, tc := range cases {
		if got := parseInt(tc.in); got != tc.want {
			t.Errorf("parseInt(%q): want %d, got %d", tc.in, tc.want, got)
		}
	}
}

func TestClientCapabilities_SupportsContainer_NoDeclaration(t *testing.T) {
	// When no containers are declared, mkv/mp4/mov should be assumed supported.
	caps := ParseCapabilities("videoDecoder=h264")
	for _, c := range []string{"mkv", "mp4", "mov"} {
		if !caps.SupportsContainer(c) {
			t.Errorf("expected %q to be supported when no containers declared", c)
		}
	}
	if caps.SupportsContainer("avi") {
		t.Error("expected avi not supported when no containers declared")
	}
}
