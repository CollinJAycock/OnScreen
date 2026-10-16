package transcode

import (
	"strings"
	"testing"
)

func TestParseSourceProbe_ParamSetsInBand(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want bool
	}{
		{"extradata_size=23\nbit_rate=9000000\n", true}, // hev1 MP4, empty hvcC
		{"extradata_size=12\n", true},
		{"extradata_size=206\n", false}, // hvcC with VPS/SPS/PPS
		{"extradata_size=0\n", false},   // no record at all (not an hvcC)
		{"bit_rate=9000000\n", false},
		{"extradata_size=x\n", false},
	} {
		if got := parseSourceProbe(tc.out).paramSetsInBand; got != tc.want {
			t.Errorf("%q: paramSetsInBand %v, want %v", tc.out, got, tc.want)
		}
	}
}

func TestBuildHLS_HEVCParamSetsInBand(t *testing.T) {
	build := func(a BuildArgs) []string {
		a.InputPath, a.SessionDir, a.SegmentPrefix = "/media/clip.mp4", "/tmp/sessions/x", "seg"
		a.AudioStreamIndex = -1
		a.AudioCodec = "aac"
		return BuildHLS(a)
	}
	t.Run("hvc1 copy rebuilds the record", func(t *testing.T) {
		args := build(BuildArgs{Encoder: EncoderCopy, IsHEVC: true, HEVCParamSetsInBand: true})
		if got := argAfter(args, "-bsf:v"); got != hevcConfigBSF {
			t.Errorf("-bsf:v %q, want %q", got, hevcConfigBSF)
		}
		if got := argAfter(args, "-tag:v"); got != "hvc1" {
			t.Errorf("-tag:v %q, want hvc1", got)
		}
	})
	t.Run("after the Dolby Vision conversion", func(t *testing.T) {
		args := build(BuildArgs{Encoder: EncoderCopy, IsHEVC: true, HEVCParamSetsInBand: true, DolbyVision: DVToP81, DVProfileKey: "7"})
		if got, want := argAfter(args, "-bsf:v"), dvConvertBSF+","+hevcConfigBSF; got != want {
			t.Errorf("-bsf:v %q, want %q", got, want)
		}
		if n := strings.Count(strings.Join(args, " "), "-bsf:v"); n != 1 {
			t.Errorf("%d -bsf:v options, want 1", n)
		}
	})
	t.Run("a complete record is left alone", func(t *testing.T) {
		if s := strings.Join(build(BuildArgs{Encoder: EncoderCopy, IsHEVC: true}), " "); strings.Contains(s, "-bsf:v") {
			t.Errorf("unexpected -bsf:v: %s", s)
		}
	})
	t.Run("MPEG-TS needs no record", func(t *testing.T) {
		s := strings.Join(build(BuildArgs{Encoder: EncoderCopy, IsHEVC: true, TSSegments: true, HEVCParamSetsInBand: true}), " ")
		if strings.Contains(s, "-bsf:v") {
			t.Errorf("unexpected -bsf:v on TS: %s", s)
		}
	})
	t.Run("a re-encode makes its own", func(t *testing.T) {
		s := strings.Join(build(BuildArgs{Encoder: EncoderSoftware, IsHEVC: true, HEVCParamSetsInBand: true}), " ")
		if strings.Contains(s, "extract_extradata") {
			t.Errorf("unexpected bsf on a re-encode: %s", s)
		}
	})
}
