package v1

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/transcode"
)

func TestBuildPredictedVariantPlaylist(t *testing.T) {
	// 10s source @ 4s segments (fps unknown → flat grid) → segs 0,1,2 (last = 2s).
	pl := buildPredictedVariantPlaylist(10_000, 0, "sid123", "720p", "tok", false, "")

	if !strings.HasPrefix(pl, "#EXTM3U\n") {
		t.Fatalf("must start with #EXTM3U:\n%s", pl)
	}
	for _, tag := range []string{"#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-TARGETDURATION:4", "#EXT-X-ENDLIST"} {
		if !strings.Contains(pl, tag) {
			t.Errorf("missing %s:\n%s", tag, pl)
		}
	}
	if n := strings.Count(pl, "#EXTINF:"); n != 3 {
		t.Errorf("got %d segments, want 3:\n%s", n, pl)
	}
	// Global-index segment URLs carry the rung + token.
	for _, want := range []string{
		"/api/v1/transcode/sessions/sid123/abr/720p/seg/0.ts?token=tok",
		"/api/v1/transcode/sessions/sid123/abr/720p/seg/2.ts?token=tok",
	} {
		if !strings.Contains(pl, want) {
			t.Errorf("missing segment URL %q:\n%s", want, pl)
		}
	}
	// Final partial segment is 2.000s, not a full 4.
	if !strings.Contains(pl, "#EXTINF:2.000,") {
		t.Errorf("expected a 2.000s tail segment:\n%s", pl)
	}
}

func TestBuildPredictedVariantPlaylist_ExactMultiple(t *testing.T) {
	// 8s → exactly 2 full segments, no partial tail.
	pl := buildPredictedVariantPlaylist(8_000, 0, "s", "480p", "t", false, "")
	if n := strings.Count(pl, "#EXTINF:"); n != 2 {
		t.Errorf("got %d segments, want 2:\n%s", n, pl)
	}
	if strings.Contains(pl, "#EXTINF:2.000,") {
		t.Errorf("exact multiple should have no partial tail:\n%s", pl)
	}
}

func TestBuildPredictedVariantPlaylist_FrameQuantized(t *testing.T) {
	// 23.976 fps: ffmpeg cuts at the first frame >= n*4s, so boundaries are
	// 0, 96/fps=4.004, 192/fps=8.008. A 10s source yields segs at those
	// boundaries with a 1.992s tail — matching the encoder, not a flat grid.
	const fps = 24000.0 / 1001.0
	pl := buildPredictedVariantPlaylist(10_000, fps, "sid", "720p", "tok", false, "")

	if n := strings.Count(pl, "#EXTINF:"); n != 3 {
		t.Fatalf("got %d segments, want 3:\n%s", n, pl)
	}
	// Full segments are 4.004s (96 frames), not the nominal 4.000.
	if c := strings.Count(pl, "#EXTINF:4.004,"); c != 2 {
		t.Errorf("want two 4.004s segments, got %d:\n%s", c, pl)
	}
	// Tail = 10 - 8.008 = 1.992s.
	if !strings.Contains(pl, "#EXTINF:1.992,") {
		t.Errorf("expected a 1.992s frame-quantized tail:\n%s", pl)
	}
}

