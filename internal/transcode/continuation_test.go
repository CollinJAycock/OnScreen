package transcode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPlaylistDurationSec(t *testing.T) {
	pl := []byte(strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:6",
		"#EXT-X-TARGETDURATION:4",
		"#EXT-X-MEDIA-SEQUENCE:0",
		"#EXTINF:4.000000,",
		"seg00000.ts",
		"#EXTINF:4.000000,",
		"seg00001.ts",
		"#EXTINF:0.200000,",
		"seg00002.ts",
		"#EXT-X-ENDLIST",
	}, "\n"))
	got := playlistDurationSec(pl)
	if want := 8.2; got < want-0.001 || got > want+0.001 {
		t.Fatalf("playlistDurationSec = %v, want %v", got, want)
	}
}

func TestExtractSegmentBody(t *testing.T) {
	pl := []byte(strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:6",
		"#EXT-X-TARGETDURATION:4",
		"#EXT-X-MEDIA-SEQUENCE:5",
		"#EXT-X-PLAYLIST-TYPE:EVENT",
		"#EXT-X-INDEPENDENT-SEGMENTS",
		`#EXT-X-MAP:URI="init.mp4"`,
		"#EXTINF:4.000000,",
		"seg00005.ts",
		"#EXTINF:4.000000,",
		"seg00006.ts",
		"#EXT-X-ENDLIST",
	}, "\n"))
	body := extractSegmentBody(pl)
	want := []string{
		`#EXT-X-MAP:URI="init.mp4"`,
		"#EXTINF:4.000000,",
		"seg00005.ts",
		"#EXTINF:4.000000,",
		"seg00006.ts",
	}
	if strings.Join(body, "\n") != strings.Join(want, "\n") {
		t.Fatalf("extractSegmentBody =\n%s\nwant\n%s", strings.Join(body, "\n"), strings.Join(want, "\n"))
	}
}

func TestStitchPlaylist_WithBody(t *testing.T) {
	prefix := []byte(strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:6",
		"#EXT-X-TARGETDURATION:4",
		"#EXT-X-MEDIA-SEQUENCE:0",
		"#EXT-X-PLAYLIST-TYPE:EVENT",
		"#EXT-X-INDEPENDENT-SEGMENTS",
		"#EXTINF:4.000000,",
		"seg00000.ts",
	}, "\n") + "\n")
	body := []string{"#EXTINF:4.000000,", "seg00005.ts"}
	out := string(stitchPlaylist(prefix, body, true))

	if strings.Count(out, "#EXT-X-DISCONTINUITY") != 1 {
		t.Fatalf("want exactly 1 DISCONTINUITY, got:\n%s", out)
	}
	if !strings.Contains(out, "#EXT-X-MEDIA-SEQUENCE:0") {
		t.Fatalf("MEDIA-SEQUENCE not preserved:\n%s", out)
	}
	if strings.Count(out, "#EXT-X-ENDLIST") != 1 || !strings.HasSuffix(strings.TrimSpace(out), "#EXT-X-ENDLIST") {
		t.Fatalf("ENDLIST must appear once at the end:\n%s", out)
	}
	// DISCONTINUITY must sit between the prefix's last segment and the body.
	di := strings.Index(out, "#EXT-X-DISCONTINUITY")
	if strings.Index(out, "seg00000.ts") > di || strings.Index(out, "seg00005.ts") < di {
		t.Fatalf("DISCONTINUITY misplaced:\n%s", out)
	}
}

func TestStitchPlaylist_EmptyBody_NoDanglingDiscontinuity(t *testing.T) {
	prefix := []byte("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:4.000000,\nseg00000.ts\n")
	out := string(stitchPlaylist(prefix, nil, false))
	if strings.Contains(out, "#EXT-X-DISCONTINUITY") {
		t.Fatalf("empty continuation must not add a DISCONTINUITY:\n%s", out)
	}
	if strings.Contains(out, "#EXT-X-ENDLIST") {
		t.Fatalf("ended=false must not add ENDLIST:\n%s", out)
	}
}

