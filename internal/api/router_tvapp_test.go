package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

const tvShellMarker = "onscreen-tv-shell"

// fakeTVApp stands in for a clients/xbox build: an inline-script shell, a
// static file and a directory, so the tests don't depend on a real build.
func fakeTVApp() fstest.MapFS {
	return fstest.MapFS{
		"index.html":      {Data: []byte(`<!doctype html><html><body id="` + tvShellMarker + `"><script>boot()</script></body></html>`)},
		"favicon.svg":     {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		"fonts/inter.txt": {Data: []byte("font bytes")},
	}
}

var cspNonceRe = regexp.MustCompile(`'nonce-([^']+)'`)

// The Xbox shell loads the TV app from the server so it runs same-origin
// with the API. It lives under its own prefix: the SPA already owns /tv
// (Live TV), and those pages must keep loading the SPA on a hard reload.
func TestRouter_TVApp(t *testing.T) {
	router, _, _ := newTestRouterWith(t, t.TempDir(), func(h *Handlers) { h.TVUI = fakeTVApp() })

	tests := []struct {
		name     string
		method   string
		path     string
		wantCode int
		// wantLocation is checked for redirects.
		wantLocation string
		// wantTVShell: the TV index (nonce-stamped, no-store) is served.
		wantTVShell bool
		// wantBody: exact body (a static file); "-" means an empty body.
		wantBody string
		// wantSPA: the main SPA's shell is served, not the TV app's.
		wantSPA bool
	}{
		{name: "bare prefix redirects", method: http.MethodGet, path: "/tvapp", wantCode: http.StatusMovedPermanently, wantLocation: "/tvapp/"},
		{name: "redirect keeps the query", method: http.MethodGet, path: "/tvapp?server=x&y=1", wantCode: http.StatusMovedPermanently, wantLocation: "/tvapp/?server=x&y=1"},
		{name: "HEAD redirects too", method: http.MethodHead, path: "/tvapp", wantCode: http.StatusMovedPermanently, wantLocation: "/tvapp/"},
		{name: "prefix serves the shell", method: http.MethodGet, path: "/tvapp/", wantCode: http.StatusOK, wantTVShell: true},
		{name: "index.html serves the shell", method: http.MethodGet, path: "/tvapp/index.html", wantCode: http.StatusOK, wantTVShell: true},
		{name: "static file", method: http.MethodGet, path: "/tvapp/favicon.svg", wantCode: http.StatusOK, wantBody: `<svg xmlns="http://www.w3.org/2000/svg"/>`},
		{name: "nested static file", method: http.MethodGet, path: "/tvapp/fonts/inter.txt", wantCode: http.StatusOK, wantBody: "font bytes"},
		{name: "HEAD static file", method: http.MethodHead, path: "/tvapp/favicon.svg", wantCode: http.StatusOK, wantBody: "-"},
		{name: "unknown path gets the TV shell", method: http.MethodGet, path: "/tvapp/no/such/page", wantCode: http.StatusOK, wantTVShell: true},
		{name: "directory gets the TV shell", method: http.MethodGet, path: "/tvapp/fonts", wantCode: http.StatusOK, wantTVShell: true},
		{name: "traversal gets the TV shell", method: http.MethodGet, path: "/tvapp/%2e%2e/router.go", wantCode: http.StatusOK, wantTVShell: true},
		{name: "Live TV page stays on the SPA", method: http.MethodGet, path: "/tv", wantCode: http.StatusOK, wantSPA: true},
		{name: "Live TV deep link stays on the SPA", method: http.MethodGet, path: "/tv/guide", wantCode: http.StatusOK, wantSPA: true},
		{name: "SPA fallback unchanged", method: http.MethodGet, path: "/settings/some/page", wantCode: http.StatusOK, wantSPA: true},
		{name: "look-alike prefix stays on the SPA", method: http.MethodGet, path: "/tvappx", wantCode: http.StatusOK, wantSPA: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			body := rec.Body.String()
			if rec.Code != tc.wantCode {
				t.Fatalf("%s %s = %d, want %d (body %.200s)", tc.method, tc.path, rec.Code, tc.wantCode, body)
			}
			if tc.wantLocation != "" {
				if got := rec.Header().Get("Location"); got != tc.wantLocation {
					t.Fatalf("Location = %q, want %q", got, tc.wantLocation)
				}
			}
			if tc.wantTVShell {
				if !strings.Contains(body, tvShellMarker) {
					t.Fatalf("body is not the TV shell: %.200s", body)
				}
				m := cspNonceRe.FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))
				if m == nil {
					t.Fatal("no nonce in the CSP header")
				}
				if !strings.Contains(body, `<script nonce="`+m[1]+`">`) {
					t.Fatalf("inline script not stamped with the CSP nonce %q: %s", m[1], body)
				}
				if got := rec.Header().Get("Cache-Control"); got != "no-store" {
					t.Fatalf("Cache-Control = %q, want no-store", got)
				}
				if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
					t.Fatalf("Content-Type = %q, want text/html", got)
				}
			}
			switch tc.wantBody {
			case "":
			case "-":
				if body != "" {
					t.Fatalf("HEAD body = %q, want empty", body)
				}
			default:
				if body != tc.wantBody {
					t.Fatalf("body = %q, want %q", body, tc.wantBody)
				}
				if got := rec.Header().Get("Cache-Control"); got == "no-store" {
					t.Fatal("static file marked no-store; only the shell must be")
				}
			}
			if tc.wantSPA {
				if strings.Contains(body, tvShellMarker) {
					t.Fatal("served the TV shell where the main SPA belongs")
				}
				if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
					t.Fatalf("Content-Type = %q, want the SPA's text/html shell", got)
				}
			}
		})
	}
}

// API paths keep their JSON 404 — neither app's shell may answer them.
func TestRouter_TVApp_APIStillJSON404(t *testing.T) {
	router, _, _ := newTestRouterWith(t, t.TempDir(), func(h *Handlers) { h.TVUI = fakeTVApp() })
	for _, path := range []string{"/api/v1/no-such-route", "/api/tvapp/"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("GET %s Content-Type = %q, want application/json", path, got)
		}
		if strings.Contains(rec.Body.String(), tvShellMarker) {
			t.Fatalf("GET %s served the TV shell", path)
		}
	}
}

// Without an injected filesystem the router uses the embedded build, or the
// placeholder page when this checkout never built clients/xbox.
func TestRouter_TVApp_EmbeddedDefault(t *testing.T) {
	router, _, _ := newTestRouter(t, t.TempDir())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tvapp/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tvapp/ = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}
