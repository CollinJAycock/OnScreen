package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code, body.Error.Message
}

// StreamFile refuses the stopped (user, item, client) with 403
// PLAYBACK_STOPPED — every range request, so a client ignoring the SSE event
// stops once its buffer runs dry — while the same user's other device and
// another user at the same address keep streaming.
func TestStreamFile_PlaybackStopScopedToStoppedClient(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "movie.mp4")
	if err := os.WriteFile(tmp, bytes.Repeat([]byte("z"), 64), 0o644); err != nil {
		t.Fatal(err)
	}
	itemID, user := uuid.New(), uuid.New()
	ms := &mockItemMedia{
		file: &media.File{ID: uuid.New(), Status: "active", FilePath: tmp, MediaItemID: itemID},
		item: &media.Item{ID: itemID, LibraryID: uuid.New(), Type: "movie", Title: "Test"},
	}
	guard := NewPlaybackStopGuard(nil, slog.Default())
	tr := streaming.NewTracker()
	h := NewItemHandler(ms, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil, tr, slog.Default()).
		WithPlaybackStops(guard)
	guard.Block(context.Background(), user, itemID, "192.0.2.10", "Server rebooting")

	serve := func(uid uuid.UUID, remote, rng string) *httptest.ResponseRecorder {
		req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", ms.file.ID.String())
		req.RemoteAddr = remote
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uid}))
		rec := httptest.NewRecorder()
		h.StreamFile(rec, req)
		return rec
	}

	for _, rng := range []string{"", "bytes=32-"} {
		rec := serve(user, "192.0.2.10:5000", rng)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("stopped client (Range %q): got %d, want 403", rng, rec.Code)
		}
		if code, msg := errorCode(t, rec); code != "PLAYBACK_STOPPED" || msg != "Playback was stopped by the server admin: Server rebooting" {
			t.Errorf("error: got %q %q", code, msg)
		}
	}
	if rec := serve(user, "192.0.2.11:5000", ""); rec.Code != http.StatusOK {
		t.Errorf("same user, other device: got %d, want 200", rec.Code)
	}
	if rec := serve(uuid.New(), "192.0.2.10:5000", ""); rec.Code != http.StatusOK {
		t.Errorf("other user, same address: got %d, want 200", rec.Code)
	}

	// Served bytes are attributed to the viewer + item in the tracker.
	var attributed bool
	for _, e := range tr.List() {
		if e.UserID == user && e.MediaItemID == itemID && e.ClientIP == "192.0.2.11" && e.FileID == ms.file.ID {
			attributed = true
		}
	}
	if !attributed {
		t.Errorf("TouchFile attribution missing: %+v", tr.List())
	}
}

// DownloadFile serves the same bytes as StreamFile (with Range support), so the
// stopped (user, item, client) is refused there too — a stopped client can't
// keep streaming the item through /media/download — while another device of
// the same user still downloads.
func TestDownloadFile_PlaybackStopScopedToStoppedClient(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "movie.mp4")
	if err := os.WriteFile(tmp, bytes.Repeat([]byte("z"), 64), 0o644); err != nil {
		t.Fatal(err)
	}
	itemID, user := uuid.New(), uuid.New()
	ms := &mockItemMedia{
		file: &media.File{ID: uuid.New(), Status: "active", FilePath: tmp, MediaItemID: itemID},
		item: &media.Item{ID: itemID, LibraryID: uuid.New(), Type: "movie", Title: "Test"},
	}
	guard := NewPlaybackStopGuard(nil, slog.Default())
	h := NewItemHandler(ms, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil, nil, slog.Default()).
		WithPlaybackStops(guard)
	guard.Block(context.Background(), user, itemID, "192.0.2.20", "Server rebooting")

	download := func(remote, rng string) *httptest.ResponseRecorder {
		req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", ms.file.ID.String())
		req.RemoteAddr = remote
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: user}))
		rec := httptest.NewRecorder()
		h.DownloadFile(rec, req)
		return rec
	}

	for _, rng := range []string{"", "bytes=32-"} {
		rec := download("192.0.2.20:5000", rng)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("stopped client (Range %q): got %d, want 403", rng, rec.Code)
		}
		if code, msg := errorCode(t, rec); code != "PLAYBACK_STOPPED" || msg != "Playback was stopped by the server admin: Server rebooting" {
			t.Errorf("error: got %q %q", code, msg)
		}
		if cd := rec.Header().Get("Content-Disposition"); cd != "" {
			t.Errorf("a refused download must not start an attachment, got Content-Disposition %q", cd)
		}
	}
	if rec := download("192.0.2.21:5000", "bytes=32-"); rec.Code != http.StatusPartialContent {
		t.Errorf("same user, other device: got %d, want 206", rec.Code)
	}
}

