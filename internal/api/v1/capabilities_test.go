package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeCapsProvider struct{ resp CapabilitiesResponse }

func (f *fakeCapsProvider) Capabilities() CapabilitiesResponse { return f.resp }

func TestCapabilities_ReturnsProviderResponse(t *testing.T) {
	want := CapabilitiesResponse{
		Server: CapabilitiesServer{
			Name:       "OnScreen Test",
			MachineID:  "abc",
			Version:    "1.2.3",
			APIVersion: "v1",
		},
		Features: CapabilitiesFeatures{
			Transcode:     true,
			Trickplay:     true,
			DevicePairing: true,
			OIDC:          false,
		},
		Limits: CapabilitiesLimits{
			MaxUploadBytes:          1 << 20,
			MaxTranscodeBitrateKbps: 40000,
			MaxTranscodeWidth:       3840,
			MaxTranscodeHeight:      2160,
		},
		Discovery: CapabilitiesDiscovery{UDPPort: 7368},
	}
	h := NewCapabilitiesHandler(&fakeCapsProvider{resp: want})
	rec := httptest.NewRecorder()
	h.Get(rec, httptest.NewRequest("GET", "/api/v1/system/capabilities", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var env struct {
		Data CapabilitiesResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Server.Name != want.Server.Name {
		t.Errorf("server.name: got %q, want %q", env.Data.Server.Name, want.Server.Name)
	}
	if !env.Data.Features.Transcode || !env.Data.Features.DevicePairing {
		t.Errorf("expected feature flags preserved: %+v", env.Data.Features)
	}
	if env.Data.Limits.MaxTranscodeBitrateKbps != 40000 {
		t.Errorf("limit not preserved: %+v", env.Data.Limits)
	}
	if env.Data.Discovery.UDPPort != 7368 {
		t.Errorf("discovery port not preserved: %+v", env.Data.Discovery)
	}
}
