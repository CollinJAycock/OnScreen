package scanner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The 2026-11 scan's bypass: ffmpeg's DASH probe finds <MPD anywhere in its
// probe buffer, and the old sniff only looked at the first 512 bytes.
func TestVerifySource_RefusesDASHBehindAComment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movie.mkv")
	body := "<!--" + strings.Repeat("x", 700) + "-->\n" +
		`<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" profiles="urn:mpeg:dash:profile:isoff-on-demand:2011">` +
		`<Period><AdaptationSet><Representation id="1"><BaseURL>/media/secret.mkv</BaseURL></Representation></AdaptationSet></Period></MPD>`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !LooksLikeReferenceContainer(path) {
		t.Error("LooksLikeReferenceContainer missed a DASH manifest behind a 700-byte comment")
	}
	status, err := VerifySource(context.Background(), path)
	if status != SourceUnreadable || !errors.Is(err, ErrUnsafeContainer) {
		t.Errorf("VerifySource = (%q, %v), want (%q, ErrUnsafeContainer)", status, err, SourceUnreadable)
	}
	if _, err := ProbeFile(context.Background(), path); !errors.Is(err, ErrUnsafeContainer) {
		t.Errorf("ProbeFile err = %v, want ErrUnsafeContainer", err)
	}
}

// VerifySource now also trusts ffprobe's own demuxer verdict, as ProbeFile
// does, for anything the byte sniff can't see.
func TestVerifyVerdict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		out    string
		status SourceStatus
		unsafe bool
	}{
		{"matroska", `{"streams":[{"index":0},{"index":1}],"format":{"format_name":"matroska,webm"}}`, SourceOK, false},
		{"dash", `{"streams":[{"index":0}],"format":{"format_name":"dash"}}`, SourceUnreadable, true},
		{"hls", `{"streams":[{"index":0}],"format":{"format_name":"hls"}}`, SourceUnreadable, true},
		{"imf", `{"streams":[{"index":0}],"format":{"format_name":"imf"}}`, SourceUnreadable, true},
		{"no streams", `{"streams":[],"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2"}}`, SourceUnreadable, false},
		{"not json", "0\n1\n", SourceUnreadable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, err := verifyVerdict([]byte(tc.out))
			if status != tc.status {
				t.Errorf("status = %q, want %q (err %v)", status, tc.status, err)
			}
			if got := errors.Is(err, ErrUnsafeContainer); got != tc.unsafe {
				t.Errorf("ErrUnsafeContainer = %v, want %v (err %v)", got, tc.unsafe, err)
			}
		})
	}
}

// TestHelperProbeFlood is not a test: runFFprobe's test re-runs the test
// binary as a stand-in ffprobe that prints far more than the cap.
func TestHelperProbeFlood(t *testing.T) {
	if os.Getenv("ONSCREEN_PROBE_FLOOD") != "1" {
		t.Skip("helper process")
	}
	chunk := []byte(strings.Repeat("x", 1<<20))
	for i := 0; i < 2*maxProbeOutputBytes>>20; i++ {
		if _, err := os.Stdout.Write(chunk); err != nil {
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func TestRunFFprobe_CapsOutput(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProbeFlood$")
	cmd.Env = append(os.Environ(), "ONSCREEN_PROBE_FLOOD=1")
	out, err := runFFprobe(cmd)
	if err == nil || out != nil {
		t.Fatalf("runFFprobe = (%d bytes, %v), want an error past %d bytes", len(out), err, maxProbeOutputBytes)
	}
}
