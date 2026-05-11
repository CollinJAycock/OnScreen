package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestCORS_disabledWhenEmpty(t *testing.T) {
	h := CORS(nil)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no Allow-Origin when allowed=nil, got %q", got)
	}
}

func TestCORS_wildcard(t *testing.T) {
	h := CORS([]string{"*"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
	req.Header.Set("Origin", "https://tv.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected * Allow-Origin, got %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("expected Vary: Origin, got %q", got)
	}
}

func TestCORS_allowlistEchoesMatchingOrigin(t *testing.T) {
	h := CORS([]string{"https://tv.local"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
	req.Header.Set("Origin", "https://tv.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://tv.local" {
		t.Errorf("expected echoed origin, got %q", got)
	}
}

func TestCORS_allowlistRejectsUnknownOrigin(t *testing.T) {
	h := CORS([]string{"https://tv.local"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no Allow-Origin for unknown origin, got %q", got)
	}
}

func TestCORS_preflightShortCircuits(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	})
	h := CORS([]string{"*"})(next)
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/hub", nil)
	req.Header.Set("Origin", "https://tv.local")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 on preflight, got %d", rec.Code)
	}
	if called {
		t.Error("preflight should not reach downstream handler")
	}
}

func TestCORS_noOriginHeaderPassesThrough(t *testing.T) {
	h := CORS([]string{"*"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no headers without Origin, got %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected downstream 200, got %d", rec.Code)
	}
}

// TVs / webviews loading from file:// send `Origin: null` (Chromium-
// based webOS 5+ / Tizen 6+ / Android System WebView). The middleware
// auto-allows them so operators don't have to know to add `null` to
// their CORS allowlist — without this, the first install of a TV app
// silently fails with "Failed to fetch" on every API call and the
// failure mode looks like a server bug.
func TestCORS_nullOriginAutoAllowed_emptyAllowlist(t *testing.T) {
	h := CORS(nil)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
	req.Header.Set("Origin", "null")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "null" {
		t.Errorf("null origin should be echoed back even with empty allowlist; got %q", got)
	}
}

// Older webviews send the literal file:// URL with the app-id as host
// instead of the opaque `null`. webOS uses com.<vendor>.tv-webos;
// Tizen uses <PartnerID>.<AppName>. Both shapes need to pass.
func TestCORS_fileOriginAutoAllowed(t *testing.T) {
	cases := []string{
		"file://com.onscreen.tv-webos",
		"file://OnScreenTV.OnScreen",
		"file://",
	}
	h := CORS(nil)(okHandler())
	for _, origin := range cases {
		t.Run(origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
			req.Header.Set("Origin", origin)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
				t.Errorf("file:// origin %q should be echoed back; got %q", origin, got)
			}
		})
	}
}

// Auto-allow only fires for file://-style origins. A regular browser
// request from an http(s):// origin that's not in the allowlist must
// still be rejected — this is the path that protects against hostile
// websites making cross-origin reads.
func TestCORS_httpOriginStillRejectedWhenNotInAllowlist(t *testing.T) {
	h := CORS([]string{"https://allowed.example"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("unknown http origin must be rejected; got %q", got)
	}
}

// Preflight from a TV origin still short-circuits with 204 even when
// the allowlist is empty — same as wildcard, but via the auto-allow
// path. Otherwise the TV's preflight OPTIONS hits a downstream handler
// that may 401 / 405 and the browser blocks the real request.
func TestCORS_tvPreflightShortCircuitsWithEmptyAllowlist(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	})
	h := CORS(nil)(next)
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/hub", nil)
	req.Header.Set("Origin", "null")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("TV preflight should 204 even with empty allowlist, got %d", rec.Code)
	}
	if called {
		t.Error("TV preflight should not reach downstream handler")
	}
}
