package transcode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/ffsafe"
)

func TestForStack(t *testing.T) {
	h264 := media.File{VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac")}
	vp9 := media.File{VideoCodec: strPtr("vp9"), AudioCodec: strPtr("opus")}
	cases := []struct {
		name string
		in   Decision
		file media.File
		want Decision
	}{
		{"direct play of h264 remuxes", DecisionDirectPlay, h264, DecisionDirectStream},
		{"direct play of vp9 transcodes", DecisionDirectPlay, vp9, DecisionTranscode},
		{"direct stream stands", DecisionDirectStream, h264, DecisionDirectStream},
		{"transcode stands", DecisionTranscode, h264, DecisionTranscode},
		{"damaged stands", DecisionDamaged, h264, DecisionDamaged},
		{"unsupported stands", DecisionUnsupported, h264, DecisionUnsupported},
	}
	for _, c := range cases {
		if got := ForStack(c.in, c.file); got != c.want {
			t.Errorf("%s: ForStack = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestPartsDurationSec(t *testing.T) {
	if got := PartsDurationSec([]SourcePart{{DurationMS: 1500}, {DurationMS: 2500}}); got != 4 {
		t.Fatalf("PartsDurationSec = %v, want 4", got)
	}
	if got := PartsDurationSec([]SourcePart{{DurationMS: 1500}, {}}); got != 0 {
		t.Fatalf("unknown part length: PartsDurationSec = %v, want 0", got)
	}
}

func TestResolvePartInputs(t *testing.T) {
	parts := []SourcePart{
		{FilePath: "/m/a cd1.mkv", SourceURL: "http://s/media/stream/a?token=1", DurationMS: 1000},
		{FilePath: "/m/a cd2.mkv", SourceURL: "http://s/media/stream/b?token=2", DurationMS: 2000},
	}
	t.Run("all local", func(t *testing.T) {
		entries, remote, err := resolvePartInputs(parts, func(string) bool { return true })
		if err != nil || remote || entries[0].Input != "/m/a cd1.mkv" || entries[1].DurationSec != 2 {
			t.Fatalf("entries=%v remote=%v err=%v", entries, remote, err)
		}
	})
	t.Run("one part missing locally reads every part over http", func(t *testing.T) {
		entries, remote, err := resolvePartInputs(parts, func(p string) bool { return p == "/m/a cd1.mkv" })
		if err != nil || !remote || entries[0].Input != parts[0].SourceURL || entries[1].Input != parts[1].SourceURL {
			t.Fatalf("entries=%v remote=%v err=%v", entries, remote, err)
		}
	})
	t.Run("unreachable without urls", func(t *testing.T) {
		noURL := []SourcePart{{FilePath: "/x"}, {FilePath: "/y", SourceURL: "http://s/y"}}
		if _, _, err := resolvePartInputs(noURL, func(string) bool { return false }); err == nil {
			t.Fatal("want an error when a part can be read neither way")
		}
	})
}

// TestBuildHLS_ConcatInput: a stacked movie's list is opened with the forced
// concat demuxer and its allowlist, immediately before -i, and the seek stays
// an input option ahead of them.
func TestBuildHLS_ConcatInput(t *testing.T) {
	parts := []ffsafe.ConcatPart{{Input: "/m/a cd1.mkv", DurationSec: 10}, {Input: "/m/a cd2.mkv", DurationSec: 10}}
	args := BuildHLS(BuildArgs{
		InputPath: "/tmp/onscreen/concat/s.ffconcat", ConcatParts: parts,
		StartOffset: 12, Encoder: "copy", SessionDir: "/tmp/s", SegmentPrefix: "seg",
		AudioCodec: "aac", AudioStreamIndex: -1,
	})
	joined := strings.Join(args, " ")
	want := "-f concat -safe 0 -protocol_whitelist " + ffsafe.Whitelist("/m/a cd1.mkv") + " -i /tmp/onscreen/concat/s.ffconcat"
	if !strings.Contains(joined, want) {
		t.Fatalf("args lack %q:\n%s", want, joined)
	}
	if !strings.Contains(joined, "-ss 12.000 -noaccurate_seek") || strings.Index(joined, "-ss") > strings.Index(joined, "-i ") {
		t.Fatalf("seek must stay an input option before -i:\n%s", joined)
	}
	single := strings.Join(BuildHLS(BuildArgs{InputPath: "/m/a.mkv", Encoder: "copy", SessionDir: "/tmp/s", AudioCodec: "aac", AudioStreamIndex: -1}), " ")
	if strings.Contains(single, "concat") || strings.Contains(single, "-safe") {
		t.Fatalf("single-file input must not use concat:\n%s", single)
	}
	ds := strings.Join(BuildDirectStream("/tmp/l.ffconcat", "/tmp/s", 0, parts...), " ")
	if !strings.Contains(ds, "-f concat -safe 0") {
		t.Fatalf("direct stream of a stack lacks the concat input:\n%s", ds)
	}
}

// TestStackedRemux_RealFFmpeg remuxes two generated parts as one stream the
// way the worker does — list written by writeConcatList, args from BuildHLS
// — and checks the whole movie comes out, and that a copy seek into part 2
// starts on the keyframe FindPreviousKeyframeInParts predicts.
func TestStackedRemux_RealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ffmpeg")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	dir := t.TempDir()
	var src []SourcePart
	for i, d := range []string{"10", "8"} {
		p := filepath.Join(dir, "Movie (2001) cd"+string(rune('1'+i))+".mkv")
		out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc=size=320x180:rate=24:duration="+d,
			"-f", "lavfi", "-i", "sine=frequency=440:duration="+d,
			"-c:v", "libx264", "-g", "48", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", p).CombinedOutput()
		if err != nil {
			t.Fatalf("make part: %v: %s", err, out)
		}
		ms := int64(10000)
		if i == 1 {
			ms = 8000
		}
		src = append(src, SourcePart{FilePath: p, DurationMS: ms})
	}
	prev := concatListDir
	concatListDir = filepath.Join(dir, "concat")
	t.Cleanup(func() { concatListDir = prev })

	entries, _, err := resolvePartInputs(src, func(p string) bool { _, e := os.Stat(p); return e == nil })
	if err != nil {
		t.Fatal(err)
	}
	list, err := writeConcatList("sess", entries)
	if err != nil {
		t.Fatal(err)
	}

	run := func(name string, start float64) float64 {
		return runConcat(t, dir, name, list, entries, start)
	}

	if got := run("full", 0); got < 17 || got > 19 {
		t.Fatalf("full stream = %.2fs, want ~18 (both parts)", got)
	}
	kf := FindPreviousKeyframeInParts(context.Background(), src, 13)
	if kf != 12 {
		t.Fatalf("keyframe before 13s = %.3f, want 12 (part 2's 2s keyframe)", kf)
	}
	if got := run("seek", 13); got < 5 || got > 7 {
		t.Fatalf("stream from 13s = %.2fs, want ~6 (12s keyframe to the end)", got)
	}

	// A storage-less fleet worker reads every part over HTTP, one URL per
	// part (the real route is /media/stream/{file_id}?token=...), seeking
	// with Range requests.
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()
	for i := range src {
		src[i].SourceURL = srv.URL + "/" + url.PathEscape(filepath.Base(src[i].FilePath))
	}
	urlEntries, remote, err := resolvePartInputs(src, func(string) bool { return false })
	if err != nil || !remote {
		t.Fatalf("remote=%v err=%v", remote, err)
	}
	urlList, err := writeConcatList("sess-http", urlEntries)
	if err != nil {
		t.Fatal(err)
	}
	if got := runConcat(t, dir, "http-seek", urlList, urlEntries, 13); got < 5 || got > 7 {
		t.Fatalf("http stream from 13s = %.2fs, want ~6", got)
	}
}

func runConcat(t *testing.T, dir, name, list string, entries []ffsafe.ConcatPart, start float64) float64 {
	t.Helper()
	sessDir := filepath.Join(dir, name)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	args := BuildHLS(BuildArgs{
		InputPath: list, ConcatParts: entries, StartOffset: start,
		Encoder: "copy", AudioCodec: "aac", AudioStreamIndex: -1,
		SessionDir: sessDir, SegmentPrefix: "seg",
	})
	cmd := exec.Command("ffmpeg", args...)
	cmd.Dir = sessDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s\nargs: %v", err, out, args)
	}
	pl, err := os.ReadFile(filepath.Join(sessDir, "index.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	return playlistDurationSec(pl)
}
