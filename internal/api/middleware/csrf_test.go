package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func csrfReq(method string, headers map[string]string, withCookie bool) *http.Request {
	r := httptest.NewRequest(method, "http://app.example/api/v1/items/x", nil)
	r.Host = "app.example"
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	if withCookie {
		r.AddCookie(&http.Cookie{Name: "onscreen_at", Value: "tok"})
	}
	return r
}

func runCSRF(t *testing.T, r *http.Request) int {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	CSRFGuard([]string{"https://trusted.example"})(next).ServeHTTP(rec, r)
	return rec.Code
}

func TestCSRF_AllowsSafeMethodsEvenCrossSite(t *testing.T) {
	r := csrfReq(http.MethodGet, map[string]string{"Sec-Fetch-Site": "cross-site"}, true)
	if got := runCSRF(t, r); got != http.StatusOK {
		t.Errorf("safe method must pass, got %d", got)
	}
}

func TestCSRF_AllowsBearerEvenCrossSite(t *testing.T) {
	r := csrfReq(http.MethodPost, map[string]string{
		"Sec-Fetch-Site": "cross-site",
		"Authorization":  "Bearer abc",
	}, true)
	if got := runCSRF(t, r); got != http.StatusOK {
		t.Errorf("Bearer client must pass (no cookie CSRF surface), got %d", got)
	}
}

func TestCSRF_AllowsWhenNoAuthCookie(t *testing.T) {
	r := csrfReq(http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, false)
	if got := runCSRF(t, r); got != http.StatusOK {
		t.Errorf("no auth cookie → nothing to protect, must pass, got %d", got)
	}
}

func TestCSRF_BlocksCrossSiteCookieWrite(t *testing.T) {
	for _, site := range []string{"cross-site", "same-site"} {
		r := csrfReq(http.MethodPost, map[string]string{"Sec-Fetch-Site": site}, true)
		if got := runCSRF(t, r); got != http.StatusForbidden {
			t.Errorf("Sec-Fetch-Site=%s cookie write must be blocked, got %d", site, got)
		}
	}
}

func TestCSRF_AllowsSameOriginCookieWrite(t *testing.T) {
	for _, site := range []string{"same-origin", "none"} {
		r := csrfReq(http.MethodPost, map[string]string{"Sec-Fetch-Site": site}, true)
		if got := runCSRF(t, r); got != http.StatusOK {
			t.Errorf("Sec-Fetch-Site=%s must pass, got %d", site, got)
		}
	}
}

func TestCSRF_OriginFallback(t *testing.T) {
	// No Sec-Fetch-Site (old browser). Origin matching Host passes.
	same := csrfReq(http.MethodPost, map[string]string{"Origin": "http://app.example"}, true)
	if got := runCSRF(t, same); got != http.StatusOK {
		t.Errorf("same-origin Origin must pass, got %d", got)
	}
	// Cross-origin Origin is blocked.
	cross := csrfReq(http.MethodPost, map[string]string{"Origin": "https://evil.example"}, true)
	if got := runCSRF(t, cross); got != http.StatusForbidden {
		t.Errorf("cross-origin Origin must be blocked, got %d", got)
	}
	// A configured allowlist origin passes.
	allowed := csrfReq(http.MethodPost, map[string]string{"Origin": "https://trusted.example"}, true)
	if got := runCSRF(t, allowed); got != http.StatusOK {
		t.Errorf("allowlisted Origin must pass, got %d", got)
	}
}

func TestCSRF_FailsOpenWhenNoProvenanceHeaders(t *testing.T) {
	// A cross-site browser attack always carries Sec-Fetch-Site or Origin; their
	// absence means a same-origin or non-browser caller, so allow.
	r := csrfReq(http.MethodPost, map[string]string{}, true)
	if got := runCSRF(t, r); got != http.StatusOK {
		t.Errorf("no provenance headers must fail open, got %d", got)
	}
}

