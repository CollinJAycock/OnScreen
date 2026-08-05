package transcode

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/testvalkey"
)

// stubProbeDuration overrides the ffprobe shell-out for the test's lifetime.
func stubProbeDuration(t *testing.T, dur float64) {
	t.Helper()
	old := probeSourceDuration
	probeSourceDuration = func(context.Context, string) float64 { return dur }
	t.Cleanup(func() { probeSourceDuration = old })
}

func writeEventPlaylist(t *testing.T, dir string, withEndlist bool) string {
	t.Helper()
	pl := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n" +
		"#EXTINF:4.000,\nseg00000.ts\n#EXTINF:4.000,\nseg00001.ts\n"
	if withEndlist {
		pl += "#EXT-X-ENDLIST\n"
	}
	path := filepath.Join(dir, "index.m3u8")
	if err := os.WriteFile(path, []byte(pl), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestContinueShortSession_CompleteSessionKeepsENDLIST pins the success path:
// a session whose playlist already covers the source must come out of
// continueShortSession TERMINATED. The success path used to `return` under a
// comment claiming "ffmpeg's ENDLIST stands" — but the entry strip had
// already removed it (and after any fold-in the merged playlist never had
// one), so every successfully auto-continued session polled as a live stream
// forever and the player never showed "ended".
func TestContinueShortSession_CompleteSessionKeepsENDLIST(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	w := &Worker{store: store, logger: slog.Default()}
	dir := t.TempDir()
	indexPath := writeEventPlaylist(t, dir, true)
	stubProbeDuration(t, 8.0) // playlist covers exactly 8s → complete

	job := TranscodeJob{SessionID: "cont-done-" + uuid.NewString()}
	if err := store.Create(context.Background(), Session{
		ID: job.SessionID, UserID: uuid.New(), MediaItemID: uuid.New(), FileID: uuid.New(),
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	w.continueShortSession(context.Background(), job, dir, ".ts", "/media/x.mkv", false,
		func(bool, float64, int, string) []string {
			t.Fatal("complete session must not re-run ffmpeg")
			return nil
		},
		func([]string) (error, bool) { t.Fatal("complete session must not re-run ffmpeg"); return nil, false },
	)

	pl, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pl), "#EXT-X-ENDLIST") {
		t.Fatal("complete session's playlist left UNTERMINATED — the player polls a " +
			"finished VOD forever and never shows 'ended'")
	}
}

// TestContinueShortSession_ProbeFailureRestoresENDLIST pins the probe-failure
// path under the new strip-first ordering: ENDLIST is stripped before the
// (slow) duration probe so a polling player doesn't latch VOD-ended during
// the window — and if the probe then fails, the strip must be undone.
func TestContinueShortSession_ProbeFailureRestoresENDLIST(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	w := &Worker{store: store, logger: slog.Default()}
	dir := t.TempDir()
	indexPath := writeEventPlaylist(t, dir, true)
	stubProbeDuration(t, 0) // probe failure

	w.continueShortSession(context.Background(), TranscodeJob{SessionID: "cont-probe-" + uuid.NewString()},
		dir, ".ts", "/media/x.mkv", false,
		func(bool, float64, int, string) []string { t.Fatal("no continuation on probe failure"); return nil },
		func([]string) (error, bool) { t.Fatal("no continuation on probe failure"); return nil, false },
	)

	pl, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pl), "#EXT-X-ENDLIST") {
		t.Fatal("probe-failure path left the stripped playlist unterminated")
	}
}
