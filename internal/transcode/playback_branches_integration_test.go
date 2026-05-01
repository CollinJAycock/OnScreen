package transcode

// Tier-1 path-matrix gaps from the v2.1 release test plan: WebVTT extract,
// burn-in, audio-stream selection, software encoder fallback (x264/x265/svtav1),
// and AV1-encode-from-non-AV1-source. Same shape as playback_integration_test.go
// — runs real FFmpeg against real fixtures, skips when fixtures are missing.
//
// Run with:
//
//	go test ./internal/transcode/... -run TestPlayback_Branch -v -timeout 180s

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures with the structural properties the new tests require.
const (
	// Forrest Gump (Blu-Ray remux) — H.264 video, 3 audio tracks (eac3 main +
	// 2 AC3 commentaries), 5+ subrip subtitle streams. Covers audio-stream
	// selection, WebVTT extraction, and any-source software-fallback runs.
	forrestGumpPath = `C:\movies\Forrest Gump (1994)\Forrest.Gump.1994.REPACK.1080p.UHD.BluRay.DD.7.1.x264-LoRD-WhiteRevtmp.mkv`

	// Goodfellas (4K UHD remux) — HEVC HDR10 + DTS + multiple PGS subtitle
	// streams. Used for burn-in (PGS bitmap → drawn over video).
	goodfellasPath = `C:\movies\GoodFellas (1990)\Goodfellas.1990.UHD.BluRay.2160p.DTS-HD.MA.5.1.HEVC.REMUX-FraMeSToR.mkv`
)

// runHLSAny runs FFmpeg with the given BuildArgs (limited to 8s of output)
// and asserts a playlist + at least one segment landed. Unlike runHLS in
// playback_integration_test.go, this helper is segment-extension-agnostic
// (HEVC software → .m4s, AV1 software → .m4s, others → .ts).
func runHLSAny(t *testing.T, a BuildArgs) string {
	t.Helper()
	sessDir := t.TempDir()
	a.SessionDir = sessDir
	a.SegmentPrefix = "seg"

	args := BuildHLS(a)
	insertIdx := -1
	for i, arg := range args {
		if arg == a.InputPath {
			insertIdx = i + 1
			break
		}
	}
	if insertIdx >= 0 {
		args = append(args[:insertIdx], append([]string{"-t", "8"}, args[insertIdx:]...)...)
	}

	cmd := exec.Command("ffmpeg", args...)
	// Anchor cwd to the session dir so the bare filename in
	// `-hls_fmp4_init_filename init.mp4` lands beside the segments.
	// Production worker.go does the same; without it the init.mp4
	// for HEVC/AV1 fMP4 sessions ends up in the test process's
	// working directory.
	cmd.Dir = sessDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("ffmpeg output:\n%s", out)
		t.Fatalf("ffmpeg failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sessDir, "index.m3u8")); err != nil {
		t.Fatalf("expected playlist index.m3u8 in %s: %v", sessDir, err)
	}
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		t.Fatalf("read sessdir: %v", err)
	}
	hasSeg := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "seg") && (strings.HasSuffix(e.Name(), ".ts") || strings.HasSuffix(e.Name(), ".m4s")) {
			hasSeg = true
			break
		}
	}
	if !hasSeg {
		t.Fatalf("no seg* segment files written in %s; entries=%v", sessDir, entries)
	}
	return sessDir
}

// hasEncoder checks ffmpeg's encoder list for the given name. Used to skip
// hardware-encoder tests on hosts where the encoder isn't built/registered.
func hasEncoder(name string) bool {
	out, err := exec.CommandContext(context.Background(), "ffmpeg", "-hide_banner", "-encoders").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		// Format: " V..... name              Description"
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == name {
			return true
		}
	}
	return false
}

// ── Audio-stream selection ───────────────────────────────────────────────────

// TestPlayback_Branch_AudioStreamSelection picks Forrest Gump's commentary
// track (audio_stream_index=2, "Commentary by Director Robert Zemeckis…")
// instead of the default eac3 main mix. Asserts the produced segment carries
// a stereo AAC stream — the actual track-2 vs track-1 distinction is covered
// at the argv level by TestBuildHLS_AudioStreamIndex; here we prove the full
// pipeline accepts a non-default index without error.
func TestPlayback_Branch_AudioStreamSelection(t *testing.T) {
	skipIfMissing(t, forrestGumpPath)

	sessDir := runHLSAny(t, BuildArgs{
		InputPath:        forrestGumpPath,
		Encoder:          EncoderSoftware,
		Width:            854,
		Height:           480,
		BitrateKbps:      1500,
		AudioCodec:       "aac",
		AudioChannels:    2,
		AudioStreamIndex: 2, // Commentary track 1
	})

	// Find the first .ts segment and confirm it has a stereo aac audio stream.
	seg := firstSegment(t, sessDir)
	probe := ffprobeAudio(t, seg)
	if !strings.Contains(probe, "codec_name=aac") {
		t.Errorf("expected aac audio, got: %s", probe)
	}
	if !strings.Contains(probe, "channels=2") {
		t.Errorf("expected 2 channels (downmix), got: %s", probe)
	}
}

