package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientCaps covers how the transcode handler resolves effective client
// capabilities: the X-Client-Capabilities header is the source of truth, the
// legacy supports_*/max_audio_channels booleans fold in for back-compat, and an
// explicit per-request channel cap overrides. See clientCaps in transcode.go.
func TestClientCaps(t *testing.T) {
	chanPtr := func(n int) *int { return &n }

	tests := []struct {
		name        string
		header      string
		body        transcodeStartRequest
		wantHEVC    bool
		wantAV1     bool
		wantChans   int
		wantProfile bool
	}{
		{
			name:        "no header, no flags → 5.1 default, no profile",
			wantChans:   6,
			wantProfile: false,
		},
		{
			name:        "no header, legacy booleans honored",
			body:        transcodeStartRequest{SupportsHEVC: true, MaxAudioChannels: chanPtr(2)},
			wantHEVC:    true,
			wantChans:   2,
			wantProfile: false,
		},
		{
			name:        "header declares hevc + stereo",
			header:      "videoDecoder=h264:h265,maxAudioChannels=2",
			wantHEVC:    true,
			wantChans:   2,
			wantProfile: true,
		},
		{
			name:        "header without channels → 5.1 default",
			header:      "videoDecoder=h264:av1",
			wantAV1:     true,
			wantChans:   6,
			wantProfile: true,
		},
		{
			name:        "explicit body channel cap overrides header",
			header:      "videoDecoder=h264,maxAudioChannels=6",
			body:        transcodeStartRequest{MaxAudioChannels: chanPtr(8)},
			wantChans:   8,
			wantProfile: true,
		},
		{
			name:        "legacy boolean never downgrades a header capability",
			header:      "videoDecoder=h264:h265", // hevc in header
			body:        transcodeStartRequest{SupportsHEVC: false},
			wantHEVC:    true,
			wantChans:   6,
			wantProfile: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			if tc.header != "" {
				r.Header.Set("X-Client-Capabilities", tc.header)
			}
			caps, hasProfile := clientCaps(r, tc.body)
			if caps.SupportsHEVC != tc.wantHEVC {
				t.Errorf("SupportsHEVC: want %v, got %v", tc.wantHEVC, caps.SupportsHEVC)
			}
			if caps.SupportsAV1 != tc.wantAV1 {
				t.Errorf("SupportsAV1: want %v, got %v", tc.wantAV1, caps.SupportsAV1)
			}
			if caps.MaxAudioChannels != tc.wantChans {
				t.Errorf("MaxAudioChannels: want %d, got %d", tc.wantChans, caps.MaxAudioChannels)
			}
			if hasProfile != tc.wantProfile {
				t.Errorf("hasProfile: want %v, got %v", tc.wantProfile, hasProfile)
			}
		})
	}
}
