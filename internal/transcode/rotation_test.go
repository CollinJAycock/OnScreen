package transcode

import "testing"

func TestParseSourceProbe(t *testing.T) {
	cases := []struct {
		name    string
		out     string
		wantDeg int
		wantKb  int
	}{
		{
			name:    "portrait phone clip: display-matrix rotation + bitrate",
			out:     "rotation=-90\nbit_rate=16000000\n",
			wantDeg: 270, // -90 normalized into [0,360)
			wantKb:  16000,
		},
		{
			name:    "legacy rotate tag, 90",
			out:     "rotate=90\nbit_rate=12000000\n",
			wantDeg: 90,
			wantKb:  12000,
		},
		{
			name:    "upside down",
			out:     "rotation=180\n",
			wantDeg: 180,
			wantKb:  0,
		},
		{
			name:    "no rotation (landscape)",
			out:     "bit_rate=9000000\n",
			wantDeg: 0,
			wantKb:  9000,
		},
		{
			name:    "rotation 0 is treated as upright",
			out:     "rotation=0\nbit_rate=5000000\n",
			wantDeg: 0,
			wantKb:  5000,
		},
		{
			name:    "repeated rotation lines: first non-zero wins, not clobbered",
			out:     "rotation=-90\nrotation=-90\nrotation=-90\n",
			wantDeg: 270,
			wantKb:  0,
		},
		{
			name:    "empty / probe failure",
			out:     "",
			wantDeg: 0,
			wantKb:  0,
		},
		{
			name:    "garbage values ignored",
			out:     "rotation=abc\nbit_rate=notanumber\n",
			wantDeg: 0,
			wantKb:  0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSourceProbe(tc.out)
			if got.rotationDeg != tc.wantDeg {
				t.Errorf("rotationDeg: got %d, want %d", got.rotationDeg, tc.wantDeg)
			}
			if got.bitrateKbps != tc.wantKb {
				t.Errorf("bitrateKbps: got %d, want %d", got.bitrateKbps, tc.wantKb)
			}
		})
	}
}
