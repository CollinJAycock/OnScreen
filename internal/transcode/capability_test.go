package transcode

import (
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
	if caps.MaxAudioChannels != 2 {
		t.Errorf("want MaxAudioChannels 2, got %d", caps.MaxAudioChannels)
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
				MaxAudioChannels: 2,
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
				MaxAudioChannels: 2,
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
				MaxAudioChannels: 2,
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
				MaxAudioChannels: 2,
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
				MaxAudioChannels: 2,
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
				MaxAudioChannels: 2,
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
		{"abc", 0},         // non-digit start → 0
		{"12abc", 12},      // stops at first non-digit
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
