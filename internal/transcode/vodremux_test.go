package transcode

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// The irregular keyframes of the experiment file (ffmpeg -force_key_frames).
var expKeyframes = []float64{0, 1.5, 4, 4.5, 9, 13.208, 14, 20, 21, 27.5, 30, 31, 38, 45, 46, 52.417, 60, 61.5, 67, 75, 82, 88}

func TestPlanRemux_GroupsKeyframes(t *testing.T) {
	p, ok := PlanRemux(expKeyframes, 90)
	if !ok {
		t.Fatal("plan refused")
	}
	want := []float64{0, 4, 9, 13.208, 20, 27.5, 38, 45, 52.417, 60, 67, 75, 82, 88} // 30 and 31 are under 4 s after 27.5
	if fmt.Sprint(p.Starts) != fmt.Sprint(want) {
		t.Fatalf("starts = %v\nwant     %v", p.Starts, want)
	}
	for i := 1; i < p.Count(); i++ {
		s, e := p.Span(i - 1)
		if e-s < RemuxSegmentTarget-1e-9 {
			t.Errorf("segment %d is %.3f s, shorter than the target", i-1, e-s)
		}
	}
	if s, e := p.Span(p.Count() - 1); s != 88 || e != 90 {
		t.Errorf("last span = [%v,%v), want [88,90)", s, e)
	}
	if p.TargetDuration() != 11 { // 27.5 -> 38 is 10.5 s
		t.Errorf("TargetDuration = %d, want 11", p.TargetDuration())
	}
}

func TestPlanRemux_SkipsTailSliverAndCrowdedKeyframes(t *testing.T) {
	// 9.9 is too close to the end; 5.0 has a keyframe 0.1 s later, so a seek
	// to 5.0+lead without the B-frame correction would land on 5.1.
	p, ok := PlanRemux([]float64{0, 5.0, 5.1, 9.9}, 10.5)
	if !ok {
		t.Fatal("plan refused")
	}
	if fmt.Sprint(p.Starts) != "[0 5.1]" {
		t.Fatalf("starts = %v, want [0 5.1]", p.Starts)
	}
}

func TestPlanRemux_Refusals(t *testing.T) {
	cases := map[string]struct {
		kf  []float64
		dur float64
	}{
		"no keyframes":       {nil, 100},
		"unknown duration":   {[]float64{0, 5, 10}, 0},
		"late first frame":   {[]float64{10, 15, 20}, 100},
		"intra only (dense)": {intraOnly(24, 60), 60},
	}
	for name, c := range cases {
		if _, ok := PlanRemux(c.kf, c.dur); ok {
			t.Errorf("%s: plan accepted, want refused", name)
		}
	}
	// A short clip is fine as one segment.
	if p, ok := PlanRemux([]float64{0}, 6); !ok || p.Count() != 1 {
		t.Errorf("short clip: ok=%v count=%d, want one segment", ok, p.Count())
	}
}

func intraOnly(fps float64, dur float64) []float64 {
	var out []float64
	for t := 0.0; t < dur; t += 1 / fps {
		out = append(out, math.Round(t*1000)/1000)
	}
	return out
}

func TestRemuxPlan_SegmentAtAndSeekTarget(t *testing.T) {
	p, _ := PlanRemux(expKeyframes, 90)
	for _, c := range []struct {
		t    float64
		want int
	}{{0, 0}, {3.99, 0}, {4, 1}, {29.9, 5}, {31, 5}, {38, 6}, {89, 13}, {500, 13}} {
		if got := p.SegmentAt(c.t); got != c.want {
			t.Errorf("SegmentAt(%v) = %d, want %d", c.t, got, c.want)
		}
	}
	if p.SeekTarget(0) != 0 {
		t.Errorf("SeekTarget(0) = %v, want 0", p.SeekTarget(0))
	}
	if got := p.SeekTarget(6); math.Abs(got-38.132) > 1e-9 {
		t.Errorf("SeekTarget(6) = %v, want 38.132", got)
	}
}