func TestStitchPlaylist_StripsPriorEndlist(t *testing.T) {
	// A prefix that already ended (ffmpeg wrote ENDLIST on its premature exit)
	// must have it stripped before the continuation is appended.
	prefix := []byte("#EXTM3U\n#EXTINF:4.000000,\nseg00000.ts\n#EXT-X-ENDLIST\n")
	out := string(stitchPlaylist(prefix, []string{"#EXTINF:4.000000,", "seg00001.ts"}, false))
	if strings.Count(out, "#EXT-X-ENDLIST") != 0 {
		t.Fatalf("prior ENDLIST should be stripped when not ended:\n%s", out)
	}
	if !strings.Contains(out, "seg00001.ts") || !strings.Contains(out, "#EXT-X-DISCONTINUITY") {
		t.Fatalf("continuation not appended after stripping ENDLIST:\n%s", out)
	}
	// seg00000 must precede the discontinuity, seg00001 must follow it.
	if strings.Index(out, "seg00000.ts") > strings.Index(out, "#EXT-X-DISCONTINUITY") {
		t.Fatalf("ordering wrong:\n%s", out)
	}
}

func TestShortOfSource(t *testing.T) {
	// shortCompletionMarginSec is 12s; source = 2617s (Babylon S03E19).
	cases := []struct {
		name             string
		produced, source float64
		want             bool
	}{
		{"well short", 2000, 2617, true},
		{"barely short, past margin", 2604, 2617, true}, // 2617-12 = 2605
		{"just inside margin", 2606, 2617, false},
		{"exactly at end", 2617, 2617, false},
		{"unknown source duration", 1000, 0, false},
		{"start of file", 100, 2617, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortOfSource(tc.produced, tc.source); got != tc.want {
				t.Errorf("shortOfSource(%v, %v) = %v, want %v", tc.produced, tc.source, got, tc.want)
			}
		})
	}
}

func TestContinuationSkip(t *testing.T) {
	// step = 4s, cap = 60s.
	cases := []struct {
		name       string
		progressed bool
		cur        float64
		wantNext   float64
		wantGiveUp bool
	}{
		{"progress resets skip", true, 8, 0, false},
		{"progress from zero stays zero", true, 0, 0, false},
		{"no progress grows by a step", false, 0, 4, false},
		{"no progress near cap still tries", false, 56, 60, false},
		{"no progress over cap gives up", false, 60, 64, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, giveUp := continuationSkip(tc.progressed, tc.cur)
			if next != tc.wantNext || giveUp != tc.wantGiveUp {
				t.Errorf("continuationSkip(%v, %v) = (%v, %v), want (%v, %v)",
					tc.progressed, tc.cur, next, giveUp, tc.wantNext, tc.wantGiveUp)
			}
		})
	}
}

func TestBuildHLS_StartNumber(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath: "/in.mkv", Encoder: "copy", SessionDir: "/sess",
		SegmentPrefix: "seg", AudioCodec: "aac", AudioChannels: 2,
		StartNumber: 42,
	})
	if !hasArgPair(args, "-start_number", "42") {
		t.Fatalf("expected -start_number 42 in args: %v", args)
	}
}

func TestBuildHLS_NoStartNumber_WhenZero(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath: "/in.mkv", Encoder: "copy", SessionDir: "/sess",
		SegmentPrefix: "seg", AudioCodec: "aac", AudioChannels: 2,
	})
	for _, a := range args {
		if a == "-start_number" {
			t.Fatalf("did not expect -start_number when StartNumber=0: %v", args)
		}
	}
}

func TestBuildHLS_PlaylistName(t *testing.T) {
	def := BuildHLS(BuildArgs{
		InputPath: "/in.mkv", Encoder: "copy", SessionDir: "/sess",
		SegmentPrefix: "seg", AudioCodec: "aac", AudioChannels: 2,
	})
	if got := lastArg(def); filepath.Base(got) != "index.m3u8" {
		t.Fatalf("default playlist should be index.m3u8, got %q", got)
	}
	cont := BuildHLS(BuildArgs{
		InputPath: "/in.mkv", Encoder: "copy", SessionDir: "/sess",
		SegmentPrefix: "seg", AudioCodec: "aac", AudioChannels: 2,
		PlaylistName: "cont.m3u8",
	})
	if got := lastArg(cont); filepath.Base(got) != "cont.m3u8" {
		t.Fatalf("PlaylistName override failed, got %q", got)
	}
}

func hasArgPair(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}

func lastArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[len(args)-1]
}

