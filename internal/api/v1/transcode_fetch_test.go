package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/staticabr"
)

// TestFetchFromWorker_NonOKIsError pins the status handling: a worker's error
// response (503 "segment not ready", 401 from a token mismatch) must surface
// as an ERROR, not as file content. It used to be returned as the body, so
// the playlist pipeline rewrote an error string and served it to the player
// as a 200 m3u8 — the player then failed parsing with no hint of the cause.
func TestFetchFromWorker_NonOKIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "segment not ready", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	body, err := fetchFromWorker(context.Background(), addr, "sess1", "index.m3u8", "")
	if err == nil {
		t.Fatalf("worker 503 returned as content: %q — this gets served to the player as a 200 playlist", body)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should carry the worker status for the logs: %v", err)
	}

	// A healthy worker still round-trips content.
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#EXTM3U\n"))
	}))
	defer ok.Close()
	body, err = fetchFromWorker(context.Background(), strings.TrimPrefix(ok.URL, "http://"), "sess1", "index.m3u8", "")
	if err != nil || string(body) != "#EXTM3U\n" {
		t.Errorf("healthy fetch broken: body=%q err=%v", body, err)
	}
}

// TestStart_StaticABRResponseUsesEnvelope pins the response shape of the
// static-ladder path: every client parses Start through the standard
// {"data": ...} envelope, and this branch used respond.JSON (raw body) — so
// whenever a pre-encoded ladder existed the client read `undefined` for
// playlist_url and playback failed on exactly the files that were optimized
// ahead of time.
func TestStart_StaticABRResponseUsesEnvelope(t *testing.T) {
	h, _ := newTestHandlerWithCodec(t, "h264")
	mock := h.media.(*mockTranscodeMedia)
	fileID := mock.files[0].ID
	hash := "h1"
	mock.files[0].FileHash = &hash
	h.staticEnabled = true
	h.cfg.TranscodeABR = true
	h.store = memStaticStore{files: map[string][]byte{
		staticabr.MasterKey(fileID): []byte("#EXTM3U\n"),
		staticabr.HashKey(fileID):   []byte(hash),
	}}

	reqBody, _ := json.Marshal(transcodeStartRequest{Height: 0})
	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(reqBody))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Start: %d\n%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data transcodeStartResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.PlaylistURL == "" {
		t.Fatalf("static-ABR response missing the {\"data\":...} envelope — clients read "+
			"undefined for playlist_url:\n%s", rec.Body.String())
	}
	if !strings.Contains(resp.Data.PlaylistURL, "/transcode/static/") {
		t.Errorf("playlist_url should point at the static ladder: %q", resp.Data.PlaylistURL)
	}
}
