package transcode

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/media"
)

func dvFile(hdr string, profile int) media.File {
	f := baseFile()
	f.VideoCodec = strPtr("hevc")
	if hdr != "" {
		f.HDRType = strPtr(hdr)
	}
	if profile > 0 {
		f.DVProfile = &profile
	}
	return f
}

func TestDVProfileKey(t *testing.T) {
	for _, tc := range []struct {
		hdr     string
		profile int
		want    string
	}{
		{"dolby_vision", 5, "5"},
		{"dolby_vision", 0, "5"}, // a row from before dv_profile
		{"hdr10", 7, "7"},
		{"hdr10", 8, "8.1"},
		{"hdr10plus", 8, "8.1"},
		{"hlg", 8, "8.4"},
		{"", 8, ""}, // a profile 8 record over SDR video (no usable metadata)
		{"hdr10", 0, ""},
		{"", 0, ""},
	} {
		if got := DVProfileKey(dvFile(tc.hdr, tc.profile)); got != tc.want {
			t.Errorf("hdr %q profile %d: key %q, want %q", tc.hdr, tc.profile, got, tc.want)
		}
	}
}

func TestParseCapabilities_DVProfiles(t *testing.T) {
	for _, tc := range []struct {
		dovi string
		want []string
	}{
		{"dovi=1", []string{"5", "8.1", "8.4"}},
		{"dovi=true", []string{"5", "8.1", "8.4"}},
		{"dovi=0", nil},
		{"dovi=5:8.1", []string{"5", "8.1"}},
		{"dovi=8", []string{"8.1"}},
		{"dovi=8.x:7", []string{"8.1", "8.4", "7"}},
		{"dolbyvision=05:08.1:8.1", []string{"5", "8.1"}},
		{"dovi=9:bogus", nil},
	} {
		caps := ParseCapabilities("videoDecoder=h265," + tc.dovi)
		if !reflect.DeepEqual(caps.DVProfiles, tc.want) {
			t.Errorf("%s: profiles %v, want %v", tc.dovi, caps.DVProfiles, tc.want)
		}
		if caps.SupportsDV != (len(tc.want) > 0) {
			t.Errorf("%s: SupportsDV %v", tc.dovi, caps.SupportsDV)
		}
	}
}