// TestContinuation_StitchRemainder_RealFFmpeg exercises the full mechanics
// against real ffmpeg: a premature-stop first run, then a continuation run
// into a side playlist with an explicit -start_number, then stitchPlaylist —
// and verifies the stitched playlist is well-formed and probes to the full
// source duration.
func TestContinuation_StitchRemainder_RealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-ffmpeg continuation test in -short mode")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")

	// 40s H.264+AAC source, keyframe every 2s.
	run(t, dir, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=40",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=40",
		"-c:v", "libx264", "-g", "20", "-keyint_min", "20", "-sc_threshold", "0",
		"-c:a", "aac", "-shortest", src)

	// First run: copy-remux, premature stop at 16s, event playlist.
	indexPath := filepath.Join(dir, "index.m3u8")
	run(t, dir, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-ss", "0", "-i", src, "-t", "16",
		"-c:v", "copy", "-map", "0:v:0", "-map", "0:a:0", "-c:a", "aac", "-ac", "2", "-b:a", "128k",
		"-f", "hls", "-hls_time", "4", "-hls_list_size", "0", "-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments+delete_segments",
		"-hls_segment_filename", filepath.Join(dir, "seg%05d.ts"),
		"-hls_playlist_type", "event", indexPath)

	pl, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index after run1: %v", err)
	}
	if !strings.Contains(string(pl), "#EXT-X-ENDLIST") {
		t.Fatalf("run1 should have ended with ENDLIST:\n%s", pl)
	}
	highest := HighestSegmentIndex(dir, ".ts")
	if highest < 0 {
		t.Fatalf("no segments produced by run1")
	}
	producedEnd := playlistDurationSec(pl)

	// Strip ENDLIST (what the worker does first) -> prefix.
	prefix := stitchPlaylist(pl, nil, false)
	if strings.Contains(string(prefix), "#EXT-X-ENDLIST") {
		t.Fatalf("prefix should have ENDLIST stripped:\n%s", prefix)
	}

	// Continuation run -> side playlist, explicit -start_number.
	contPath := filepath.Join(dir, "cont.m3u8")
	run(t, dir, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-ss", strconv.FormatFloat(producedEnd, 'f', 3, 64), "-i", src,
		"-c:v", "copy", "-map", "0:v:0", "-map", "0:a:0", "-c:a", "aac", "-ac", "2", "-b:a", "128k",
		"-avoid_negative_ts", "make_zero",
		"-f", "hls", "-hls_time", "4", "-hls_list_size", "0", "-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments+delete_segments",
		"-hls_segment_filename", filepath.Join(dir, "seg%05d.ts"),
		"-start_number", strconv.Itoa(highest+1),
		"-hls_playlist_type", "event", contPath)

	contPl, err := os.ReadFile(contPath)
	if err != nil {
		t.Fatalf("read cont.m3u8: %v", err)
	}
	// -start_number honored: first new segment is highest+1, no collision.
	wantSeg := "seg" + leftPad(highest+1) + ".ts"
	if !strings.Contains(string(contPl), wantSeg) {
		t.Fatalf("continuation should list %s (start_number honored):\n%s", wantSeg, contPl)
	}

	// Stitch and validate.
	merged := stitchPlaylist(prefix, extractSegmentBody(contPl), true)
	if err := os.WriteFile(indexPath, merged, 0o644); err != nil {
		t.Fatalf("write merged: %v", err)
	}
	m := string(merged)
	if strings.Count(m, "#EXT-X-DISCONTINUITY") != 1 {
		t.Fatalf("expected exactly 1 DISCONTINUITY:\n%s", m)
	}
	if !strings.Contains(m, "#EXT-X-MEDIA-SEQUENCE:0") {
		t.Fatalf("MEDIA-SEQUENCE must stay 0 (no client re-download):\n%s", m)
	}
	if strings.Count(m, "#EXT-X-ENDLIST") != 1 {
		t.Fatalf("expected exactly 1 ENDLIST:\n%s", m)
	}

	// The stitched playlist must probe to ~the full source duration.
	out := runOut(t, dir, "ffprobe", "-hide_banner", "-loglevel", "error",
		"-show_entries", "format=duration", "-of", "default=nw=1:nk=1", indexPath)
	dur, err := strconv.ParseFloat(strings.TrimSpace(out), 64)
	if err != nil {
		t.Fatalf("parse stitched duration %q: %v", out, err)
	}
	if dur < 38 || dur > 42 {
		t.Fatalf("stitched duration = %v, want ~40 (full source)", dur)
	}
}

func leftPad(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 5 {
		s = "0" + s
	}
	return s
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s failed: %v\n%s", name, err, out)
	}
}

func runOut(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s failed: %v", name, err)
	}
	return string(out)
}
