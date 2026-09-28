package transcode

import (
	"reflect"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/media"
)

func TestDecisionReasons_Table(t *testing.T) {
	sevenOne := []byte(`[{"index":1,"codec":"truehd","channels":8}]`)
	fiveOne := []byte(`[{"index":1,"codec":"ac3","channels":6}]`)

	cases := []struct {
		name string
		file func() media.File
		caps string
		want []string
	}{
		{
			name: "compatible file has no reasons",
			file: baseFile,
			caps: "videoDecoder=h264,audioDecoder=aac,protocols=mkv",
			want: nil,
		},
		{
			name: "hevc on an h264-only client",
			file: func() media.File { f := baseFile(); f.VideoCodec = strPtr("hevc"); return f },
			caps: "videoDecoder=h264,audioDecoder=aac",
			want: []string{"HEVC not supported by client"},
		},
		{
			name: "7.1 audio over a 5.1 cap",
			file: func() media.File {
				f := baseFile()
				f.AudioCodec = strPtr("aac")
				f.AudioStreams = sevenOne
				return f
			},
			caps: "videoDecoder=h264,audioDecoder=aac,maxAudioChannels=6",
			want: []string{"7.1 audio → client max 5.1"},
		},
		{
			name: "undecodable audio codec plus container",
			file: func() media.File {
				f := baseFile()
				f.AudioCodec = strPtr("truehd")
				f.AudioStreams = sevenOne
				f.Container = strPtr("matroska")
				return f
			},
			caps: "videoDecoder=h264,audioDecoder=aac:ac3,protocols=mp4:ts",
			want: []string{"TrueHD audio not supported by client", "MKV container not supported by client"},
		},
		{
			name: "every problem is listed, not just the first",
			file: func() media.File {
				f := baseFile()
				f.VideoCodec = strPtr("hevc")
				f.ResolutionW, f.ResolutionH = intPtr(3840), intPtr(2160)
				f.HDRType = strPtr("hdr10")
				f.AudioCodec = strPtr("ac3")
				f.AudioStreams = fiveOne
				return f
			},
			caps: "videoDecoder=h264,audioDecoder=aac:ac3,maxAudioChannels=2",
			want: []string{
				"HDR10 not supported by client (tonemapped to SDR)",
				"HEVC not supported by client",
				"3840×2160 exceeds client max 1920×1080",
				"5.1 audio → client max stereo",
			},
		},
		{
			name: "dolby vision",
			file: func() media.File {
				f := baseFile()
				f.VideoCodec = strPtr("hevc")
				f.HDRType = strPtr("dolby_vision")
				return f
			},
			caps: "videoDecoder=h264:h265,audioDecoder=aac",
			want: []string{"Dolby Vision not supported by client"},
		},
		{
			name: "hi10p h264",
			file: func() media.File { f := baseFile(); f.VideoBitDepth = intPtr(10); return f },
			caps: "videoDecoder=h264,audioDecoder=aac",
			want: []string{"10-bit H.264 not decodable by client"},
		},
		{
			name: "main 12 hevc on a main 10 decoder",
			file: func() media.File {
				f := baseFile()
				f.VideoCodec = strPtr("hevc")
				f.VideoBitDepth = intPtr(12)
				return f
			},
			caps: "videoDecoder=h264:h265,audioDecoder=aac,maxBitDepth=10",
			want: []string{"12-bit HEVC → client max 10-bit"},
		},
		{
			name: "vp9 needing an audio change can't remux",
			file: func() media.File {
				f := baseFile()
				f.VideoCodec = strPtr("vp9")
				f.AudioCodec = strPtr("opus")
				f.Container = strPtr("webm")
				return f
			},
			caps: "videoDecoder=h264:vp9,audioDecoder=aac,protocols=mkv:mp4",
			want: []string{
				"Opus audio not supported by client",
				"WEBM container not supported by client",
				"VP9 can't be remuxed for streaming",
			},
		},
		{
			name: "damaged file is the only reason",
			file: func() media.File { f := baseFile(); f.IntegrityStatus = media.IntegrityDamaged; return f },
			caps: "videoDecoder=h264,audioDecoder=aac",
			want: []string{"File is damaged"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReasonTexts(DecisionReasons(tc.file(), ParseCapabilities(tc.caps)))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("reasons:\n got  %q\n want %q", got, tc.want)
			}
		})
	}
}

// A per-request supports_hevc override sets the flag without adding "h265" to
// the declared list; the reasons must honor it the way Start does.
func TestDecisionReasons_HonorsHEVCFlag(t *testing.T) {
	f := baseFile()
	f.VideoCodec = strPtr("hevc")
	caps := ParseCapabilities("videoDecoder=h264,audioDecoder=aac")
	caps.SupportsHEVC = true
	if got := DecisionReasons(f, caps); len(got) != 0 {
		t.Errorf("reasons with SupportsHEVC set: got %v, want none", got)
	}
}

// Reason kinds let a remux keep only audio/container reasons.
func TestDecisionReasons_Kinds(t *testing.T) {
	f := baseFile()
	f.VideoCodec = strPtr("hevc")
	f.AudioCodec = strPtr("dts")
	rs := DecisionReasons(f, ParseCapabilities("videoDecoder=h264,audioDecoder=aac"))
	want := []Reason{
		{ReasonVideo, "HEVC not supported by client"},
		{ReasonAudio, "DTS audio not supported by client"},
	}
	if !reflect.DeepEqual(rs, want) {
		t.Errorf("got %+v, want %+v", rs, want)
	}
}

// DecideWithReasons never changes Decide's verdict, and a direct play has no
// reasons.
func TestDecideWithReasons_MatchesDecide(t *testing.T) {
	files := []media.File{baseFile()}
	hevc := baseFile()
	hevc.VideoCodec = strPtr("hevc")
	files = append(files, hevc)
	mkvDTS := baseFile()
	mkvDTS.AudioCodec = strPtr("dts")
	files = append(files, mkvDTS)
	for _, capsHeader := range []string{"", "videoDecoder=h264,audioDecoder=aac", "videoDecoder=h264:h265,audioDecoder=aac:dts,protocols=mkv"} {
		caps := ParseCapabilities(capsHeader)
		for _, f := range files {
			d, reasons := DecideWithReasons(f, caps, defaultServerCaps)
			if want := Decide(f, caps, defaultServerCaps); d != want {
				t.Errorf("caps %q: DecideWithReasons=%s, Decide=%s", capsHeader, d, want)
			}
			if d == DecisionDirectPlay && reasons != nil {
				t.Errorf("caps %q: direct play carries reasons %v", capsHeader, reasons)
			}
			if d != DecisionDirectPlay && len(reasons) == 0 {
				t.Errorf("caps %q codec %s: %s verdict with no reasons", capsHeader, *f.VideoCodec, d)
			}
		}
	}
}

func TestChannelLabel(t *testing.T) {
	for ch, want := range map[int]string{1: "mono", 2: "stereo", 6: "5.1", 8: "7.1", 4: "4-channel"} {
		if got := ChannelLabel(ch); got != want {
			t.Errorf("ChannelLabel(%d) = %q, want %q", ch, got, want)
		}
	}
}
