package settings

import (
	"encoding/json"
	"testing"
)

func TestWorkerFleetConfig_JSON_RoundTrip(t *testing.T) {
	original := WorkerFleetConfig{
		EmbeddedEnabled: true,
		EmbeddedEncoder: "h264_nvenc",
		Workers: []WorkerSlotConfig{
			{Addr: "10.0.0.5:7073", Name: "NVIDIA Box", Encoder: "h264_nvenc"},
			{Addr: "10.0.0.6:7073", Name: "AMD Box", Encoder: "h264_amf"},
		},
	}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded WorkerFleetConfig
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.EmbeddedEnabled != original.EmbeddedEnabled {
		t.Errorf("embedded_enabled: got %v, want %v", decoded.EmbeddedEnabled, original.EmbeddedEnabled)
	}
	if decoded.EmbeddedEncoder != original.EmbeddedEncoder {
		t.Errorf("embedded_encoder: got %q, want %q", decoded.EmbeddedEncoder, original.EmbeddedEncoder)
	}
	if len(decoded.Workers) != 2 {
		t.Fatalf("workers: got %d, want 2", len(decoded.Workers))
	}
	if decoded.Workers[0].Addr != "10.0.0.5:7073" {
		t.Errorf("workers[0].addr: got %q", decoded.Workers[0].Addr)
	}
	if decoded.Workers[0].Name != "NVIDIA Box" {
		t.Errorf("workers[0].name: got %q", decoded.Workers[0].Name)
	}
	if decoded.Workers[1].Encoder != "h264_amf" {
		t.Errorf("workers[1].encoder: got %q", decoded.Workers[1].Encoder)
	}
}

func TestWorkerFleetConfig_JSON_DefaultsEmpty(t *testing.T) {
	// Default struct should serialize cleanly.
	cfg := WorkerFleetConfig{EmbeddedEnabled: true}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded WorkerFleetConfig
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !decoded.EmbeddedEnabled {
		t.Error("embedded_enabled: want true")
	}
	if decoded.EmbeddedEncoder != "" {
		t.Errorf("embedded_encoder: want empty, got %q", decoded.EmbeddedEncoder)
	}
	if decoded.Workers != nil {
		t.Errorf("workers: want nil, got %v", decoded.Workers)
	}
}

func TestWorkerFleetConfig_JSON_EmbeddedDisabled(t *testing.T) {
	cfg := WorkerFleetConfig{
		EmbeddedEnabled: false,
		Workers: []WorkerSlotConfig{
			{Addr: "192.168.1.10:7073", Name: "Remote", Encoder: "h264_qsv"},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded WorkerFleetConfig
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.EmbeddedEnabled {
		t.Error("embedded_enabled: want false")
	}
	if len(decoded.Workers) != 1 {
		t.Fatalf("workers: got %d, want 1", len(decoded.Workers))
	}
	if decoded.Workers[0].Encoder != "h264_qsv" {
		t.Errorf("workers[0].encoder: got %q", decoded.Workers[0].Encoder)
	}
}

func TestWorkerFleetConfig_JSON_EmptyString(t *testing.T) {
	// Simulates what happens when the DB returns "" — the caller (Service.WorkerFleet)
	// checks for empty before unmarshal, but verify Unmarshal fails gracefully.
	var cfg WorkerFleetConfig
	err := json.Unmarshal([]byte(""), &cfg)
	if err == nil {
		t.Error("expected error unmarshaling empty string")
	}
}

func TestWorkerFleetConfig_JSON_AllEncoderTypes(t *testing.T) {
	cfg := WorkerFleetConfig{
		EmbeddedEnabled: true,
		EmbeddedEncoder: "libx264",
		Workers: []WorkerSlotConfig{
			{Addr: "10.0.0.1:7073", Name: "NVENC", Encoder: "h264_nvenc"},
			{Addr: "10.0.0.2:7073", Name: "AMF", Encoder: "h264_amf"},
			{Addr: "10.0.0.3:7073", Name: "QSV", Encoder: "h264_qsv"},
			{Addr: "10.0.0.4:7073", Name: "VAAPI", Encoder: "h264_vaapi"},
			{Addr: "10.0.0.5:7073", Name: "Software", Encoder: "libx264"},
			{Addr: "10.0.0.6:7073", Name: "Auto"},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded WorkerFleetConfig
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Workers) != 6 {
		t.Fatalf("workers: got %d, want 6", len(decoded.Workers))
	}
	want := []string{"h264_nvenc", "h264_amf", "h264_qsv", "h264_vaapi", "libx264", ""}
	for i, w := range want {
		if decoded.Workers[i].Encoder != w {
			t.Errorf("workers[%d].encoder: got %q, want %q", i, decoded.Workers[i].Encoder, w)
		}
	}
}
