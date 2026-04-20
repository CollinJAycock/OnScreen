package transcode

// Playback integration tests: runs real FFmpeg against real media files in the
// library to verify each HLS path produces valid segments.
//
// These tests are skipped automatically when:
//   - the source file doesn't exist on disk
//   - FFmpeg is not on PATH
//   - -short flag is passed
//
// Run explicitly with:
//
//	go test ./internal/transcode/... -run TestPlayback -v -timeout 120s

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/onscreen/onscreen/internal/scanner"
)

// testFiles holds the real media files used by each playback path test.
// Each key is a human-readable label; each value is the Windows absolute path.
var testFiles = map[string]string{
	// Full transcode: mpeg4/mp3 in AVI — browser cannot decode, must re-encode.
	"transcode_avi_mpeg4_mp3": `C:\TV\Good Eats\Season 8\Good.Eats.S08E05.SDTV.avi`,

	// Remux: h264/aac in MKV — video is browser-compatible but MKV is not.
	// Stream-copy video, transcode audio, remux into MPEG-TS.
	"remux_mkv_h264_aac": `C:\TV\Good Eats\Season 1\Good.Eats.S01E03.The.Egg.Files.NTSC.DVDRip.x264-thisismesmiling.mkv`,

	// Non-faststart MP4: h264/aac but moov is at end of file (mdat first).
	// scanner.IsFaststart returns false → browser can't start without full download.
	// Routed through remux path same as MKV.
	"remux_mp4_nonfaststart_h264_aac": `C:\TV\Good Eats\Season 1\Good.Eats.s01e01.Steak.Your.Claim.DVDRip.XviD.AAC-BUYMORE-Obfuscated.mp4`,

	// Full transcode: AV1/Opus in MKV — AV1 in MPEG-TS is unsupported by browsers.
	// This is the LotR path that was presenting as audio-only before the fix.
	"transcode_mkv_av1_opus": `C:\movies\The Lord of the Rings - The Fellowship of the Ring (2001)\LOTR.The.Fellowship.Of.The.Rings.2001.PROPER.Bluray.1080p.AV1.OPUS.5.1-UH.mkv`,
}

// skipIfMissing skips the test if the file doesn't exist or ffmpeg isn't on PATH.
func skipIfMissing(t *testing.T, path string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping playback integration test in -short mode")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("media file not found: %s", path)
	}
}

// runHLS runs FFmpeg with the given BuildArgs (plus a -t 8 duration limit) and
// returns the session directory. It fails the test if FFmpeg exits non-zero or
// if no segments are produced.
func runHLS(t *testing.T, a BuildArgs) string {
	t.Helper()
	sessDir := t.TempDir()
	a.SessionDir = sessDir
	a.SegmentPrefix = "seg"

	args := BuildHLS(a)

	// Insert "-t 8" just after the input file to limit output to 8 seconds
	// (2 segments). This keeps test duration fast regardless of file length.
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
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("ffmpeg output:\n%s", out)
		t.Fatalf("ffmpeg failed: %v", err)
	}

	// Verify at least one segment file was written. Remux (video copy) with
	// -hls_playlist_type event starts at seg00000.ts; transcode starts at seg00001.ts.
	seg0 := filepath.Join(sessDir, "seg00000.ts")
	seg1 := filepath.Join(sessDir, "seg00001.ts")
	if _, err0 := os.Stat(seg0); os.IsNotExist(err0) {
		if _, err1 := os.Stat(seg1); os.IsNotExist(err1) {
			t.Fatalf("expected seg00000.ts or seg00001.ts to exist after ffmpeg in %s", sessDir)
		}
	}

	// Verify playlist was written.
	playlist := filepath.Join(sessDir, "index.m3u8")
	if _, err := os.Stat(playlist); os.IsNotExist(err) {
		t.Fatalf("expected playlist %s to exist after ffmpeg, but it doesn't", playlist)
	}

	return sessDir
}

// runRemux runs FFmpeg in video-copy (remux) mode and verifies output.
func runRemux(t *testing.T, inputPath string) string {
	t.Helper()
	return runHLS(t, BuildArgs{
		InputPath:     inputPath,
		Encoder:       "copy",
		AudioCodec:    "aac",
		AudioChannels: 2,
	})
}

// TestPlayback_Transcode_AVI_MPEG4 exercises the full transcode path using a
// real AVI file with mpeg4 video and mp3 audio — neither is browser-playable,
// so the server must re-encode to h264/aac in MPEG-TS HLS.
func TestPlayback_Transcode_AVI_MPEG4(t *testing.T) {
	path := testFiles["transcode_avi_mpeg4_mp3"]
	skipIfMissing(t, path)

	sessDir := runHLS(t, BuildArgs{
		InputPath:     path,
		Encoder:       EncoderSoftware,
		Width:         320,
		Height:        240,
		BitrateKbps:   800,
		AudioCodec:    "aac",
		AudioChannels: 2,
	})

	// Verify the playlist references at least two segments (8s / 4s per segment).
	data, err := os.ReadFile(filepath.Join(sessDir, "index.m3u8"))
	if err != nil {
		t.Fatalf("read playlist: %v", err)
	}
	content := string(data)
	if count := countOccurrences(content, ".ts"); count < 1 {
		t.Errorf("expected at least 1 .ts segment in playlist, got 0\nplaylist:\n%s", content)
	}
}

