package scanner

import (
	"bytes"
	"testing"
)

// atom builds an 8-byte ISOBMFF top-level atom header with no body (size = 8).
func atom(typ string) []byte {
	b := []byte{0, 0, 0, 8}
	return append(b, []byte(typ)...)
}

func TestIsMP4Container(t *testing.T) {
	for _, p := range []string{"a.mp4", "a.MOV", "a.m4v", "a.m4a"} {
		if !IsMP4Container(p) {
			t.Errorf("%s should be an MP4-family container", p)
		}
	}
	for _, p := range []string{"a.mkv", "a.flac", "a.webm", "a"} {
		if IsMP4Container(p) {
			t.Errorf("%s should not be treated as MP4-family", p)
		}
	}
}

func TestIsFaststartReader(t *testing.T) {
	// moov before mdat → faststart.
	if !IsFaststartReader(bytes.NewReader(append(atom("ftyp"), atom("moov")...))) {
		t.Error("moov-before-mdat should report faststart=true")
	}
	// mdat before moov → not faststart.
	if IsFaststartReader(bytes.NewReader(append(atom("ftyp"), atom("mdat")...))) {
		t.Error("mdat-before-moov should report faststart=false")
	}
	// Unreadable / not ISOBMFF → assume ok (true) rather than block playback.
	if !IsFaststartReader(bytes.NewReader([]byte("garbage"))) {
		t.Error("undecodable input should default to true (assume ok)")
	}
}

func TestStreamBitDepth(t *testing.T) {
	cases := []struct {
		name string
		s    *ffprobeStream
		want int
	}{
		{
			name: "raw sample preferred over decoded",
			s:    &ffprobeStream{BitsPerRawSample: "24", BitsPerSample: 32},
			want: 24,
		},
		{
			name: "falls back to decoded when raw missing",
			s:    &ffprobeStream{BitsPerSample: 16},
			want: 16,
		},
		{
			name: "raw zero string falls through",
			s:    &ffprobeStream{BitsPerRawSample: "0", BitsPerSample: 16},
			want: 16,
		},
		{
			name: "raw garbage falls through",
			s:    &ffprobeStream{BitsPerRawSample: "junk", BitsPerSample: 16},
			want: 16,
		},
		{
			name: "neither set returns zero (lossy formats)",
			s:    &ffprobeStream{},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := streamBitDepth(tc.s); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestVideoBitDepth(t *testing.T) {
	cases := []struct {
		name string
		s    *ffprobeStream
		want int
	}{
		{"8-bit yuv420p", &ffprobeStream{PixFmt: "yuv420p"}, 8},
		{"8-bit nv12", &ffprobeStream{PixFmt: "nv12"}, 8},
		{"10-bit yuv420p10le", &ffprobeStream{PixFmt: "yuv420p10le"}, 10},
		{"10-bit p010le", &ffprobeStream{PixFmt: "p010le"}, 10},
		{"10-bit yuv444p10le", &ffprobeStream{PixFmt: "yuv444p10le"}, 10},
		{"12-bit yuv420p12le", &ffprobeStream{PixFmt: "yuv420p12le"}, 12},
		{"16-bit yuv420p16le", &ffprobeStream{PixFmt: "yuv420p16le"}, 16},
		{"pix_fmt empty falls back to raw sample", &ffprobeStream{BitsPerRawSample: "10"}, 10},
		{"unknown stream returns zero", &ffprobeStream{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := videoBitDepth(tc.s); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestChannelLayoutFromCount(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "mono"},
		{2, "stereo"},
		{3, "2.1"},
		{4, "quad"},
		{6, "5.1"},
		{8, "7.1"},
		{5, "5 channels"},   // exotic
		{7, "7 channels"},   // exotic
		{10, "10 channels"}, // exotic
		{0, "0 channels"},   // edge case
	}
	for _, tc := range cases {
		if got := channelLayoutFromCount(tc.n); got != tc.want {
			t.Errorf("channelLayoutFromCount(%d): got %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestParseIntSafe(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"42", 42},
		{"0", 0},
		{"-7", -7},
		{"", 0},
		{"abc", 0},
		{"3.14", 0}, // not an int
	}
	for _, tc := range cases {
		if got := parseIntSafe(tc.in); got != tc.want {
			t.Errorf("parseIntSafe(%q): got %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestIsLosslessAudio(t *testing.T) {
	alac := "alac"
	aac := "aac"
	cases := []struct {
		name  string
		path  string
		codec *string
		want  bool
	}{
		{"flac is lossless", "song.flac", nil, true},
		{"flac uppercase ext", "song.FLAC", nil, true},
		{"wav is lossless", "song.wav", nil, true},
		{"aiff is lossless", "song.aiff", nil, true},
		{"alac extension is lossless", "song.alac", nil, true},
		{"wavpack is lossless", "song.wv", nil, true},
		{"ape is lossless", "song.ape", nil, true},
		{"tak is lossless", "song.tak", nil, true},
		{"dsf is lossless", "song.dsf", nil, true},
		{"dff is lossless", "song.dff", nil, true},
		{"mp3 is lossy", "song.mp3", nil, false},
		{"opus is lossy", "song.opus", nil, false},
		{"ogg is lossy", "song.ogg", nil, false},
		{"m4a with ALAC codec is lossless", "song.m4a", &alac, true},
		{"m4a with AAC codec is lossy", "song.m4a", &aac, false},
		{"m4a with nil codec is conservatively lossy", "song.m4a", nil, false},
		{"m4a with empty codec is lossy", "song.m4a", strPtr(""), false},
		{"unknown extension is lossy", "song.xyz", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLosslessAudio(tc.path, tc.codec); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
