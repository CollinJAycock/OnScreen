package transcode

import "testing"

func TestSourceAudioChannels(t *testing.T) {
	streams := []byte(`[{"index":1,"codec":"eac3","channels":6,"language":"eng"},{"index":2,"codec":"aac","channels":2,"language":"jpn"}]`)
	cases := []struct {
		name string
		json []byte
		idx  int
		want int
	}{
		{"default stream (-1) → first", streams, -1, 6},
		{"explicit first", streams, 0, 6},
		{"explicit second", streams, 1, 2},
		{"out of range → 0", streams, 5, 0},
		{"empty → 0", nil, -1, 0},
		{"garbage → 0", []byte("not json"), 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SourceAudioChannels(tc.json, tc.idx); got != tc.want {
				t.Errorf("SourceAudioChannels(%s, %d) = %d, want %d", tc.json, tc.idx, got, tc.want)
			}
		})
	}
}

func TestTargetAudioChannels(t *testing.T) {
	cases := []struct {
		name             string
		source, clientMax int
		want             int
	}{
		{"5.1 preserved (no client cap)", 6, 0, 6},
		{"7.1 source downmixed to 5.1 (clients can't decode 7.1 AAC)", 8, 0, 6},
		{"stereo stays stereo", 2, 0, 2},
		{"client caps 5.1 to stereo", 6, 2, 2},
		{"client cap above source is a no-op", 2, 6, 2},
		{"7.1 not restored even if client asks for 8 (hard 5.1 AAC cap)", 8, 8, 6},
		{"above ceiling clamps to 5.1", 12, 0, 6},
		{"unknown source → stereo", 0, 0, 2},
		{"unknown source ignores client max", 0, 6, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TargetAudioChannels(tc.source, tc.clientMax); got != tc.want {
				t.Errorf("TargetAudioChannels(%d, %d) = %d, want %d", tc.source, tc.clientMax, got, tc.want)
			}
		})
	}
}

func TestAACBitrateKbps(t *testing.T) {
	cases := []struct{ ch, want int }{
		{1, 128}, {2, 128}, {6, 384}, {8, 512},
	}
	for _, tc := range cases {
		if got := AACBitrateKbps(tc.ch); got != tc.want {
			t.Errorf("AACBitrateKbps(%d) = %d, want %d", tc.ch, got, tc.want)
		}
	}
}
