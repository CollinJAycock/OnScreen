package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// desktopOrigins are the Origin values the Tauri desktop app's webview sends:
// Windows (WebView2), its useHttpsScheme variant, then macOS / Linux.
var desktopOrigins = []string{
	"http://tauri.localhost",
	"https://tauri.localhost",
	"tauri://localhost",
}

// Every API call the desktop app makes is cross-origin. A stock server (empty
// allowlist) must echo its origin, or every call dies as "Failed to fetch".
func TestCORS_desktopOriginsAutoAllowed_emptyAllowlist(t *testing.T) {
	h := CORS(nil)(okHandler())
	for _, origin := range desktopOrigins {
		t.Run(origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
			req.Header.Set("Origin", origin)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("downstream status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
				t.Errorf("Allow-Origin = %q, want the origin echoed", got)
			}
			if got := rec.Header().Get("Vary"); got != "Origin" {
				t.Errorf("Vary = %q, want Origin", got)
			}
			if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Errorf("Allow-Credentials = %q; the auto-allow must never grant credentials", got)
			}
		})
	}
}

// The desktop's preflight (a JSON POST carrying Authorization and the
// capability header) must be answered by the middleware itself. Before the
// fix it fell through to the router, which 405s an OPTIONS on a POST route.
func TestCORS_desktopPreflightShortCircuits(t *testing.T) {
	for _, origin := range desktopOrigins {
		t.Run(origin, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusMethodNotAllowed)
			})
			h := CORS(nil)(next)
			req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
			req.Header.Set("Origin", origin)
			req.Header.Set("Access-Control-Request-Method", "POST")
			req.Header.Set("Access-Control-Request-Headers", "authorization,content-type,x-client-capabilities")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Errorf("preflight status = %d, want 204", rec.Code)
			}
			if called {
				t.Error("preflight must not reach the downstream handler")
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
				t.Errorf("Allow-Origin = %q, want %q", got, origin)
			}
			if got := rec.Header().Get("Vary"); got != "Origin" {
				t.Errorf("Vary = %q, want Origin", got)
			}
			methods := rec.Header().Get("Access-Control-Allow-Methods")
			for _, want := range []string{"POST", "PUT", "DELETE"} {
				if !strings.Contains(methods, want) {
					t.Errorf("Allow-Methods = %q, missing %s", methods, want)
				}
			}
			allowHeaders := strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers"))
			for _, want := range []string{"authorization", "content-type", "x-client-capabilities"} {
				if !strings.Contains(allowHeaders, want) {
					t.Errorf("Allow-Headers = %q, missing %q", allowHeaders, want)
				}
			}
			if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Errorf("Allow-Credentials = %q; the auto-allow must never grant credentials", got)
			}
		})
	}
}

// Only the exact Tauri origins are allowed. Look-alikes a stranger can serve
// (a public DNS name under tauri.localhost.*), or that Tauri never sends (a
// different host under the scheme, a sibling *.localhost name, a port — Tauri
// serves without one, so a port means some other local server — a trailing
// slash, other casing), are refused like any unknown origin, both on a stock
// server and next to a configured allowlist.
func TestCORS_desktopLookalikeOriginsRejected(t *testing.T) {
	lookalikes := []string{
		"http://tauri.localhost.evil.com",
		"https://tauri.localhost.evil.com",
		"tauri://localhost.evil",
		"tauri://evil",
		"http://evil-tauri.localhost",
		"http://evil.tauri.localhost",
		"https://tauri.localhost:8443",
		"http://tauri.localhost:80",
		"tauri://localhost:1420",
		"http://tauri.localhost/",
		"HTTP://TAURI.LOCALHOST",
		"http://localhost",
		" tauri://localhost",
	}
	for _, allowed := range [][]string{nil, {"https://allowed.example"}} {
		h := CORS(allowed)(okHandler())
		for _, origin := range lookalikes {
			for _, method := range []string{http.MethodGet, http.MethodOptions} {
				req := httptest.NewRequest(method, "/api/v1/hub", nil)
				req.Header.Set("Origin", origin)
				if method == http.MethodOptions {
					req.Header.Set("Access-Control-Request-Method", "POST")
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
					t.Errorf("allowlist %v, %s from %q: Allow-Origin = %q, want none", allowed, method, origin, got)
				}
				if method == http.MethodOptions && rec.Code == http.StatusNoContent {
					t.Errorf("allowlist %v: preflight from %q answered 204; it must fall through", allowed, origin)
				}
			}
		}
	}
}

