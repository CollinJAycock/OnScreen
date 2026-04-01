package transcode

import (
	"context"
	"strings"
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

func TestBestEncoder_AMF(t *testing.T) {
	encoders := []Encoder{EncoderAMF, EncoderSoftware}
	if got := BestEncoder(encoders); got != EncoderAMF {
		t.Errorf("want EncoderAMF, got %s", got)
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
		{"amf", []Encoder{EncoderAMF}},
		{"nvenc,software", []Encoder{EncoderNVENC, EncoderSoftware}},
		{"vaapi,nvenc", []Encoder{EncoderVAAPI, EncoderNVENC}},
		{"amf,software", []Encoder{EncoderAMF, EncoderSoftware}},
		// full codec names (as stored in DB fleet config)
		{"h264_nvenc", []Encoder{EncoderNVENC}},
		{"h264_amf", []Encoder{EncoderAMF}},
		{"h264_vaapi", []Encoder{EncoderVAAPI}},
		{"h264_qsv", []Encoder{EncoderQSV}},
		{"h264_nvenc,h264_amf,libx264", []Encoder{EncoderNVENC, EncoderAMF, EncoderSoftware}},
		// unknown values are skipped; empty result defaults to software
		{"bogus", []Encoder{EncoderSoftware}},
		// mixed case
		{"NVENC,Software", []Encoder{EncoderNVENC, EncoderSoftware}},
		{"AMF", []Encoder{EncoderAMF}},
		// whitespace around entries
		{" nvenc , amf ", []Encoder{EncoderNVENC, EncoderAMF}},
		// empty string
		{"", []Encoder{EncoderSoftware}},
		// duplicates preserved (caller decides policy)
		{"nvenc,nvenc", []Encoder{EncoderNVENC, EncoderNVENC}},
	}

	for _, tc := range tests {
		got := ParseOverride(tc.override)
		if len(got) != len(tc.want) {
			t.Errorf("ParseOverride(%q): want %v, got %v", tc.override, tc.want, got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("ParseOverride(%q)[%d]: want %s, got %s", tc.override, i, tc.want[i], got[i])
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

func TestEncoderNames_AllTypes(t *testing.T) {
	encoders := []Encoder{EncoderNVENC, EncoderAMF, EncoderVAAPI, EncoderQSV, EncoderSoftware}
	names := EncoderNames(encoders)
	want := []string{"h264_nvenc", "h264_amf", "h264_vaapi", "h264_qsv", "libx264"}
	if len(names) != len(want) {
		t.Fatalf("want %d names, got %d", len(want), len(names))
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("names[%d]: want %q, got %q", i, w, names[i])
		}
	}
}

func TestParseOverride_RoundTrip(t *testing.T) {
	// Parsing full codec names and converting back should be idempotent.
	original := []Encoder{EncoderNVENC, EncoderAMF, EncoderSoftware}
	names := EncoderNames(original)
	override := strings.Join(names, ",")
	parsed := ParseOverride(override)
	if len(parsed) != len(original) {
		t.Fatalf("round-trip: want %d encoders, got %d", len(original), len(parsed))
	}
	for i := range original {
		if parsed[i] != original[i] {
			t.Errorf("round-trip[%d]: want %s, got %s", i, original[i], parsed[i])
		}
	}
}