func TestRemuxPlan_Playlist(t *testing.T) {
	p, _ := PlanRemux([]float64{0, 4.5, 9, 13.5}, 15)
	pl := p.Playlist(func(i int) string { return fmt.Sprintf("seg/%d.ts?token=x", i) }, "")
	for _, want := range []string{
		"#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-TARGETDURATION:5", "#EXT-X-ENDLIST",
		"#EXTINF:4.500,\nseg/0.ts?token=x", "#EXTINF:1.500,\nseg/3.ts?token=x",
	} {
		if !strings.Contains(pl, want) {
			t.Errorf("playlist missing %q:\n%s", want, pl)
		}
	}
	if strings.Contains(pl, "EXT-X-MAP") {
		t.Error("MPEG-TS playlist must not carry EXT-X-MAP")
	}
	fmp4 := p.Playlist(func(i int) string { return fmt.Sprintf("%d.m4s", i) }, "init.mp4?token=x")
	if !strings.Contains(fmp4, `#EXT-X-MAP:URI="init.mp4?token=x"`) {
		t.Errorf("fMP4 playlist missing its init map:\n%s", fmp4)
	}
}

func TestRemuxTSOffset(t *testing.T) {
	if got := RemuxTSOffset(2, 24); math.Abs(got-0.175) > 1e-9 { // 3/24 + 0.05
		t.Errorf("RemuxTSOffset(2, 24) = %v, want 0.175", got)
	}
	if got := RemuxTSOffset(0, 60); got != 0.1 { // floor
		t.Errorf("RemuxTSOffset(0, 60) = %v, want 0.1", got)
	}
	if got := RemuxTSOffset(2, 0); got != 1.0 { // unknown fps
		t.Errorf("RemuxTSOffset(2, 0) = %v, want 1", got)
	}
}

func TestParseRunPieces(t *testing.T) {
	pl := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:7\n#EXT-X-PLAYLIST-TYPE:EVENT\n" +
		"#EXTINF:1.500000,\nseg00000.ts\n#EXTINF:2.500000,\nseg00001.ts\n#EXTINF:0.500000,\nseg00002.ts\n"
	d, ended := ParseRunPieces(pl)
	if fmt.Sprint(d) != "[1.5 2.5 0.5]" || ended {
		t.Fatalf("got %v ended=%v", d, ended)
	}
	if _, ended := ParseRunPieces(pl + "#EXT-X-ENDLIST\n"); !ended {
		t.Fatal("ENDLIST not seen")
	}
}

// pieceDurs is what ffmpeg writes for a run from keyframe index i0 (one
// piece per keyframe), up to keyframe index i1 (exclusive), on the
// experiment file.
func pieceDurs(i0, i1 int) []float64 {
	var out []float64
	for i := i0; i < i1; i++ {
		out = append(out, expKeyframes[i+1]-expKeyframes[i])
	}
	return out
}

func TestRemuxPieceSpan(t *testing.T) {
	p, _ := PlanRemux(expKeyframes, 90)

	// Run from 0, finished through keyframe 30 (index 10).
	durs := pieceDurs(0, 10)
	s0, e0 := p.Span(1) // [4, 9): pieces 2 (4) and 3 (4.5)
	if got := RemuxPieceSpan(0, durs, false, s0, e0, false); !got.Ready || got.First != 2 || got.Last != 3 {
		t.Errorf("segment 1 = %+v, want pieces 2..3 ready", got)
	}
	// Segment [27.5, 31) needs the piece ending at 31 — not written yet.
	s, e := p.Span(5)
	if got := RemuxPieceSpan(0, durs, false, s, e, false); got.Ready || got.Mismatch || got.First != 9 {
		t.Errorf("segment 5 = %+v, want waiting from piece 9", got)
	}
	// Far ahead of the run: wait (the caller decides whether to restart).
	s, e = p.Span(10)
	if got := RemuxPieceSpan(0, durs, false, s, e, false); got.Ready || got.Mismatch {
		t.Errorf("segment 10 = %+v, want not ready, no mismatch", got)
	}

	// A restart at segment 6 (38 s, keyframe index 12), run to the end.
	rd := pieceDurs(12, 21)
	rd = append(rd, 90-88)
	s, e = p.Span(6) // [38, 45): piece 0 only
	if got := RemuxPieceSpan(38, rd, true, s, e, false); !got.Ready || got.First != 0 || got.Last != 0 {
		t.Errorf("restart seg 6 = %+v, want piece 0", got)
	}
	last := p.Count() - 1
	s, e = p.Span(last)
	if got := RemuxPieceSpan(38, rd, true, s, e, true); !got.Ready || got.First != len(rd)-1 || got.Last != len(rd)-1 {
		t.Errorf("last segment = %+v, want the final piece", got)
	}
	// The last segment before ENDLIST is not ready even with its piece listed.
	if got := RemuxPieceSpan(38, rd, false, s, e, true); got.Ready {
		t.Errorf("last segment before ENDLIST = %+v, want waiting", got)
	}
	// Before the run's start: it can never serve it.
	s, e = p.Span(2)
	if got := RemuxPieceSpan(38, rd, true, s, e, false); !got.Mismatch {
		t.Errorf("segment before the run = %+v, want mismatch", got)
	}
}

