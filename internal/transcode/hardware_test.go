package transcode

import (
	"context"
	"testing"
)

func TestBestEncoder_ReturnsFirst(t *testing.T) {
	encoders := []Encoder{EncoderNVENC, EncoderSoftware}
	if got := BestEncoder(encoders); got != EncoderNVENC {
		t.Errorf("want EncoderNVENC, got %s", got)
	}
}

func TestBestEncoder_Empty_DefaultsSoftware(t *testing.T) {
	if got := BestEncoder(nil); got != EncoderSoftware {
		t.Errorf("want EncoderSoftware for nil slice, got %s", got)
	}
	if got := BestEncoder([]Encoder{}); got != EncoderSoftware {
		t.Errorf("want EncoderSoftware for empty slice, got %s", got)
	}
}

func TestParseOverride(t *testing.T) {
	tests := []struct {
		override string
		want     []Encoder
	}{
		{"software", []Encoder{EncoderSoftware}},
		{"libx264", []Encoder{EncoderSoftware}},
		{"nvenc", []Encoder{EncoderNVENC}},
		{"vaapi", []Encoder{EncoderVAAPI}},
		{"qsv", []Encoder{EncoderQSV}},
		{"nvenc,software", []Encoder{EncoderNVENC, EncoderSoftware}},
		{"vaapi,nvenc", []Encoder{EncoderVAAPI, EncoderNVENC}},
		// unknown values are skipped; empty result defaults to software
		{"bogus", []Encoder{EncoderSoftware}},
		// mixed case
		{"NVENC,Software", []Encoder{EncoderNVENC, EncoderSoftware}},
	}

	for _, tc := range tests {
		got := parseOverride(tc.override)
		if len(got) != len(tc.want) {
			t.Errorf("parseOverride(%q): want %v, got %v", tc.override, tc.want, got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parseOverride(%q)[%d]: want %s, got %s", tc.override, i, tc.want[i], got[i])
			}
		}
	}
}

func TestDetectEncoders_AutoDetect_SoftwareFallback(t *testing.T) {
	// In CI / WSL without GPU hardware, no device files exist.
	// DetectEncoders should always return at least [software].
	encoders, err := DetectEncoders(context.Background(), "")
	if err != nil {
		t.Fatalf("DetectEncoders: %v", err)
	}
	if len(encoders) == 0 {
		t.Fatal("expected at least one encoder")
	}
	// Software must always be present as the final fallback.
	last := encoders[len(encoders)-1]
	if last != EncoderSoftware {
		t.Errorf("want EncoderSoftware as last fallback, got %s", last)
	}
}

func TestEncoderNames(t *testing.T) {
	encoders := []Encoder{EncoderNVENC, EncoderSoftware}
	names := EncoderNames(encoders)
	if len(names) != 2 {
		t.Fatalf("want 2 names, got %d", len(names))
	}
	if names[0] != "h264_nvenc" {
		t.Errorf("want h264_nvenc, got %s", names[0])
	}
	if names[1] != "libx264" {
		t.Errorf("want libx264, got %s", names[1])
	}
}