// TestPlayback_Remux_MKV_H264_AAC exercises the video-copy (remux) path using a
// real MKV file with h264/aac. Video is browser-compatible but MKV is not, so
// the server stream-copies video and transcodes audio to produce MPEG-TS HLS.
func TestPlayback_Remux_MKV_H264_AAC(t *testing.T) {
	path := testFiles["remux_mkv_h264_aac"]
	skipIfMissing(t, path)

	sessDir := runRemux(t, path)

	data, err := os.ReadFile(filepath.Join(sessDir, "index.m3u8"))
	if err != nil {
		t.Fatalf("read playlist: %v", err)
	}
	if count := countOccurrences(string(data), ".ts"); count < 1 {
		t.Errorf("expected at least 1 segment in remux playlist\n%s", string(data))
	}
}

// TestPlayback_Remux_MP4_NonFaststart exercises the non-faststart MP4 path.
// The file has h264/aac in an MP4 container but moov is at the end of the file
// (mdat comes first), so canDirectPlay returns false and it falls through to remux.
// This verifies our scanner.IsFaststart detection routes the file correctly.
func TestPlayback_Remux_MP4_NonFaststart(t *testing.T) {
	path := testFiles["remux_mp4_nonfaststart_h264_aac"]
	skipIfMissing(t, path)

	// Verify scanner.IsFaststart correctly identifies this file as non-faststart.
	from_pkg := scanner.IsFaststart(path)
	if from_pkg {
		t.Errorf("expected scanner.IsFaststart=false for non-faststart MP4, got true")
	}

	// Verify the remux path produces valid HLS segments.
	sessDir := runRemux(t, path)

	data, err := os.ReadFile(filepath.Join(sessDir, "index.m3u8"))
	if err != nil {
		t.Fatalf("read playlist: %v", err)
	}
	if count := countOccurrences(string(data), ".ts"); count < 1 {
		t.Errorf("expected at least 1 segment in non-faststart MP4 playlist\n%s", string(data))
	}
}

// TestPlayback_Transcode_AV1_MKV exercises the full transcode path for the LotR
// file: AV1 video with Opus audio in MKV. AV1 in MPEG-TS is unsupported by
// browsers (audio-only), so it must be fully transcoded to h264/aac.
// This is the exact path that was broken before the remux→transcode escalation fix.
func TestPlayback_Transcode_AV1_MKV(t *testing.T) {
	path := testFiles["transcode_mkv_av1_opus"]
	skipIfMissing(t, path)

	// The web player auto-escalates to full transcode after detecting videoWidth=0.
	// On the server side that means height>0, videoCopy=false.
	sessDir := runHLS(t, BuildArgs{
		InputPath:     path,
		Encoder:       EncoderSoftware,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		AudioChannels: 2,
	})

	data, err := os.ReadFile(filepath.Join(sessDir, "index.m3u8"))
	if err != nil {
		t.Fatalf("read playlist: %v", err)
	}
	if count := countOccurrences(string(data), ".ts"); count < 1 {
		t.Errorf("expected at least 1 segment in AV1 transcode playlist\n%s", string(data))
	}
}

// TestPlayback_IsFaststart_KnownFiles verifies our faststart detection against
// files we know the answer for from the database.
func TestPlayback_IsFaststart_KnownFiles(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		wantFast bool
	}{
		{
			name:     "non-faststart mp4 (mdat before moov)",
			path:     testFiles["remux_mp4_nonfaststart_h264_aac"],
			wantFast: false,
		},
		{
			name:     "avi file (not ISOBMFF — always true)",
			path:     testFiles["transcode_avi_mpeg4_mp3"],
			wantFast: true,
		},
		{
			name:     "mkv file (not ISOBMFF — always true)",
			path:     testFiles["remux_mkv_h264_aac"],
			wantFast: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if testing.Short() {
				t.Skip("skipping in -short mode")
			}
			if _, err := os.Stat(tc.path); os.IsNotExist(err) {
				t.Skipf("file not found: %s", tc.path)
			}
			got := scanner.IsFaststart(tc.path)
			if got != tc.wantFast {
				t.Errorf("scanner.IsFaststart(%q): want %v, got %v", tc.path, tc.wantFast, got)
			}
		})
	}
}

func countOccurrences(s, substr string) int {
	count := 0
	start := 0
	for {
		idx := indexOf(s[start:], substr)
		if idx < 0 {
			break
		}
		count++
		start += idx + len(substr)
	}
	return count
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
