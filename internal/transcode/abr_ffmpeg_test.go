package transcode

import (
	"strings"
	"testing"
)

// argValues returns every value passed for the given repeated flag.
func argValues(args []string, flag string) []string {
	var out []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			out = append(out, args[i+1])
		}
	}
	return out
}

func argValue(args []string, flag string) string {
	v := argValues(args, flag)
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func TestBuildABRHLS_StructureForThreeRungs(t *testing.T) {
	ladder := BuildLadder(1920, 1080, 0, false, 720) // 720p + 480p + 360p = 3 rungs
	if len(ladder) != 3 {
		t.Fatalf("precondition: expected 3 rungs, got %d", len(ladder))
	}
	a := BuildArgs{
		InputPath:  "/media/movie.mkv",
		SessionDir: "/tmp/sess",
		AudioCodec: "aac",
	}
	args := BuildABRHLS(a, ladder)

	// One -map pair (video + audio) per rung → 2N maps total.
	if maps := argValues(args, "-map"); len(maps) != 2*len(ladder) {
		t.Errorf("got %d -map args, want %d", len(maps), 2*len(ladder))
	}

	// var_stream_map must have one group per rung, arity matching the maps.
	vsm := argValue(args, "-var_stream_map")
	if got := len(strings.Fields(vsm)); got != len(ladder) {
		t.Errorf("var_stream_map %q has %d groups, want %d", vsm, got, len(ladder))
	}
	if !strings.Contains(vsm, "v:0,a:0") || !strings.Contains(vsm, "v:2,a:2") {
		t.Errorf("var_stream_map %q missing expected v:i,a:i groups", vsm)
	}

	// Per-rung video bitrate flags.
	for i := range ladder {
		if argValue(args, "-b:v:"+itoa(i)) == "" {
			t.Errorf("missing -b:v:%d", i)
		}
	}

	// Master playlist name + per-variant (%v) segment/playlist paths.
	if argValue(args, "-master_pl_name") != "master.m3u8" {
		t.Errorf("master_pl_name = %q, want master.m3u8", argValue(args, "-master_pl_name"))
	}
	if seg := argValue(args, "-hls_segment_filename"); !strings.Contains(seg, "%v") {
		t.Errorf("segment filename %q must contain %%v", seg)
	}
	// Last arg is the per-variant playlist output template.
	if out := args[len(args)-1]; !strings.Contains(out, "%v") || !strings.HasSuffix(out, "index.m3u8") {
		t.Errorf("playlist output %q must be a %%v index.m3u8 template", out)
	}

	// filter_complex must split into N and scale each branch.
	fc := argValue(args, "-filter_complex")
	if !strings.Contains(fc, "split=3") {
		t.Errorf("filter_complex %q missing split=3", fc)
	}
	if !strings.Contains(fc, "[vo0]") || !strings.Contains(fc, "[vo2]") {
		t.Errorf("filter_complex %q missing scaled outputs", fc)
	}
}

func TestBuildABRHLS_TonemapPrependedBeforeSplit(t *testing.T) {
	ladder := BuildLadder(3840, 2160, 0, false, 0)
	a := BuildArgs{InputPath: "/m.mkv", SessionDir: "/tmp/s", AudioCodec: "aac", NeedsToneMap: true}
	fc := argValue(BuildABRHLS(a, ladder), "-filter_complex")
	if !strings.Contains(fc, "tonemap") {
		t.Errorf("tonemap requested but filter_complex has no tonemap: %q", fc)
	}
	// Tonemap must feed the split (the [tm] label is the split input).
	if !strings.Contains(fc, "[tm]split=") {
		t.Errorf("tonemap output must feed split: %q", fc)
	}
}

func TestBuildABRHLS_AudioStreamSelection(t *testing.T) {
	ladder := BuildLadder(1280, 720, 0, false, 0)
	a := BuildArgs{InputPath: "/m.mkv", SessionDir: "/tmp/s", AudioCodec: "aac", AudioStreamIndex: 2}
	maps := argValues(BuildABRHLS(a, ladder), "-map")
	found := false
	for _, m := range maps {
		if m == "0:a:2" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected audio map 0:a:2, got maps %v", maps)
	}
}