func TestRemuxPieceSpan_UnindexedKeyframes(t *testing.T) {
	// The Cues indexed only 0, 10, 20 (one per cluster) but the encode has a
	// keyframe every 2.5 s: ffmpeg writes 2.5 s pieces, and a segment is the
	// four pieces between two indexed keyframes.
	p, ok := PlanRemux([]float64{0, 10, 20}, 30)
	if !ok {
		t.Fatal("plan refused")
	}
	var durs []float64
	for range 12 {
		durs = append(durs, 2.5)
	}
	s, e := p.Span(1)
	if got := RemuxPieceSpan(0, durs, true, s, e, false); !got.Ready || got.First != 4 || got.Last != 7 {
		t.Errorf("segment 1 = %+v, want pieces 4..7", got)
	}
}

func TestRemuxPieceSpan_DriftIsAMismatch(t *testing.T) {
	// Pieces that never land on the 4 s boundary (the run started somewhere
	// else) must be reported, not served as the wrong content.
	p, _ := PlanRemux(expKeyframes, 90)
	s, e := p.Span(1)
	if got := RemuxPieceSpan(0, []float64{3, 3, 3}, false, s, e, false); !got.Mismatch {
		t.Errorf("got %+v, want mismatch", got)
	}
}

func TestBuildHLS_VODRemuxRunFlags(t *testing.T) {
	args := strings.Join(BuildHLS(BuildArgs{
		InputPath:         "/media/x.mkv",
		StartOffset:       13.34,
		OutputTSOffsetSec: 0.175,
		CopyTS:            true,
		KeyframePieces:    true,
		Encoder:           "copy",
		AudioCodec:        "aac",
		AudioChannels:     2,
		SessionDir:        "/tmp/s",
		SegmentPrefix:     "seg",
	}), " ")
	for _, want := range []string{
		"-copyts -ss 13.340 -noaccurate_seek",
		"-output_ts_offset 0.175000",
		"-hls_time " + RemuxPieceHLSTime + " ",
		"-hls_segment_options mpegts_copyts=1",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q:\n%s", want, args)
		}
	}
	if strings.Contains(args, "make_zero") {
		t.Errorf("a CopyTS run must not shift its timeline to zero:\n%s", args)
	}

	// An ordinary remux keeps its 4 s segments and rebased timestamps.
	plain := strings.Join(BuildHLS(BuildArgs{
		InputPath: "/media/x.mkv", StartOffset: 13.34, Encoder: "copy", AudioCodec: "aac",
		AudioChannels: 2, SessionDir: "/tmp/s", SegmentPrefix: "seg",
	}), " ")
	if strings.Contains(plain, "-copyts") || !strings.Contains(plain, fmt.Sprintf("-hls_time %d ", SegmentDuration)) ||
		!strings.Contains(plain, "make_zero") {
		t.Errorf("ordinary remux args changed:\n%s", plain)
	}
}