func TestHighestLocalSegOnDisk(t *testing.T) {
	dir := t.TempDir()
	if got := highestLocalSegOnDisk(dir, ".ts"); got != -1 {
		t.Errorf("empty dir: got %d want -1", got)
	}
	for _, n := range []string{"seg00000.ts", "seg00001.ts", "seg00007.ts", "index.m3u8", "seg0000x.ts", "init.mp4", "seg00009.m4s"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// .ts head ignores the .m4s and init files.
	if got := highestLocalSegOnDisk(dir, ".ts"); got != 7 {
		t.Errorf(".ts: got %d want 7", got)
	}
	// .m4s head sees only the fMP4 segment, not init.mp4 or the .ts files.
	if got := highestLocalSegOnDisk(dir, ".m4s"); got != 9 {
		t.Errorf(".m4s: got %d want 9", got)
	}
	if got := highestLocalSegOnDisk(filepath.Join(dir, "nope"), ".ts"); got != -1 {
		t.Errorf("missing dir: got %d want -1", got)
	}
}

func TestABRReachableSoon(t *testing.T) {
	// head = 40: children keep every segment (hls_list_size 0), so anything at
	// or below the head is present and reachable; only a forward seek past
	// head+lookahead restarts.
	const head = 40
	cases := []struct {
		name     string
		localSeg int
		want     bool
	}{
		{"well below head (present)", 5, true},
		{"head", 40, true},
		{"next sequential", 41, true},
		{"within lookahead", 46, true},             // 40 + abrSeekLookahead
		{"forward seek past lookahead", 47, false}, // 40 + 7
		{"negative", -1, false},
	}
	for _, c := range cases {
		if got := abrReachableSoon(head, c.localSeg); got != c.want {
			t.Errorf("%s: abrReachableSoon(%d, %d)=%v want %v", c.name, head, c.localSeg, got, c.want)
		}
	}

	// No segments yet (head -1): seg 0..lookahead are imminent, beyond restarts.
	if !abrReachableSoon(-1, 0) {
		t.Error("head=-1 localSeg=0 should be reachable (child spinning up)")
	}
	if abrReachableSoon(-1, abrSeekLookahead+1) {
		t.Error("head=-1 far-ahead seek should not be reachable")
	}
}

func TestABRSegmentBoundary_FrameQuantizedMonotonic(t *testing.T) {
	// Boundaries must be the same value the encoder forces and strictly
	// increasing, so global→local segment mapping stays aligned.
	const fps = 24000.0 / 1001.0
	prev := -1.0
	for i := 0; i < 5; i++ {
		got := abrSegmentBoundarySec(i, fps)
		want := math.Ceil(float64(i)*4*fps) / fps
		if got != want {
			t.Errorf("boundary(%d)=%v want %v", i, got, want)
		}
		if got <= prev && i > 0 {
			t.Errorf("boundary(%d)=%v not > previous %v", i, got, prev)
		}
		prev = got
	}
	// fps unknown → nominal flat grid.
	if got := abrSegmentBoundarySec(3, 0); got != 12 {
		t.Errorf("flat-grid boundary(3)=%v want 12", got)
	}
}

func TestABRMasterURLsUseRungLabels(t *testing.T) {
	// serveABRMaster uses BuildMasterPlaylist with label-keyed variant
	// URLs — verify the wiring shape via the same generator.
	ladder := transcode.BuildLadder(1920, 1080, 0, "h264", 0)
	master := transcode.BuildMasterPlaylist(ladder, "", func(rd transcode.Rendition) string {
		return "/api/v1/transcode/sessions/SID/abr/" + rd.Label + "/index.m3u8?token=TOK"
	})
	if !strings.Contains(master, "/abr/1080p/index.m3u8?token=TOK") {
		t.Errorf("master missing label-keyed variant URL:\n%s", master)
	}
}

func TestBuildPredictedVariantPlaylist_FMP4(t *testing.T) {
	// HEVC fMP4 ladder: the playlist references an init segment via EXT-X-MAP
	// and uses .m4s media segments, not .ts.
	pl := buildPredictedVariantPlaylist(10_000, 0, "sid", "1080p", "tok", true, "")

	if !strings.Contains(pl, `#EXT-X-MAP:URI="/api/v1/transcode/sessions/sid/abr/1080p/seg/init.mp4?token=tok"`) {
		t.Errorf("fMP4 playlist missing EXT-X-MAP init segment:\n%s", pl)
	}
	if !strings.Contains(pl, "/abr/1080p/seg/0.m4s?token=tok") {
		t.Errorf("fMP4 playlist should use .m4s segments:\n%s", pl)
	}
	if strings.Contains(pl, ".ts?token=") {
		t.Errorf("fMP4 playlist must not reference .ts segments:\n%s", pl)
	}
}
