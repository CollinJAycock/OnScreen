package v1

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/scanner"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

// swappedFile returns a media row that was indexed as real Matroska — full
// codec + resolution metadata, so lazyReprobe's "metadata complete" early
// return applies — whose on-disk bytes have since been replaced by content.
func swappedFile(t *testing.T, content string) media.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	w, h := 1920, 1080
	return media.File{
		ID:          uuid.New(),
		FilePath:    path,
		Container:   strPtr("matroska,webm"),
		VideoCodec:  strPtr("h264"),
		AudioCodec:  strPtr("aac"),
		ResolutionW: &w,
		ResolutionH: &h,
	}
}

var playlistPayloads = map[string]string{
	"hls":      "#EXTM3U\n#EXTINF:10,\nfile:///etc/passwd\n",
	"ffconcat": "ffconcat version 1.0\nfile '/etc/passwd'\n",
}

// A file indexed as Matroska and later replaced at the same path by a playlist
// kept its old row, and lazyReprobe returned before probing because the row's
// metadata was complete — so Decide handed out a verdict and Start transcoded
// the playlist, following the references inside it.
func TestDecision_RefusesFileSwappedForPlaylist(t *testing.T) {
	for name, payload := range playlistPayloads {
		t.Run(name, func(t *testing.T) {
			h := newDecisionHandler(t, swappedFile(t, payload))
			rec := httptest.NewRecorder()
			h.Decision(rec, decisionRequest(t, "videoDecoder=h264,audioDecoder=aac,protocols=mkv:mp4", true))
			if rec.Code != http.StatusUnsupportedMediaType || !bodyHas(rec, "UNSUPPORTED_CONTAINER") {
				t.Errorf("got %d %s, want 415 UNSUPPORTED_CONTAINER", rec.Code, rec.Body.String())
			}
		})
	}
}

// startSwapped runs Start for file with verify as the pre-flight check.
func startSwapped(t *testing.T, file media.File,
	verify func(context.Context, string) (scanner.SourceStatus, error)) *httptest.ResponseRecorder {
	t.Helper()
	v := testvalkey.New(t)
	h := NewNativeTranscodeHandler(transcode.NewSessionStore(v), transcode.NewSegmentTokenManager(v),
		&mockTranscodeMedia{
			item:  &media.Item{ID: uuid.New(), Type: "movie", Title: "Test"},
			files: []media.File{file},
		}, &config.Config{TranscodeMaxHeight: 2160}, slog.Default()).
		WithVerifySource(verify)

	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode",
		bytes.NewReader([]byte(`{"height":720}`)))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)
	rec := httptest.NewRecorder()
	h.Start(rec, req)
	return rec
}

// verifyMustNotRun fails the test if Start reaches the ffprobe pre-flight.
func verifyMustNotRun(t *testing.T) func(context.Context, string) (scanner.SourceStatus, error) {
	return func(context.Context, string) (scanner.SourceStatus, error) {
		t.Error("VerifySource (ffprobe) ran on a playlist/reference container")
		return scanner.SourceOK, nil
	}
}

// Start used to run VerifySource — an ffprobe, whose HLS demuxer follows the
// references in the playlist — BEFORE the byte sniff, and answered 422
// SOURCE_UNREADABLE instead of 415. The sniff must come first.
func TestStart_RefusesFileSwappedForPlaylist(t *testing.T) {
	for name, payload := range playlistPayloads {
		t.Run(name, func(t *testing.T) {
			rec := startSwapped(t, swappedFile(t, payload), verifyMustNotRun(t))
			if rec.Code != http.StatusUnsupportedMediaType || !bodyHas(rec, "UNSUPPORTED_CONTAINER") {
				t.Errorf("got %d %s, want 415 UNSUPPORTED_CONTAINER", rec.Code, rec.Body.String())
			}
		})
	}
}

// A row whose stored container is itself a reference demuxer is refused on the
// stored name alone, also before VerifySource.
func TestStart_RefusesStoredReferenceContainerBeforeProbe(t *testing.T) {
	f := swappedFile(t, "\x1a\x45\xdf\xa3binary")
	f.Container = strPtr("hls")
	rec := startSwapped(t, f, verifyMustNotRun(t))
	if rec.Code != http.StatusUnsupportedMediaType || !bodyHas(rec, "UNSUPPORTED_CONTAINER") {
		t.Errorf("got %d %s, want 415 UNSUPPORTED_CONTAINER", rec.Code, rec.Body.String())
	}
}

// Swapped between the handler's sniff and VerifySource: VerifySource's own
// sniff refuses it, and Start still reports 415, not 422.
func TestStart_VerifySourceUnsafeContainerIs415(t *testing.T) {
	rec := startSwapped(t, swappedFile(t, "\x1a\x45\xdf\xa3binary"),
		func(context.Context, string) (scanner.SourceStatus, error) {
			return scanner.SourceUnreadable, scanner.ErrUnsafeContainer
		})
	if rec.Code != http.StatusUnsupportedMediaType || !bodyHas(rec, "UNSUPPORTED_CONTAINER") {
		t.Errorf("got %d %s, want 415 UNSUPPORTED_CONTAINER", rec.Code, rec.Body.String())
	}
}

// Control: real (binary) media at the path still gets a verdict — the sniff
// must not refuse ordinary files.
func TestDecision_BinaryMediaStillPlays(t *testing.T) {
	h := newDecisionHandler(t, swappedFile(t, "\x1a\x45\xdf\xa3\x9f\x42\x86\x81\x01matroska-ish"))
	rec := httptest.NewRecorder()
	h.Decision(rec, decisionRequest(t, "videoDecoder=h264,audioDecoder=aac,protocols=mkv:mp4", true))
	if rec.Code != http.StatusOK {
		t.Errorf("binary media refused: got %d %s", rec.Code, rec.Body.String())
	}
}