// ── WebVTT extraction ────────────────────────────────────────────────────────

// TestPlayback_Branch_WebVTTExtraction asserts that requesting a subrip
// subtitle stream produces a sub0.vtt sidecar with a valid WEBVTT header.
// The conversion is what the player consumes for off-track subs.
func TestPlayback_Branch_WebVTTExtraction(t *testing.T) {
	skipIfMissing(t, forrestGumpPath)

	sessDir := runHLSAny(t, BuildArgs{
		InputPath:       forrestGumpPath,
		Encoder:         EncoderSoftware,
		Width:           854,
		Height:          480,
		BitrateKbps:     1500,
		AudioCodec:      "aac",
		AudioChannels:   2,
		SubtitleStreams: []int{0}, // English subrip
	})

	vttPath := filepath.Join(sessDir, "sub0.vtt")
	data, err := os.ReadFile(vttPath)
	if err != nil {
		t.Fatalf("expected sub0.vtt at %s: %v", vttPath, err)
	}
	if !strings.HasPrefix(string(data), "WEBVTT") {
		head := string(data)
		if len(head) > 32 {
			head = head[:32]
		}
		t.Errorf("sub0.vtt should start with 'WEBVTT' header; got: %q", head)
	}
}

// ── Subtitle burn-in ─────────────────────────────────────────────────────────

// TestPlayback_Branch_SubtitleBurnIn drives the BurnSubtitleStream filter
// path against a real text-subtitle source. The argv-level shape of the
// filter is validated by TestBuildHLS_BurnSubtitle; this test proves the
// filter chain composes with a real subtitle stream and produces a
// segment without ffmpeg errors. Frame-content verification (subs visibly
// drawn) is left to manual / browser-based testing — most movies have no
// subtitles in the first 8 seconds anyway.
//
// Source choice: ffmpeg's `subtitles` filter only supports text-based
// streams (subrip/ass/etc), not bitmap PGS — the filter aborts with
// "Only text based subtitles are currently supported". Goodfellas' PGS
// streams therefore can't drive this test; Forrest Gump has subrip.
// (Bitmap subtitle burn-in would need an `overlay` filter chain instead,
// which is a separate code path not yet implemented.)
func TestPlayback_Branch_SubtitleBurnIn(t *testing.T) {
	skipIfMissing(t, forrestGumpPath)

	si := 0 // First subrip stream (English)
	runHLSAny(t, BuildArgs{
		InputPath:          forrestGumpPath,
		Encoder:            EncoderSoftware,
		Width:              854,
		Height:             480,
		BitrateKbps:        1500,
		AudioCodec:         "aac",
		AudioChannels:      2,
		BurnSubtitleStream: &si,
	})
}

// ── Software encoder fallback ────────────────────────────────────────────────

// TestPlayback_Branch_Encoder_libx264 confirms the software H.264 path
// produces a valid h264 segment. This is the universal fallback when no
// hardware encoder is available.
func TestPlayback_Branch_Encoder_libx264(t *testing.T) {
	skipIfMissing(t, forrestGumpPath)
	sessDir := runHLSAny(t, BuildArgs{
		InputPath:     forrestGumpPath,
		Encoder:       EncoderSoftware,
		Width:         640,
		Height:        360,
		BitrateKbps:   600,
		AudioCodec:    "aac",
		AudioChannels: 2,
	})
	seg := firstSegment(t, sessDir)
	if codec := ffprobeVideoCodec(t, seg); codec != "h264" {
		t.Errorf("libx264 should emit h264, got %q", codec)
	}
}

// TestPlayback_Branch_Encoder_libx265 confirms the software HEVC path. HEVC
// segments must be fMP4 (.m4s) with an init.mp4 — the HLS muxer wraps them
// for browser MSE compatibility.
func TestPlayback_Branch_Encoder_libx265(t *testing.T) {
	skipIfMissing(t, forrestGumpPath)
	sessDir := runHLSAny(t, BuildArgs{
		InputPath:     forrestGumpPath,
		Encoder:       EncoderHEVCSoftware,
		Width:         640,
		Height:        360,
		BitrateKbps:   600,
		AudioCodec:    "aac",
		AudioChannels: 2,
	})
	if _, err := os.Stat(filepath.Join(sessDir, "init.mp4")); err != nil {
		t.Errorf("HEVC software output should produce init.mp4: %v", err)
	}
	seg := firstSegment(t, sessDir)
	if !strings.HasSuffix(seg, ".m4s") {
		t.Errorf("HEVC software segment should be .m4s, got %s", filepath.Base(seg))
	}
	if codec := ffprobeVideoCodec(t, seg); codec != "hevc" {
		t.Errorf("libx265 should emit hevc, got %q", codec)
	}
}

