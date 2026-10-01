package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The desktop app (Tauri) calls the API cross-origin from its own webview
// origin. On a stock server — no CORS origins configured — every call failed
// with "Failed to fetch": the preflight fell through to the router and 405'd,
// and no response carried Access-Control-Allow-Origin. Exercised through the
// real router so the CORS → CSRF ordering is covered too.
func TestRouter_DesktopOriginsWorkOnStockServer(t *testing.T) {
	router, _, _ := newTestRouter(t, t.TempDir())

	for _, origin := range []string{"http://tauri.localhost", "https://tauri.localhost", "tauri://localhost"} {
		t.Run(origin, func(t *testing.T) {
			// Preflight for the sign-in POST.
			req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
			req.Header.Set("Origin", origin)
			req.Header.Set("Access-Control-Request-Method", "POST")
			req.Header.Set("Access-Control-Request-Headers", "content-type,x-client-capabilities")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("preflight = %d, want 204", rec.Code)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
				t.Fatalf("preflight Allow-Origin = %q, want %q", got, origin)
			}
			if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Fatalf("preflight Allow-Credentials = %q, want none", got)
			}

			// The real request, as WebView2 sends it: cross-site, JSON body.
			req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader("{}"))
			req.Header.Set("Origin", origin)
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			req.Header.Set("Content-Type", "application/json")
			rec = httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("POST /auth/logout = %d, want 204 (body %s)", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
				t.Fatalf("response Allow-Origin = %q, want %q", got, origin)
			}
			if got := rec.Header().Get("Vary"); got != "Origin" {
				t.Fatalf("response Vary = %q, want Origin", got)
			}
		})
	}

	// A look-alike gets nothing: the preflight is not answered by CORS.
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "http://tauri.localhost.evil.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusNoContent {
		t.Fatalf("look-alike preflight = 204, want it to fall through")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("look-alike Allow-Origin = %q, want none", got)
	}
}
