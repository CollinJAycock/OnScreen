package middleware

import (
	"net/http"
	"strings"
)

// CORS returns a middleware that adds Access-Control-Allow-* headers for
// requests whose Origin matches the allowlist. When allowed contains a
// single "*" entry, any origin is accepted (use only when the API
// authenticates via Authorization header, not cookies).
//
// Preflight OPTIONS requests are short-circuited with a 204 response
// so they do not hit downstream handlers.
//
// **TV apps and local-file webviews are always allowed.** webOS / Tizen
// / Android-WebView apps load their HTML from file:// URLs and send
// either `Origin: null` (most common) or `Origin: file://<app-id>` in
// fetch requests. These origins:
//   - can only originate from a TV app installed on the user's device,
//     not from a hostile website (no website can spoof them),
//   - cannot piggy-back on browser cookie credentials (file:// origins
//     have no cookie jar), so the worst-case echo of `*` for credentialed
//     auth isn't reachable from this path,
//   - are the *expected* origin for every TV-client install.
//
// Auto-allowing them removes a major footgun: an operator who doesn't
// set CORS to `*` would otherwise see TVs silently fail with a
// confusing "Failed to fetch" the first time the app tries to call any
// API endpoint, and the failure mode looks like a server bug.
//
// When `allowed` is empty AND no file://-style origin is present, the
// middleware is a no-op — same-origin requests work without any
// headers added.
func CORS(allowed []string) func(http.Handler) http.Handler {
	wildcard := false
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" {
			wildcard = true
			continue
		}
		set[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowOrigin := ""
			switch {
			case wildcard:
				allowOrigin = "*"
			case isTVAppOrigin(origin):
				// TV / native-webview apps. See the docstring for
				// the threat-model justification.
				allowOrigin = origin
			default:
				if _, ok := set[origin]; ok {
					allowOrigin = origin
				}
			}

			if allowOrigin == "" {
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			h.Set("Access-Control-Allow-Origin", allowOrigin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With")
			h.Set("Access-Control-Expose-Headers", "X-Request-ID")
			h.Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isTVAppOrigin reports whether the request's Origin header indicates
// a TV / native-webview app rather than a regular browser. Two shapes
// to catch:
//
//   - `null` — Chrome / Chromium-based webviews (webOS 5/6, modern
//     Tizen, Android System WebView) send `Origin: null` for any
//     document loaded from file://. Treated as opaque-origin per
//     the Fetch spec.
//   - `file://<host>` — older webviews send the literal file:// URL,
//     where <host> is the app's installed-package identifier
//     (com.onscreen.tv-webos, OnScreenTV.OnScreen, etc.).
//
// Neither can be sent by a regular browser via a hostile website —
// browsers refuse to set these Origin values from http(s)// contexts.
// So this auto-allow doesn't widen the attack surface for credentialed
// cookie auth (file:// has no cookie jar anyway).
func isTVAppOrigin(origin string) bool {
	return origin == "null" || strings.HasPrefix(origin, "file://")
}
