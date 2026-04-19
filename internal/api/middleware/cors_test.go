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