func TestPlanDolbyVision(t *testing.T) {
	only81 := ParseCapabilities("dovi=8.1")
	all := ParseCapabilities("dovi=5:7:8.1:8.4")
	none := ParseCapabilities("videoDecoder=h265")
	for _, tc := range []struct {
		name       string
		file       media.File
		caps       ClientCapabilities
		canConvert bool
		want       DVMode
	}{
		{"8.1 to an 8.1 client", dvFile("hdr10", 8), only81, false, DVKeep},
		{"8.1 to a non-DV client", dvFile("hdr10", 8), none, true, DVNone},
		{"8.4 to an 8.1-only client", dvFile("hlg", 8), only81, false, DVNone},
		{"7 to a client that decodes 7", dvFile("hdr10", 7), all, false, DVKeep},
		{"7 to an 8.1 client, convertible", dvFile("hdr10", 7), only81, true, DVToP81},
		{"7 to an 8.1 client, no converter", dvFile("hdr10", 7), only81, false, DVNone},
		{"5 to a DV client", dvFile("dolby_vision", 5), all, false, DVKeep},
		{"8 over SDR", dvFile("", 8), all, true, DVNone},
	} {
		if got := PlanDolbyVision(tc.file, tc.caps, tc.canConvert); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDecide_DolbyVision(t *testing.T) {
	const base = "videoDecoder=h264:h265,audioDecoder=aac,protocols=mkv:mp4,maxWidth=3840,maxHeight=2160,maxBitDepth=10"
	for _, tc := range []struct {
		name   string
		file   media.File
		caps   string
		server ServerCaps
		want   Decision
	}{
		// The HDR answer doesn't matter when the client shows the file as
		// Dolby Vision; without Dolby Vision an HDR10 base still needs hdr=1.
		{"8.1, DV client without hdr", dvFile("hdr10", 8), base + ",dovi=8.1", defaultServerCaps, DecisionDirectPlay},
		{"8.1, no DV, no hdr", dvFile("hdr10", 8), base, defaultServerCaps, DecisionTranscode},
		{"5, DV client", dvFile("dolby_vision", 5), base + ",dovi=5", defaultServerCaps, DecisionDirectPlay},
		{"5, 8.1-only client", dvFile("dolby_vision", 5), base + ",hdr=1,dovi=8.1", defaultServerCaps, DecisionUnsupported},
		// Profile 5 can't be re-encoded (green): a DV client over its limits
		// gets unsupported, not a transcode.
		{"5, DV client, too big", dvFile("dolby_vision", 5), "videoDecoder=h265,audioDecoder=aac,protocols=mkv,maxWidth=1280,maxHeight=720,maxBitDepth=10,dovi=5", defaultServerCaps, DecisionUnsupported},
		// Profile 7 for a client that only takes 8.1: remuxed to convert when
		// a worker can, otherwise played as-is (its HDR10 base layer).
		{"7, 8.1 client, converter", dvFile("hdr10", 7), base + ",hdr=1,dovi=8.1", ServerCaps{DVConvert: true}, DecisionDirectStream},
		{"7, 8.1 client, no converter", dvFile("hdr10", 7), base + ",hdr=1,dovi=8.1", defaultServerCaps, DecisionDirectPlay},
		{"7, client decodes 7", dvFile("hdr10", 7), base + ",dovi=7", ServerCaps{DVConvert: true}, DecisionDirectPlay},
	} {
		f := tc.file
		f.VideoBitDepth = intPtr(10)
		if got := Decide(f, ParseCapabilities(tc.caps), tc.server); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestBuildHLS_DolbyVision(t *testing.T) {
	build := func(a BuildArgs) []string {
		a.InputPath, a.SessionDir, a.SegmentPrefix = "/media/movie.mkv", "/tmp/sessions/x", "seg"
		a.AudioStreamIndex = -1
		return BuildHLS(a)
	}
	t.Run("keep 8.1", func(t *testing.T) {
		args := build(BuildArgs{Encoder: EncoderCopy, IsHEVC: true, AudioCodec: "aac", DolbyVision: DVKeep, DVProfileKey: "8.1"})
		s := strings.Join(args, " ")
		for _, want := range []string{"-strict unofficial", "-hls_segment_type fmp4"} {
			if !strings.Contains(s, want) {
				t.Errorf("want %q in: %s", want, s)
			}
		}
		if got := argAfter(args, "-tag:v"); got != "hvc1" {
			t.Errorf("-tag:v %q, want hvc1", got)
		}
		if strings.Contains(s, "-bsf:v") {
			t.Errorf("keep must not convert: %s", s)
		}
	})
	t.Run("keep 5 is dvh1", func(t *testing.T) {
		args := build(BuildArgs{Encoder: EncoderCopy, IsHEVC: true, AudioCodec: "copy", DolbyVision: DVKeep, DVProfileKey: "5"})
		if got := argAfter(args, "-tag:v"); got != "dvh1" {
			t.Errorf("-tag:v %q, want dvh1", got)
		}
	})
	t.Run("convert 7", func(t *testing.T) {
		args := build(BuildArgs{Encoder: EncoderCopy, IsHEVC: true, AudioCodec: "copy", DolbyVision: DVToP81, DVProfileKey: "7"})
		if got := argAfter(args, "-bsf:v"); got != dvConvertBSF {
			t.Errorf("-bsf:v %q, want %q", got, dvConvertBSF)
		}
		if got := argAfter(args, "-tag:v"); got != "hvc1" {
			t.Errorf("-tag:v %q, want hvc1", got)
		}
	})
	t.Run("TS request still fMP4", func(t *testing.T) {
		args := build(BuildArgs{Encoder: EncoderCopy, IsHEVC: true, AudioCodec: "aac", TSSegments: true, DolbyVision: DVKeep, DVProfileKey: "8.1"})
		if ext := segmentFilenameExt(t, args); ext != ".m4s" {
			t.Errorf("segments %s, want .m4s", ext)
		}
	})
	t.Run("passthrough keeps one -strict -2", func(t *testing.T) {
		s := strings.Join(build(BuildArgs{Encoder: EncoderCopy, IsHEVC: true, AudioCodec: "copy", AudioPassthrough: true, DolbyVision: DVKeep, DVProfileKey: "8.1"}), " ")
		if strings.Count(s, "-strict") != 1 || !strings.Contains(s, "-strict -2") {
			t.Errorf("want exactly -strict -2: %s", s)
		}
	})
	t.Run("ignored on a re-encode", func(t *testing.T) {
		s := strings.Join(build(BuildArgs{Encoder: EncoderSoftware, IsHEVC: true, AudioCodec: "aac", DolbyVision: DVToP81, DVProfileKey: "7"}), " ")
		if strings.Contains(s, "-strict") || strings.Contains(s, "dovi_rpu") {
			t.Errorf("a re-encode must not carry Dolby Vision options: %s", s)
		}
	})
	t.Run("no mode, unchanged", func(t *testing.T) {
		s := strings.Join(build(BuildArgs{Encoder: EncoderCopy, IsHEVC: true, AudioCodec: "aac", DVProfileKey: "8.1"}), " ")
		if strings.Contains(s, "-strict") || strings.Contains(s, "dovi_rpu") {
			t.Errorf("no Dolby Vision mode must leave the copy as before: %s", s)
		}
	})
}

func TestJobNeeds_DolbyVision(t *testing.T) {
	keep := jobNeeds(TranscodeJob{Encoder: string(EncoderCopy), DolbyVision: DVKeep}).Features
	if !reflect.DeepEqual(keep, []string{WorkerFeatureDolbyVision}) {
		t.Errorf("keep needs %v", keep)
	}
	conv := jobNeeds(TranscodeJob{Encoder: string(EncoderCopy), DolbyVision: DVToP81}).Features
	if !reflect.DeepEqual(conv, []string{WorkerFeatureDolbyVision, WorkerFeatureDVConvert}) {
		t.Errorf("convert needs %v", conv)
	}
	if !(TranscodeJob{Encoder: string(EncoderCopy), DolbyVision: DVKeep}).ForcesFMP4() {
		t.Error("a Dolby Vision copy must force fMP4")
	}
}