func TestCSRF_RefreshCookieAlsoGuarded(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "http://app.example/api/v1/auth/refresh", nil)
	r.Host = "app.example"
	r.Header.Set("Sec-Fetch-Site", "same-site")
	r.AddCookie(&http.Cookie{Name: "onscreen_rt", Value: "rtok"})
	if got := runCSRF(t, r); got != http.StatusForbidden {
		t.Errorf("same-site write carrying the refresh cookie must be blocked, got %d", got)
	}
}

// loginReq builds an unauthenticated (no cookie) POST to a credential endpoint.
func loginReq(path, contentType string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://app.example"+path, nil)
	r.Host = "app.example"
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

// Login CSRF: with no session cookie the cookie gate never engages, so a
// cross-site form post of the attacker's credentials must be refused on the
// credential endpoints themselves.
func TestCSRF_BlocksCrossSiteFormLogin(t *testing.T) {
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/register", "/api/v1/auth/ldap/login", "/api/v1/auth/totp/verify", "/api/v1/invites/accept"} {
		for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", ""} {
			r := loginReq(path, ct, map[string]string{"Sec-Fetch-Site": "cross-site"})
			if got := runCSRF(t, r); got != http.StatusForbidden {
				t.Errorf("%s with %q cross-site: got %d, want 403", path, ct, got)
			}
		}
		// Old browser without Sec-Fetch-Site: a foreign Origin is the signal.
		r := loginReq(path, "text/plain", map[string]string{"Origin": "https://evil.example"})
		if got := runCSRF(t, r); got != http.StatusForbidden {
			t.Errorf("%s text/plain from foreign Origin: got %d, want 403", path, got)
		}
	}
}

func TestCSRF_AllowsLegitimateLogins(t *testing.T) {
	cases := []struct {
		name    string
		ct      string
		headers map[string]string
	}{
		{"same-origin SPA", "application/json", map[string]string{"Sec-Fetch-Site": "same-origin"}},
		{"same-origin form type", "text/plain", map[string]string{"Sec-Fetch-Site": "same-origin"}},
		{"TV web app (CORS-preflighted JSON)", "application/json; charset=utf-8", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "null"}},
		{"native client (no browser headers)", "", nil},
		{"allowlisted origin, old browser", "text/plain", map[string]string{"Origin": "https://trusted.example"}},
	}
	for _, c := range cases {
		r := loginReq("/api/v1/auth/login", c.ct, c.headers)
		if got := runCSRF(t, r); got != http.StatusOK {
			t.Errorf("%s: got %d, want 200", c.name, got)
		}
	}
	// Non-credential unauthenticated endpoints keep the old no-cookie pass.
	r := loginReq("/api/v1/auth/forgot-password", "text/plain", map[string]string{"Sec-Fetch-Site": "cross-site"})
	if got := runCSRF(t, r); got != http.StatusOK {
		t.Errorf("forgot-password without cookie: got %d, want 200", got)
	}
}

// A proxy that rewrites Host without the port (nginx `$host`) must not make
// old-browser cookie writes fail the Origin fallback on a non-default port:
// X-Forwarded-Host carries the host:port the browser addressed. A mismatching
// X-Forwarded-Host still blocks.
func TestCSRF_OriginFallbackHonoursForwardedHost(t *testing.T) {
	r := csrfReq(http.MethodPost, map[string]string{
		"Origin":           "https://app.example:8443",
		"X-Forwarded-Host": "app.example:8443",
	}, true)
	if got := runCSRF(t, r); got != http.StatusOK {
		t.Errorf("same origin via X-Forwarded-Host: got %d, want 200", got)
	}
	r = csrfReq(http.MethodPost, map[string]string{
		"Origin":           "https://evil.example",
		"X-Forwarded-Host": "app.example:8443",
	}, true)
	if got := runCSRF(t, r); got != http.StatusForbidden {
		t.Errorf("foreign origin: got %d, want 403", got)
	}
}