// A 'playing' beat from the stopped client is refused (so a player running off
// its buffer learns it was stopped) and doesn't resurrect the Now Playing
// card; pause/stop reports still record.
func TestProgress_PlaybackStopRefusesPlayingBeat(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	guard := NewPlaybackStopGuard(nil, slog.Default())
	tr := streaming.NewTracker()
	ws := &captureWatch{}
	h := NewItemHandler(&mockItemMedia{}, ws, &mockSessionCleaner{}, nil, nil, nil, nil, tr, slog.Default()).
		WithPlaybackStops(guard)
	guard.Block(context.Background(), user, itemID, "192.0.2.1", "")

	rec := httptest.NewRecorder()
	h.Progress(rec, progressReq(t, itemID, user, `{"view_offset_ms":1000,"duration_ms":9000,"state":"playing"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("playing beat: got %d, want 403", rec.Code)
	}
	if code, _ := errorCode(t, rec); code != "PLAYBACK_STOPPED" {
		t.Errorf("code: got %q", code)
	}
	if len(tr.List()) != 0 {
		t.Error("a refused beat must not put the card back in Now Playing")
	}
	runProgress(t, h, itemID, user, `{"view_offset_ms":1000,"duration_ms":9000,"state":"paused"}`)
	if len(ws.events) != 1 {
		t.Errorf("paused report should still record, got %d events", len(ws.events))
	}
}

// Heartbeats are attributed to the viewer with the client's own decision.
func TestProgress_HeartbeatAttributedToViewer(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	tr := streaming.NewTracker()
	h := NewItemHandler(&mockItemMedia{}, &captureWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil, tr, slog.Default())
	runProgress(t, h, itemID, user, `{"view_offset_ms":1,"duration_ms":9,"state":"playing","client_name":"Pixel 8","decision":"directStream"}`)
	entries := tr.List()
	if len(entries) != 1 || entries[0].UserID != user || entries[0].Decision != "directStream" || entries[0].ClientName != "Pixel 8" {
		t.Fatalf("heartbeat entry: got %+v", entries)
	}
	runProgress(t, h, itemID, user, `{"view_offset_ms":1,"duration_ms":9,"state":"stopped"}`)
	if len(tr.List()) != 0 {
		t.Error("a stopped beat must remove the viewer's heartbeat entry")
	}
}

func newStartHandler(t *testing.T, file media.File) (*NativeTranscodeHandler, *transcode.SessionStore) {
	t.Helper()
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	h := NewNativeTranscodeHandler(store, transcode.NewSegmentTokenManager(v), &mockTranscodeMedia{
		item:  &media.Item{ID: file.MediaItemID, Type: "movie", Title: "Test"},
		files: []media.File{file},
	}, &config.Config{TranscodeMaxHeight: 2160, TranscodeMaxWidth: 3840, TranscodeMaxBitrate: 40000}, slog.Default()).
		WithVerifySource(fakeSourceOK)
	return h, store
}

func startAs(t *testing.T, h *NativeTranscodeHandler, itemID, user uuid.UUID, remote, caps string, body transcodeStartRequest) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := withChiParam(httptest.NewRequest("POST", "/", bytes.NewReader(b)), "id", itemID.String())
	req.RemoteAddr = remote
	if caps != "" {
		req.Header.Set("X-Client-Capabilities", caps)
	}
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: user}))
	rec := httptest.NewRecorder()
	h.Start(rec, req)
	return rec
}

func onlySession(t *testing.T, store *transcode.SessionStore) transcode.Session {
	t.Helper()
	all, err := store.List(context.Background())
	if err != nil || len(all) != 1 {
		t.Fatalf("sessions: got %d (%v), want 1", len(all), err)
	}
	return all[0]
}

func ip(v int) *int { return &v }

// Start refuses a stopped remux client its replacement session; another
// device is unaffected.
func TestStart_PlaybackStopBlocksReplacementSession(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("dts"), ResolutionW: ip(1920), ResolutionH: ip(1080)}
	h, store := newStartHandler(t, file)
	guard := NewPlaybackStopGuard(nil, slog.Default())
	h.WithPlaybackStops(guard)
	guard.Block(context.Background(), user, itemID, "192.0.2.50", "")

	rec := startAs(t, h, itemID, user, "192.0.2.50:1", "", transcodeStartRequest{VideoCopy: true})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("stopped client: got %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if code, _ := errorCode(t, rec); code != "PLAYBACK_STOPPED" {
		t.Errorf("code: got %q", code)
	}
	if all, _ := store.List(context.Background()); len(all) != 0 {
		t.Errorf("a refused Start created %d sessions", len(all))
	}
	if rec := startAs(t, h, itemID, user, "192.0.2.51:1", "", transcodeStartRequest{VideoCopy: true}); rec.Code != http.StatusOK {
		t.Errorf("other device: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// Start stamps the viewer's address, the reasons and the output shape on the
// session it creates — the data behind the Now Playing card.
func TestStart_StampsNowPlayingAttribution(t *testing.T) {
	t.Run("transcode", func(t *testing.T) {
		itemID, user := uuid.New(), uuid.New()
		file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
			VideoCodec: strPtr("hevc"), AudioCodec: strPtr("aac"), ResolutionW: ip(3840), ResolutionH: ip(2160)}
		h, store := newStartHandler(t, file)
		rec := startAs(t, h, itemID, user, "203.0.113.4:9", "videoDecoder=h264,audioDecoder=aac", transcodeStartRequest{Height: 720})
		if rec.Code != http.StatusOK {
			t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
		}
		s := onlySession(t, store)
		if s.ClientIP != "203.0.113.4" {
			t.Errorf("client ip: got %q", s.ClientIP)
		}
		want := []string{"HEVC not supported by client", "3840×2160 exceeds client max 1920×1080", "Quality set to 720p by the viewer"}
		if !reflect.DeepEqual(s.TranscodeReasons, want) {
			t.Errorf("reasons:\n got  %q\n want %q", s.TranscodeReasons, want)
		}
		if s.OutputHeight != 720 || s.OutputWidth == 0 || s.OutputAudioCodec != "aac" {
			t.Errorf("output: %dx%d audio=%q", s.OutputWidth, s.OutputHeight, s.OutputAudioCodec)
		}
	})
	t.Run("remux keeps only audio/container reasons", func(t *testing.T) {
		itemID, user := uuid.New(), uuid.New()
		file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
			VideoCodec: strPtr("h264"), AudioCodec: strPtr("dts"), ResolutionW: ip(3840), ResolutionH: ip(2160)}
		h, store := newStartHandler(t, file)
		rec := startAs(t, h, itemID, user, "192.168.1.9:9", "videoDecoder=h264,audioDecoder=aac", transcodeStartRequest{VideoCopy: true})
		if rec.Code != http.StatusOK {
			t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
		}
		s := onlySession(t, store)
		if !reflect.DeepEqual(s.TranscodeReasons, []string{"DTS audio not supported by client"}) {
			t.Errorf("reasons: got %q", s.TranscodeReasons)
		}
		if s.OutputHeight != 0 || s.OutputAudioCodec != "aac" {
			t.Errorf("remux output: %dx%d audio=%q", s.OutputWidth, s.OutputHeight, s.OutputAudioCodec)
		}
	})
}

func TestStartReasons(t *testing.T) {
	br := int64(25_000_000)
	base := func() *media.File {
		return &media.File{VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mkv"), Bitrate: &br}
	}
	caps := transcode.ParseCapabilities("videoDecoder=h264,audioDecoder=aac")

	if got := startReasons(base(), caps, "transcode", 0, 1080, 8000, true); !reflect.DeepEqual(got, []string{"Over the user's bitrate cap (25 Mbps > 8 Mbps)"}) {
		t.Errorf("cap forced: got %q", got)
	}
	if got := startReasons(base(), caps, "transcode", 0, 1080, 0, false); !reflect.DeepEqual(got, []string{"Requested by client"}) {
		t.Errorf("nothing visible: got %q", got)
	}
	if got := startReasons(base(), caps, "remux", 0, 1080, 0, false); len(got) != 1 || !strings.Contains(got[0], "requested by client") {
		t.Errorf("remux fallback: got %q", got)
	}
	// An explicit pick at or above the source isn't a reason.
	if got := startReasons(base(), caps, "transcode", 1080, 1080, 0, false); !reflect.DeepEqual(got, []string{"Requested by client"}) {
		t.Errorf("pick == source: got %q", got)
	}
	if got := fmtMbps(2534); got != "2.5 Mbps" {
		t.Errorf("fmtMbps: got %q", got)
	}
}
