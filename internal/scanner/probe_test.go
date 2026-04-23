package scanner

import "testing"

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
