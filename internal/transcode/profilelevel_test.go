package transcode

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/media"
)

func plIntPtr(n int) *int { return &n }

func TestParseCapabilities_VideoLimits(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   map[string]VideoLimit
	}{
		{"absent", "videoDecoder=h264:hevc", nil},
		{"h264 high 4.1", "h264Profile=high,h264Level=4.1", map[string]VideoLimit{"h264": {"high", 41}}},
		{"hevc main10 5.1, case-insensitive key", "HEVCPROFILE=Main10,hevcLevel=5.1",
			map[string]VideoLimit{"h265": {"main10", 51}}},
		{"h265 alias + idc level", "h265Profile=main,h265Level=153", map[string]VideoLimit{"h265": {"main", 51}}},
		{"av1 and h264 together, & separators", "av1Profile=main&av1Level=5.1&h264Level=41",
			map[string]VideoLimit{"av1": {"main", 51}, "h264": {"", 41}}},
		{"bare major level", "h264Level=5", map[string]VideoLimit{"h264": {"", 50}}},
		{"ffprobe-style profile names fold", "h264Profile=High 10", map[string]VideoLimit{"h264": {"high10", 0}}},
		{"constrained baseline is baseline", "h264Profile=constrained_baseline", map[string]VideoLimit{"h264": {"baseline", 0}}},
		{"unknown profile and junk level dropped", "h264Profile=extended,hevcLevel=abc", nil},
		{"vp9 isn't level-gated", "vp9Level=5.1", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCapabilities(tc.header).VideoLimits
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("VideoLimits = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVideoLevelTenths(t *testing.T) {
	cases := []struct {
		codec string
		raw   int
		want  int
	}{
		{"h264", 41, 41}, {"h264", 51, 51}, {"h264", 9, 10}, {"avc1", 40, 40},
		{"hevc", 153, 51}, {"hevc", 120, 40}, {"h265", 93, 31}, {"hevc", 186, 62},
		{"av1", 8, 40}, {"av1", 13, 51}, {"av1", 0, 0}, {"av1", 31, 0}, {"av1", 23, 73},
		{"h264", -99, 0}, {"vp9", 41, 0},
	}
	for _, tc := range cases {
		if got := VideoLevelTenths(tc.codec, tc.raw); got != tc.want {
			t.Errorf("VideoLevelTenths(%s, %d) = %d, want %d", tc.codec, tc.raw, got, tc.want)
		}
	}
}

func TestFormatLevel(t *testing.T) {
	for tenths, want := range map[int]string{41: "4.1", 50: "5", 62: "6.2", 10: "1"} {
		if got := FormatLevel(tenths); got != want {
			t.Errorf("FormatLevel(%d) = %q, want %q", tenths, got, want)
		}
	}
}

func TestLevelMaxHeight(t *testing.T) {
	cases := []struct {
		name       string
		codec      string
		level      int
		w, h       int
		fps        float64
		wantZero   bool // source already fits: no ceiling
		wantBelow  int  // otherwise the ceiling must be below this…
		wantAtLeas int  // …and at least this
	}{
		{"1080p24 fits H.264 4.0", "h264", 40, 1920, 1080, 23.976, true, 0, 0},
		{"1080p30 fits H.264 4.1", "h264", 41, 1920, 1080, 30, true, 0, 0},
		{"1080p60 is over H.264 4.1", "h264", 41, 1920, 1080, 60, false, 1080, 720},
		{"1080p60 fits H.264 4.2", "h264", 42, 1920, 1080, 60, true, 0, 0},
		{"4K is over H.264 4.1", "h264", 41, 3840, 2160, 24, false, 1081, 1080},
		{"4K24 fits HEVC 5.0", "hevc", 50, 3840, 2160, 24, true, 0, 0},
		{"4K60 is over HEVC 5.0", "hevc", 50, 3840, 2160, 60, false, 2160, 1440},
		{"4K is over HEVC 4.1", "h265", 41, 3840, 2160, 24, false, 1200, 1080},
		{"4K is over AV1 4.0", "av1", 40, 3840, 2160, 24, false, 1200, 1080},
		{"scope 4K over HEVC 4.1 keeps 1080 lines wide", "hevc", 41, 3840, 1606, 24, false, 1300, 900},
		{"no level", "h264", 0, 3840, 2160, 24, true, 0, 0},
		{"unknown fps checks size only", "h264", 41, 1920, 1080, 0, true, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LevelMaxHeight(tc.codec, tc.level, tc.w, tc.h, tc.fps)
			if tc.wantZero {
				if got != 0 {
					t.Errorf("got %d, want 0 (fits)", got)
				}
				return
			}
			if got >= tc.wantBelow || got < tc.wantAtLeas || got%2 != 0 {
				t.Errorf("got %d, want even in [%d, %d)", got, tc.wantAtLeas, tc.wantBelow)
			}
			w := int(float64(got)*float64(tc.w)/float64(tc.h) + 0.5)
			w += w & 1
			if !FitsLevel(tc.codec, tc.level, w, got, tc.fps) {
				t.Errorf("%dx%d doesn't fit level %d", w, got, tc.level)
			}
		})
	}
}

func TestLevelMaxKbps(t *testing.T) {
	if got := LevelMaxKbps("h264", 41); got != 50000*2/3 {
		t.Errorf("h264 4.1: %d", got)
	}
	if got := LevelMaxKbps("hevc", 41); got != 20000*2/3 {
		t.Errorf("hevc 4.1: %d", got)
	}
	if got := LevelMaxKbps("h264", 0); got != 0 {
		t.Errorf("no level: %d", got)
	}
}

// Decide: a source over the client's declared profile or level is a
// transcode; one within it, one whose profile/level wasn't probed, or a client
// that declares nothing decides exactly as before.
func TestDecide_ProfileLevel(t *testing.T) {
	str := func(s string) *string { return &s }
	file := func(codec, profile string, level, h int) media.File {
		f := media.File{
			VideoCodec: str(codec), AudioCodec: str("aac"), Container: str("mp4"),
			ResolutionW: plIntPtr(h * 16 / 9), ResolutionH: plIntPtr(h),
		}
		if profile != "" {
			f.VideoProfile = str(profile)
		}
		if level > 0 {
			f.VideoLevel = plIntPtr(level)
		}
		return f
	}
	const base = "videoDecoder=h264:h265:av1,audioDecoder=aac,protocols=mp4,maxWidth=3840,maxHeight=2160,maxbitdepth=10"
	cases := []struct {
		name string
		file media.File
		caps string
		want Decision
	}{
		{"no limits declared: direct play", file("h264", "High", 51, 1080), base, DecisionDirectPlay},
		{"h264 High@4.1 within High@4.1", file("h264", "High", 41, 1080), base + ",h264Profile=high,h264Level=4.1", DecisionDirectPlay},
		{"h264 level 5.1 over 4.1", file("h264", "High", 51, 1080), base + ",h264Level=4.1", DecisionTranscode},
		{"h264 High over Main", file("h264", "High", 40, 1080), base + ",h264Profile=main", DecisionTranscode},
		{"h264 Main within High", file("h264", "Main", 40, 1080), base + ",h264Profile=high", DecisionDirectPlay},
		{"hevc Main 10 over Main", file("hevc", "Main 10", 120, 1080), base + ",hevcProfile=main", DecisionTranscode},
		{"hevc Main 10 within Main10@5.1", file("hevc", "Main 10", 153, 2160), base + ",hevcProfile=main10,hevcLevel=5.1", DecisionDirectPlay},
		{"hevc 5.1 over 5.0", file("hevc", "Main 10", 153, 2160), base + ",hevcLevel=5.0", DecisionTranscode},
		{"hevc limits don't gate h264", file("h264", "High", 51, 1080), base + ",hevcLevel=4.1", DecisionDirectPlay},
		{"av1 5.1 over 4.1", file("av1", "Main", 13, 2160), base + ",av1Level=4.1", DecisionTranscode},
		{"unprobed profile/level: not gated", file("h264", "", 0, 1080), base + ",h264Profile=baseline,h264Level=3.0", DecisionDirectPlay},
		{"unranked profile (Extended): not gated", file("h264", "Extended", 30, 480), base + ",h264Profile=main", DecisionDirectPlay},
		{"level over in mkv: transcode, not remux", func() media.File {
			f := file("h264", "High", 51, 1080)
			f.Container = str("matroska")
			return f
		}(), base + ",h264Level=4.1", DecisionTranscode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.file, ParseCapabilities(tc.caps), ServerCaps{}); got != tc.want {
				t.Errorf("Decide = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDecisionReasons_ProfileLevel(t *testing.T) {
	str := func(s string) *string { return &s }
	f := media.File{VideoCodec: str("h264"), AudioCodec: str("aac"), Container: str("mp4"),
		VideoProfile: str("High 10"), VideoLevel: plIntPtr(51), ResolutionW: plIntPtr(1920), ResolutionH: plIntPtr(1080)}
	caps := ParseCapabilities("videoDecoder=h264,audioDecoder=aac,protocols=mp4,h264Profile=high,h264Level=4.1")
	texts := strings.Join(ReasonTexts(DecisionReasons(f, caps)), " | ")
	for _, want := range []string{"H.264 High 10 profile → client max High", "H.264 level 5.1 → client max 4.1"} {
		if !strings.Contains(texts, want) {
			t.Errorf("reasons %q missing %q", texts, want)
		}
	}
}

func TestOutputLimitsFor(t *testing.T) {
	caps := ParseCapabilities("h264Profile=main,h264Level=4.1,hevcProfile=main,hevcLevel=5.1,av1Level=5.0")
	got := OutputLimitsFor(caps, 23.976)
	want := OutputLimits{H264Profile: "main", H264Level: 41, HEVCLevel: 51, AV1Level: 50, FrameRate: 23.976}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if !caps.HEVCMain8Only() {
		t.Error("hevcProfile=main must force 8-bit HEVC output")
	}
	// High (or above) pins nothing: H.264 output is High at most.
	if o := OutputLimitsFor(ParseCapabilities("h264Profile=high10"), 24); !o.IsZero() {
		t.Errorf("high10 ceiling: %+v, want zero", o)
	}
	if o := OutputLimitsFor(ParseCapabilities(""), 24); !o.IsZero() || o.FrameRate != 0 {
		t.Errorf("no profile: %+v, want zero", o)
	}
}

// Each encoder takes the level in its own form; a picture over the level
// (an encoder fallback the API didn't size for) gets no level at all.
func TestOutputLimitArgs(t *testing.T) {
	lim := OutputLimits{H264Profile: "main", H264Level: 41, HEVCLevel: 50, AV1Level: 40, FrameRate: 24}
	cases := []struct {
		enc  Encoder
		w, h int
		want string
	}{
		{EncoderSoftware, 1920, 1080, "-profile:v main -level 4.1"},
		{EncoderNVENC, 1920, 1080, "-profile:v main -level 4.1"},
		{EncoderAMF, 1920, 1080, "-profile:v main -level 4.1"},
		{EncoderQSV, 1920, 1080, "-profile:v main -level 41"},
		{EncoderVAAPI, 1920, 1080, "-profile:v main -level 4.1"},
		{EncoderHEVCSoftware, 3840, 2160, "-x265-params level-idc=5.0"},
		{EncoderHEVCNVENC, 3840, 2160, "-level 5.0"},
		{EncoderHEVCAMF, 3840, 2160, "-level 5.0"},
		{EncoderHEVCQSV, 3840, 2160, "-level 50"},
		{EncoderHEVCVAAPI, 3840, 2160, "-level 5"},
		{EncoderAV1NVENC, 1920, 1080, "-level 4.0"},
		{EncoderAV1QSV, 1920, 1080, "-level 40"},
		{EncoderAV1Software, 1920, 1080, ""},
		// A 4K H.264 fallback is over 4.1: the profile still, but no level.
		{EncoderNVENC, 3840, 2160, "-profile:v main"},
	}
	for _, tc := range cases {
		got := strings.Join(outputLimitArgs(tc.enc, lim, tc.w, tc.h), " ")
		if got != tc.want {
			t.Errorf("%s %dx%d: %q, want %q", tc.enc, tc.w, tc.h, got, tc.want)
		}
	}
	baseline := OutputLimits{H264Profile: "baseline"}
	for enc, want := range map[Encoder]string{
		EncoderSoftware: "baseline", EncoderNVENC: "baseline", EncoderQSV: "baseline",
		EncoderAMF: "constrained_baseline", EncoderVAAPI: "constrained_baseline",
	} {
		if got := argAfter(outputLimitArgs(enc, baseline, 1280, 720), "-profile:v"); got != want {
			t.Errorf("%s baseline: %q, want %q", enc, got, want)
		}
	}
	if got := outputLimitArgs(EncoderHEVCNVENC, baseline, 1280, 720); len(got) != 0 {
		t.Errorf("an H.264 profile pin leaked onto HEVC: %v", got)
	}
}

// BuildHLS carries the limits on a re-encode and never on a copy.
func TestBuildHLS_OutputLimits(t *testing.T) {
	lim := OutputLimits{H264Profile: "main", H264Level: 41, FrameRate: 24}
	args := BuildHLS(BuildArgs{
		InputPath: "/media/x.mkv", Encoder: EncoderSoftware, Width: 1920, Height: 1080, BitrateKbps: 8000,
		AudioCodec: "aac", AudioStreamIndex: -1, OutputLimits: lim,
		SessionDir: "/tmp/s", SegmentPrefix: "seg",
	})
	if argAfter(args, "-profile:v") != "main" || argAfter(args, "-level") != "4.1" {
		t.Errorf("re-encode lacks the limits: %v", args)
	}
	copyArgs := BuildHLS(BuildArgs{
		InputPath: "/media/x.mkv", Encoder: EncoderCopy, AudioCodec: "aac", AudioStreamIndex: -1,
		OutputLimits: lim, SessionDir: "/tmp/s", SegmentPrefix: "seg",
	})
	if s := strings.Join(copyArgs, " "); strings.Contains(s, "-level") || strings.Contains(s, "-profile:v") {
		t.Errorf("a copy got encoder limits: %s", s)
	}
}

func TestMasterCodecs(t *testing.T) {
	cases := []struct {
		codec    string
		h, cap   int
		audio    string
		want     string
		wantSame string // the legacy helper this must match ("" = skip)
	}{
		{LadderHEVC, 1080, 0, "", "hvc1.1.6.L123.B0,mp4a.40.2", HEVCMasterCodecs(1080)},
		{LadderHEVC, 2160, 0, "", "hvc1.1.6.L153.B0,mp4a.40.2", HEVCMasterCodecs(2160)},
		{LadderHEVC, 4320, 0, "", "hvc1.1.6.L180.B0,mp4a.40.2", HEVCMasterCodecs(4320)},
		{LadderAV1, 1080, 0, "", "av01.0.08M.08,mp4a.40.2", AV1MasterCodecs(1080)},
		{LadderAV1, 2160, 0, "", "av01.0.13M.08,mp4a.40.2", AV1MasterCodecs(2160)},
		{LadderHEVC, 2160, 50, "eac3", "hvc1.1.6.L150.B0,ec-3", ""},
		{LadderHEVC, 1080, 51, "truehd", "hvc1.1.6.L123.B0,mlpa", ""},
		{LadderAV1, 2160, 50, "ac3", "av01.0.12M.08,ac-3", ""},
		{LadderAV1, 1080, 0, "dts", "av01.0.08M.08,mp4a.a9", ""},
		{LadderH264, 1080, 41, "ac3", "", ""},
	}
	for _, tc := range cases {
		got := MasterCodecs(tc.codec, tc.h, tc.cap, tc.audio)
		if got != tc.want {
			t.Errorf("MasterCodecs(%s, %d, %d, %q) = %q, want %q", tc.codec, tc.h, tc.cap, tc.audio, got, tc.want)
		}
		if tc.wantSame != "" && got != tc.wantSame {
			t.Errorf("legacy helper drifted: %q vs %q", got, tc.wantSame)
		}
	}
}

// A ladder whose rungs copy a passed-through track advertises its bitrate in
// BANDWIDTH instead of the AAC allowance.
func TestBuildMasterPlaylistAudio_PassthroughBandwidth(t *testing.T) {
	rends := []Rendition{{Label: "1080p", Width: 1920, Height: 1080, BitrateKbps: 5000}}
	path := func(r Rendition) string { return r.Label + ".m3u8" }
	m := BuildMasterPlaylistAudio(rends, MasterCodecs(LadderHEVC, 1080, 0, "truehd"), PassthroughAudioKbps("truehd"), path)
	if !strings.Contains(m, "BANDWIDTH=11000000") || !strings.Contains(m, `CODECS="hvc1.1.6.L123.B0,mlpa"`) {
		t.Errorf("master:\n%s", m)
	}
	if BuildMasterPlaylist(rends, "", path) != BuildMasterPlaylistAudio(rends, "", 0, path) {
		t.Error("BuildMasterPlaylist must stay the AAC-allowance form")
	}
}