// nativeAppOriginShapes are the provenance headers a native app's webview
// sends: WebView2 / Chromium set Sec-Fetch-Site (always cross-site from the
// app's own origin to the server); an older WebKit / WebKitGTK may send
// Origin alone.
func nativeAppOriginShapes(origin string) []map[string]string {
	return []map[string]string{
		{"Sec-Fetch-Site": "cross-site", "Origin": origin},
		{"Origin": origin},
	}
}

// runCSRFStock runs the guard as a stock server builds it: no configured
// origins.
func runCSRFStock(r *http.Request) int {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	CSRFGuard(nil)(next).ServeHTTP(rec, r)
	return rec.Code
}

// The desktop app's state-changing requests pass the guard on a stock server,
// exactly as the TV apps' do: they send JSON bodies, fetch with
// `credentials: 'omit'` (no cookie) and carry a Bearer once signed in.
func TestCSRF_DesktopAppRequestsPassOnStockServer(t *testing.T) {
	origins := append([]string{"null", "file://com.onscreen.tv-webos"}, desktopOrigins...)
	for _, origin := range origins {
		for _, hdr := range nativeAppOriginShapes(origin) {
			// Sign-in and the second factor (no session yet, JSON body).
			for path := range credentialPaths {
				if got := runCSRFStock(loginReq(path, "application/json", hdr)); got != http.StatusOK {
					t.Errorf("%v: JSON POST %s = %d, want 200", hdr, path, got)
				}
			}
			// Refresh with the refresh token in the JSON body, with and without
			// the expired access token as Bearer.
			r := loginReq("/api/v1/auth/refresh", "application/json", hdr)
			if got := runCSRFStock(r); got != http.StatusOK {
				t.Errorf("%v: JSON refresh = %d, want 200", hdr, got)
			}
			r = loginReq("/api/v1/auth/refresh", "application/json", hdr)
			r.Header.Set("Authorization", "Bearer expired")
			if got := runCSRFStock(r); got != http.StatusOK {
				t.Errorf("%v: Bearer refresh = %d, want 200", hdr, got)
			}
			// Bearer-authenticated writes (progress, logout, a delete), with
			// no cookie (the apps' real shape) and with a stray auth cookie
			// too: the cookie case pins the Bearer exemption itself, which the
			// no-cookie case passes before ever reaching it.
			for _, m := range []string{http.MethodPut, http.MethodPost, http.MethodDelete} {
				for _, withCookie := range []bool{false, true} {
					r := csrfReq(m, hdr, withCookie)
					r.Header.Set("Authorization", "Bearer tok")
					r.Header.Set("Content-Type", "application/json")
					if got := runCSRFStock(r); got != http.StatusOK {
						t.Errorf("%v: Bearer %s (cookie=%v) = %d, want 200", hdr, m, withCookie, got)
					}
				}
			}
		}
	}
}

// The guard does not share CORS's native-app allowance: a native-app origin is
// a foreign origin here, for the desktop exactly as for the TVs. So a
// form-type body to a credential endpoint, or a cookie-authenticated write
// without a Bearer, is refused from those origins too (no real native client
// sends either). `null` in particular must never be trusted here — a sandboxed
// iframe on any website can send it.
func TestCSRF_NativeAppOriginsGetNoExemption(t *testing.T) {
	origins := append([]string{"null", "file://com.onscreen.tv-webos"}, desktopOrigins...)
	for _, origin := range origins {
		for _, hdr := range nativeAppOriginShapes(origin) {
			if got := runCSRFStock(loginReq("/api/v1/auth/login", "text/plain", hdr)); got != http.StatusForbidden {
				t.Errorf("%v: text/plain login = %d, want 403", hdr, got)
			}
			if got := runCSRFStock(csrfReq(http.MethodPost, hdr, true)); got != http.StatusForbidden {
				t.Errorf("%v: cookie write without Bearer = %d, want 403", hdr, got)
			}
		}
	}
}