// Adding the desktop origins changes nothing for the TV origins or for a
// configured allowlist: TV origins and configured origins are still echoed,
// unknown ones still refused, and the wildcard still answers `*` (for a
// desktop origin too).
func TestCORS_desktopAllowanceLeavesOtherOriginsUnchanged(t *testing.T) {
	cases := []struct {
		allowed []string
		origin  string
		want    string
	}{
		{nil, "null", "null"},
		{nil, "file://com.onscreen.tv-webos", "file://com.onscreen.tv-webos"},
		{nil, "https://evil.example", ""},
		{[]string{"https://app.example"}, "null", "null"},
		{[]string{"https://app.example"}, "https://app.example", "https://app.example"},
		{[]string{"https://app.example"}, "https://evil.example", ""},
		{[]string{"https://app.example"}, "http://tauri.localhost", "http://tauri.localhost"},
		// The pre-fix workaround (origins typed into Settings) keeps working.
		{[]string{"http://tauri.localhost", "tauri://localhost"}, "tauri://localhost", "tauri://localhost"},
		{[]string{"*"}, "tauri://localhost", "*"},
		{[]string{"*"}, "https://evil.example", "*"},
	}
	for _, c := range cases {
		h := CORS(c.allowed)(okHandler())
		req := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
		req.Header.Set("Origin", c.origin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != c.want {
			t.Errorf("allowlist %v, origin %q: Allow-Origin = %q, want %q", c.allowed, c.origin, got, c.want)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("allowlist %v, origin %q: Allow-Credentials = %q, want none", c.allowed, c.origin, got)
		}
	}
}

func TestIsNativeAppOrigin(t *testing.T) {
	for _, o := range []string{"null", "file://", "file://com.onscreen.tv-webos", "http://tauri.localhost", "https://tauri.localhost", "tauri://localhost"} {
		if !isNativeAppOrigin(o) {
			t.Errorf("isNativeAppOrigin(%q) = false, want true", o)
		}
	}
	for _, o := range []string{"", "NULL", "https://tauri.localhost:8443", "http://tauri.localhost.evil.com", "tauri://localhost.evil", "http://evil-tauri.localhost", "https://app.example"} {
		if isNativeAppOrigin(o) {
			t.Errorf("isNativeAppOrigin(%q) = true, want false", o)
		}
	}
}

// Vary: Origin goes on every response, allowed or not and with no Origin at
// all, so a shared cache never serves a response fetched without CORS headers
// to an app's cross-origin request. Added, not set: a handler's own Vary
// values stay.
func TestCORS_alwaysVariesOnOrigin(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed []string
		origin  string
	}{
		{"no origin, stock server", nil, ""},
		{"no origin, wildcard", []string{"*"}, ""},
		{"origin not allowed", nil, "https://example.com"},
		{"desktop origin", nil, "http://tauri.localhost"},
		{"allowlisted origin", []string{"https://web.example"}, "https://web.example"},
	} {
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Add("Vary", "Accept-Encoding")
		})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		rec := httptest.NewRecorder()
		CORS(tc.allowed)(next).ServeHTTP(rec, req)
		vary := rec.Header().Values("Vary")
		if len(vary) != 2 || vary[0] != "Origin" || vary[1] != "Accept-Encoding" {
			t.Errorf("%s: Vary = %q, want [Origin Accept-Encoding]", tc.name, vary)
		}
	}
}

// The web UI reads Content-Disposition and X-OnScreen-Schema-Version on the
// backup download and sends Range on its one-byte probes; cross-origin (the
// desktop app) they must be exposed / allowed.
func TestCORS_exposesBackupHeadersAndAllowsRange(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/admin/backup", nil)
	req.Header.Set("Origin", "http://tauri.localhost")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	CORS(nil)(okHandler()).ServeHTTP(rec, req)

	expose := rec.Header().Get("Access-Control-Expose-Headers")
	for _, want := range []string{"Content-Disposition", "X-OnScreen-Schema-Version", "X-Request-ID"} {
		if !strings.Contains(expose, want) {
			t.Errorf("Expose-Headers = %q, missing %s", expose, want)
		}
	}
	if allow := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(allow, "Range") {
		t.Errorf("Allow-Headers = %q, missing Range", allow)
	}
}
