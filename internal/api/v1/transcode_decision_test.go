package v1

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

func newDecisionHandler(t *testing.T, file media.File) *NativeTranscodeHandler {
	t.Helper()
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	segToken := transcode.NewSegmentTokenManager(v)
	cfg := &config.Config{TranscodeMaxHeight: 2160}
	itemID := uuid.New()
	file.MediaItemID = itemID
	return NewNativeTranscodeHandler(store, segToken, &mockTranscodeMedia{
		item:  &media.Item{ID: itemID, Type: "movie", Title: "Test"},
		files: []media.File{file},
	}, cfg, slog.Default()).WithVerifySource(fakeSourceOK)
}

func decisionRequest(t *testing.T, capsHeader string, withAuth bool) *http.Request {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/playback-decision",
		bytes.NewReader([]byte("{}")))
	req = withChiParam(req, "id", uuid.New().String())
	if withAuth {
		req = withClaims(req)
	}
	if capsHeader != "" {
		req.Header.Set("X-Client-Capabilities", capsHeader)
	}
	return req
}

func bodyHas(rec *httptest.ResponseRecorder, s string) bool {
	return bytes.Contains(rec.Body.Bytes(), []byte(s))
}

func TestDecision_DirectPlayWhenCompatible(t *testing.T) {
	h := newDecisionHandler(t, media.File{
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mp4"),
		AudioStreams: []byte(`[{"channels":6}]`), // 5.1
	})
	rec := httptest.NewRecorder()
	h.Decision(rec, decisionRequest(t, "videoDecoder=h264,audioDecoder=aac,protocols=mp4", true))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !bodyHas(rec, `"decision":"directPlay"`) {
		t.Errorf("want directPlay; body: %s", rec.Body.String())
	}
	// Must use the standard {"data": ...} envelope — clients' ApiResponse<T>
	// parsers require it; a raw body makes every client fall back to local.
	if !bodyHas(rec, `"data":`) {
		t.Errorf("response must be {data:...}-wrapped; body: %s", rec.Body.String())
	}
}

func TestDecision_DirectStreamWhen7_1ExceedsCap(t *testing.T) {
	h := newDecisionHandler(t, media.File{
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mp4"),
		AudioStreams: []byte(`[{"channels":8}]`), // 7.1 — exceeds the default 5.1 cap
	})
	rec := httptest.NewRecorder()
	h.Decision(rec, decisionRequest(t, "videoDecoder=h264,audioDecoder=aac,protocols=mp4", true))
	// H.264 video copies; only the 7.1 audio needs downmixing → video-copy remux.
	if !bodyHas(rec, `"decision":"directStream"`) {
		t.Errorf("7.1 on h264 should directStream (video-copy + downmix); body: %s", rec.Body.String())
	}
}

func TestDecision_TranscodeWhenVideoUnsupported(t *testing.T) {
	h := newDecisionHandler(t, media.File{
		VideoCodec: strPtr("av1"), AudioCodec: strPtr("aac"), Container: strPtr("mkv"),
		AudioStreams: []byte(`[{"channels":6}]`),
	})
	rec := httptest.NewRecorder()
	// Client decodes h264 only; AV1 can't be decoded or remuxed → full transcode.
	h.Decision(rec, decisionRequest(t, "videoDecoder=h264,audioDecoder=aac,protocols=mkv:mp4", true))
	if !bodyHas(rec, `"decision":"transcode"`) {
		t.Errorf("AV1 on a non-AV1 client should transcode; body: %s", rec.Body.String())
	}
}

func TestDecision_DirectStreamWhenContainerUnsupported(t *testing.T) {
	h := newDecisionHandler(t, media.File{
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mkv"),
		AudioStreams: []byte(`[{"channels":2}]`),
	})
	rec := httptest.NewRecorder()
	// Client decodes h264+aac but only plays mp4/ts — remux (directStream).
	h.Decision(rec, decisionRequest(t, "videoDecoder=h264,audioDecoder=aac,protocols=mp4:ts", true))
	if !bodyHas(rec, `"decision":"directStream"`) {
		t.Errorf("mkv on an mp4/ts client should directStream; body: %s", rec.Body.String())
	}
}

func TestDecision_Unauthorized(t *testing.T) {
	h := newDecisionHandler(t, media.File{VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mp4")})
	rec := httptest.NewRecorder()
	h.Decision(rec, decisionRequest(t, "", false))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}