// TestPlayback_Branch_Encoder_libsvtav1 confirms the software AV1 path.
// SVT-AV1 preset 8 is the live-stream sweet spot per the maintainer
// guidance — slower presets are too expensive for live transcode.
func TestPlayback_Branch_Encoder_libsvtav1(t *testing.T) {
	if !hasEncoder("libsvtav1") {
		t.Skip("libsvtav1 not built into this ffmpeg")
	}
	skipIfMissing(t, forrestGumpPath)
	sessDir := runHLSAny(t, BuildArgs{
		InputPath:     forrestGumpPath,
		Encoder:       EncoderAV1Software,
		Width:         640,
		Height:        360,
		BitrateKbps:   600,
		AudioCodec:    "aac",
		AudioChannels: 2,
	})
	if _, err := os.Stat(filepath.Join(sessDir, "init.mp4")); err != nil {
		t.Errorf("AV1 software output should produce init.mp4: %v", err)
	}
	seg := firstSegment(t, sessDir)
	if codec := ffprobeVideoCodec(t, seg); codec != "av1" {
		t.Errorf("libsvtav1 should emit av1, got %q", codec)
	}
}

// ── AV1 encode from non-AV1 source (av1_nvenc) ───────────────────────────────

// TestPlayback_Branch_Encoder_av1_nvenc covers the AV1-encode-from-H.264
// path — av1_nvenc is on the dev box (RTX 5080) but never invoked by the
// live API matrix because the auto-prefer-AV1 logic only fires for AV1
// sources. Skipped on hosts without av1_nvenc (Ampere or older, AMD/Intel).
func TestPlayback_Branch_Encoder_av1_nvenc(t *testing.T) {
	if !hasEncoder("av1_nvenc") {
		t.Skip("av1_nvenc not registered (needs RTX 40-series)")
	}
	skipIfMissing(t, forrestGumpPath)
	sessDir := runHLSAny(t, BuildArgs{
		InputPath:     forrestGumpPath,
		Encoder:       EncoderAV1NVENC,
		Width:         1280,
		Height:        720,
		BitrateKbps:   3000,
		AudioCodec:    "aac",
		AudioChannels: 2,
	})
	seg := firstSegment(t, sessDir)
	if codec := ffprobeVideoCodec(t, seg); codec != "av1" {
		t.Errorf("av1_nvenc should emit av1, got %q", codec)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// firstSegment returns the path to the first segNNNNN.{ts,m4s} file in
// sessDir, walking the directory in lexicographic order.
func firstSegment(t *testing.T, sessDir string) string {
	t.Helper()
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		t.Fatalf("read sessdir: %v", err)
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "seg") && (strings.HasSuffix(n, ".ts") || strings.HasSuffix(n, ".m4s")) {
			return filepath.Join(sessDir, n)
		}
	}
	t.Fatalf("no segment found in %s", sessDir)
	return ""
}

// ffprobeVideoCodec returns the v:0 codec_name of a segment, or fails the test.
// For fMP4 segments the init.mp4 is required for full decode but ffprobe can
// extract the codec name from the segment alone via mov demuxer hints.
func ffprobeVideoCodec(t *testing.T, segPath string) string {
	t.Helper()
	probe := segPath
	// fMP4 segments need their init.mp4 prepended for ffprobe to see the
	// codec; concatenate them into a temp file.
	if strings.HasSuffix(segPath, ".m4s") {
		init := filepath.Join(filepath.Dir(segPath), "init.mp4")
		joined := filepath.Join(t.TempDir(), "joined.mp4")
		if err := concatFiles(joined, init, segPath); err != nil {
			t.Fatalf("concat init+seg: %v", err)
		}
		probe = joined
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-of", "default=nw=1",
		"-select_streams", "v:0", "-show_entries", "stream=codec_name", probe).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", probe, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "codec_name=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "codec_name="))
		}
	}
	return ""
}

// ffprobeAudio returns the joined a:0 stream metadata as a single string.
func ffprobeAudio(t *testing.T, segPath string) string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-of", "default=nw=1",
		"-select_streams", "a:0",
		"-show_entries", "stream=codec_name,channels,channel_layout,sample_rate",
		segPath).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", segPath, err)
	}
	return strings.ReplaceAll(string(out), "\n", " ")
}

func concatFiles(out string, parts ...string) error {
	dst, err := os.Create(out)
	if err != nil {
		return err
	}
	defer dst.Close()
	for _, p := range parts {
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = dst.ReadFrom(src)
		src.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
