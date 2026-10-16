package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/transcode"
	"github.com/onscreen/onscreen/internal/valkey"
)

// A Chromecast's Default Media Receiver runs on Google's origin and reads a
// cast's HLS session, the static ladder and its subtitle VTT with XHR. Those
// routes, and only those, answer `Access-Control-Allow-Origin: *` (their
// credential is the ?token= in the URL); the rest of the API stays as it was.
func TestRouter_TokenURLMediaRoutesAllowAnyOrigin(t *testing.T) {
	const receiver = "https://www.gstatic.com"
	router, _, _ := newTestRouterWith(t, t.TempDir(), func(h *Handlers) {
		mr := miniredis.RunT(t)
		vc, err := valkey.New(context.Background(), "redis://"+mr.Addr())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = vc.Close() })
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		h.NativeTranscode = v1.NewNativeTranscodeHandler(transcode.NewSessionStore(vc),
			transcode.NewSegmentTokenManager(vc), nil, &config.Config{}, logger)
		h.Items = &v1.ItemHandler{}
		h.Subtitles = &v1.SubtitleHandler{}
	})
	sid := uuid.New().String()
	fid := uuid.New().String()
	tokenRoutes := []string{
		"/api/v1/transcode/sessions/" + sid + "/playlist.m3u8?token=bad",
		"/api/v1/transcode/sessions/" + sid + "/seg/seg00000.ts?token=bad",
		"/api/v1/transcode/sessions/" + sid + "/abr/720p/index.m3u8?token=bad",
		"/api/v1/transcode/sessions/" + sid + "/abr/720p/seg/seg00000.ts?token=bad",
		"/api/v1/transcode/static/" + fid + "/master.m3u8",
		"/api/v1/transcode/static/" + fid + "/720p/index.m3u8",
		"/api/v1/transcode/static/" + fid + "/720p/seg/seg00000.ts",
		"/media/subtitles/" + fid + "/2",
		"/media/subtitles/" + fid + "/external/" + uuid.New().String(),
		"/media/external-subtitles/" + uuid.New().String(),
	}
	for _, path := range tokenRoutes {
		t.Run("GET "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Origin", receiver)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			// Readable by the receiver, but still refused without a valid token.
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 for a request without a valid token", rec.Code)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Errorf("Allow-Origin = %q, want *", got)
			}
			if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Errorf("Allow-Credentials = %q, want none", got)
			}
		})
		t.Run("OPTIONS "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, path, nil)
			req.Header.Set("Origin", receiver)
			req.Header.Set("Access-Control-Request-Method", "GET")
			req.Header.Set("Access-Control-Request-Headers", "range")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("preflight = %d, want 204", rec.Code)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Errorf("preflight Allow-Origin = %q, want *", got)
			}
		})
	}

	// Everything else (the general API, direct play, the session's stop)
	// gained nothing: a foreign origin still gets no Allow-Origin.
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/items/" + uuid.New().String()},
		{http.MethodGet, "/api/v1/hub"},
		{http.MethodGet, "/media/stream/" + fid},
		{http.MethodPost, "/api/v1/items/" + uuid.New().String() + "/transcode"},
		{http.MethodOptions, "/api/v1/transcode/sessions/" + sid},
		{http.MethodOptions, "/api/v1/items/" + uuid.New().String() + "/playback-decision"},
	} {
		t.Run("untouched "+tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Origin", receiver)
			if tc.method == http.MethodOptions {
				req.Header.Set("Access-Control-Request-Method", "POST")
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("Allow-Origin = %q, want none", got)
			}
		})
	}
}
