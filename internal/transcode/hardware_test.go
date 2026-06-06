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

func TestBestAV1Encoder(t *testing.T) {
	// Empty list / no AV1 encoder → empty string. Worker callers
	// distinguish "no AV1 available" from "use this AV1 encoder" via
	// the empty-string sentinel; a wrong default would silently pick
	// the H.264 path.
	if got := BestAV1Encoder(nil); got != "" {
		t.Errorf("nil slice: want empty, got %s", got)
	}
	if got := BestAV1Encoder([]Encoder{EncoderNVENC, EncoderHEVCNVENC, EncoderSoftware}); got != "" {
		t.Errorf("no AV1 in list: want empty, got %s", got)
	}
	// Priority order: caller's slice order wins (matches DetectEncoders'
	// detection-order semantics, same as BestHEVCEncoder).
	got := BestAV1Encoder([]Encoder{EncoderNVENC, EncoderAV1NVENC, EncoderAV1Software, EncoderSoftware})
	if got != EncoderAV1NVENC {
		t.Errorf("want EncoderAV1NVENC (first AV1 in list), got %s", got)
	}
	// Software-only AV1 in the list (the libsvtav1 test path) — picks it.
	got = BestAV1Encoder([]Encoder{EncoderSoftware, EncoderHEVCSoftware, EncoderAV1Software})
	if got != EncoderAV1Software {
		t.Errorf("want EncoderAV1Software, got %s", got)
	}
}

func TestHasAV1Encoder(t *testing.T) {
	if HasAV1Encoder([]Encoder{EncoderNVENC, EncoderHEVCNVENC, EncoderSoftware}) {
		t.Error("no AV1 in list: want false, got true")
	}
	if !HasAV1Encoder([]Encoder{EncoderSoftware, EncoderAV1Software}) {
		t.Error("libsvtav1 in list: want true, got false")
	}
	if !HasAV1Encoder([]Encoder{EncoderAV1NVENC}) {
		t.Error("av1_nvenc only: want true, got false")
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
		// AV1 encoder strings
		{"av1_nvenc", []Encoder{EncoderAV1NVENC}},
		{"av1_qsv", []Encoder{EncoderAV1QSV}},
		{"av1_vaapi", []Encoder{EncoderAV1VAAPI}},
		{"av1_amf", []Encoder{EncoderAV1AMF}},
		{"av1_software", []Encoder{EncoderAV1Software}},
		{"libsvtav1", []Encoder{EncoderAV1Software}},
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

func encEq(a, b []Encoder) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFallbackEncoders(t *testing.T) {
	// The laptop: RTX 4080 dGPU (NVENC) + Intel iGPU (QSV), Windows (no VAAPI).
	laptop := []Encoder{
		EncoderNVENC, EncoderHEVCNVENC, EncoderAV1NVENC,
		EncoderQSV, EncoderHEVCQSV,
		EncoderSoftware,
	}
	// A single-GPU NVENC box (no second provider): software is the only fallback.
	nvencOnly := []Encoder{EncoderNVENC, EncoderHEVCNVENC, EncoderAV1NVENC, EncoderSoftware}
	// The Arc box (Linux): VAAPI + QSV on the same Intel GPU.
	arc := []Encoder{
		EncoderVAAPI, EncoderHEVCVAAPI, EncoderAV1VAAPI,
		EncoderQSV, EncoderHEVCQSV, EncoderAV1QSV,
		EncoderSoftware,
	}

	tests := []struct {
		name      string
		primary   Encoder
		available []Encoder
		want      []Encoder
	}{
		{
			// The headline case: NVENC H.264 saturates → Intel iGPU QSV → CPU.
			name:      "laptop nvenc h264 -> qsv -> software",
			primary:   EncoderNVENC,
			available: laptop,
			want:      []Encoder{EncoderQSV, EncoderSoftware},
		},
		{
			// HEVC stays HEVC: hevc_nvenc -> hevc_qsv -> libx265 (same .m4s family).
			name:      "laptop nvenc hevc -> qsv hevc -> software hevc",
			primary:   EncoderHEVCNVENC,
			available: laptop,
			want:      []Encoder{EncoderHEVCQSV, EncoderHEVCSoftware},
		},
		{
			// AV1 nvenc has no QSV AV1 on this box (laptop QSV lacks av1_qsv) → just software AV1.
			name:      "laptop nvenc av1 -> software av1 (no qsv av1 present)",
			primary:   EncoderAV1NVENC,
			available: laptop,
			want:      []Encoder{EncoderAV1Software},
		},
		{
			// Single-provider box: nothing to spill to but the CPU.
			name:      "nvenc-only box -> software",
			primary:   EncoderNVENC,
			available: nvencOnly,
			want:      []Encoder{EncoderSoftware},
		},
		{
			// VAAPI primary on the Arc box -> QSV (different driver class) -> software.
			name:      "arc vaapi h264 -> qsv -> software",
			primary:   EncoderVAAPI,
			available: arc,
			want:      []Encoder{EncoderQSV, EncoderSoftware},
		},
		{
			// AV1 on the Arc box has a real QSV AV1 sibling -> av1_qsv -> software.
			name:      "arc vaapi av1 -> qsv av1 -> software",
			primary:   EncoderAV1VAAPI,
			available: arc,
			want:      []Encoder{EncoderAV1QSV, EncoderAV1Software},
		},
		{
			// A software primary can't run out of GPU sessions — no fail-over.
			name:      "software primary -> nil",
			primary:   EncoderSoftware,
			available: laptop,
			want:      nil,
		},
		{
			// Stream-copy is not an encoder — no fail-over.
			name:      "copy primary -> nil",
			primary:   "copy",
			available: laptop,
			want:      nil,
		},
		{
			// Same-vendor siblings are never fail-over targets (same saturated GPU).
			name:      "never falls over to a same-vendor sibling",
			primary:   EncoderNVENC,
			available: []Encoder{EncoderNVENC, EncoderHEVCNVENC, EncoderSoftware},
			want:      []Encoder{EncoderSoftware},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FallbackEncoders(tt.primary, tt.available)
			if !encEq(got, tt.want) {
				t.Errorf("FallbackEncoders(%s) = %v, want %v", tt.primary, got, tt.want)
			}
		})
	}
}
