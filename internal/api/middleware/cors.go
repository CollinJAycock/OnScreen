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
// **First-party native-app webviews are always allowed** (see
// isNativeAppOrigin for the exact set and the threat model):
//   - TV apps: webOS / Tizen / Android-WebView apps load their HTML from
//     file:// URLs and send either `Origin: null` (most common) or
//     `Origin: file://<app-id>`.
//   - The desktop app: the Tauri 2 webview serves the bundled web UI from
//     `http://tauri.localhost` on Windows (`https://tauri.localhost` if
//     the app ever turns on useHttpsScheme) and `tauri://localhost` on
//     macOS / Linux.
//
// Every one of these is the *expected* origin for every install of that
// client, and every API call from it is cross-origin. Auto-allowing them
// removes a major footgun: an operator who doesn't set CORS to `*` would
// otherwise see the app silently fail with a confusing "Failed to fetch"
// on the first API call (the browser blocks the response, or the preflight
// OPTIONS falls through to the router and 405s), and the failure mode
// looks like a server bug.
//
// The allowance is safe because this middleware never sets
// `Access-Control-Allow-Credentials`: the browser never hands a
// cookie-authenticated response to a cross-origin page, so an allowed
// origin can only act with credentials it already holds (the native apps
// send `Authorization: Bearer`).
//
// Every response carries `Vary: Origin`, with or without an Origin header,
// because whether and how CORS headers appear depends on it: without it a
// shared cache in front of the server could hand a response fetched without
// an Origin (so with no Allow-Origin) to an app's cross-origin request. Add,
// not Set, so a handler's own Vary values survive. Beyond that, when
// `allowed` is empty AND no native-app origin is present, the middleware
// adds nothing — same-origin requests work as before.
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
			w.Header().Add("Vary", "Origin")
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowOrigin := ""
			switch {
			case wildcard:
				allowOrigin = "*"
			case isNativeAppOrigin(origin):
				// TV / desktop native-webview apps. See
				// isNativeAppOrigin for the threat-model justification.
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
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			// Range: the web player's one-byte probes (playback-stop.ts,
			// webAudioGain.ts). Content-Disposition and
			// X-OnScreen-Schema-Version: read by the backup page.
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With, X-Client-Capabilities, Range")
			h.Set("Access-Control-Expose-Headers", "X-Request-ID, X-OnScreen-Client-Caps, Content-Disposition, X-OnScreen-Schema-Version")
			h.Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// tauriWebviewOrigins are the exact Origin values the Tauri 2 desktop app's
// webview sends when it serves the bundled web UI (it never adds a port):
//
//   - `http://tauri.localhost` — Windows (WebView2), and Android; the Tauri 2
//     default.
//   - `https://tauri.localhost` — the same platforms with the app's
//     `useHttpsScheme` option on (also Tauri 1's Windows default). The desktop
//     app doesn't set it today; it is listed so turning it on later can't
//     silently break the app against every server that hasn't updated.
//   - `tauri://localhost` — macOS / iOS (WKWebView) and Linux (WebKitGTK),
//     served from Tauri's registered custom URI scheme.
//
// Exact matches only. A prefix / suffix / port-tolerant match would admit
// origins a stranger can serve: `http://tauri.localhost.evil.com` is a public
// DNS name, and `tauri://localhost.evil`, `http://evil-tauri.localhost` and
// `https://tauri.localhost:8443` are not origins Tauri ever produces.
var tauriWebviewOrigins = map[string]struct{}{
	"http://tauri.localhost":  {},
	"https://tauri.localhost": {},
	"tauri://localhost":       {},
}

// isNativeAppOrigin reports whether the request's Origin header is one a
// first-party native app's webview sends, rather than a regular website's.
// Three shapes:
//
//   - `null` — Chrome / Chromium-based webviews (webOS 5/6, modern
//     Tizen, Android System WebView) send `Origin: null` for any
//     document loaded from file://. Treated as opaque-origin per
//     the Fetch spec.
//   - `file://<host>` — older webviews send the literal file:// URL,
//     where <host> is the app's installed-package identifier
//     (com.onscreen.tv-webos, OnScreenTV.OnScreen, etc.).
//   - the Tauri desktop webview's origins (tauriWebviewOrigins).
//
// Threat model — this auto-allow is NOT a security boundary, and nothing may
// treat it as one:
//
//   - A hostile page CAN produce `Origin: null` (a sandboxed iframe, or a
//     data:/blob: document).
//   - An ordinary website can't produce the Tauri origins: Origin is a
//     forbidden request header, `tauri:` is a custom scheme only a Tauri app
//     registers, and `.localhost` is a reserved name (RFC 6761) that public
//     DNS never delegates and the major browsers resolve to loopback. That
//     is defence in depth, not a guarantee — an engine that sends the name
//     to a hostile resolver could load an attacker's page there — and every
//     Tauri app shares these origins, so a match means "a Tauri webview",
//     not "the OnScreen desktop app".
//
// Either way an allowed origin is worth no more than `null`, and it is safe
// only because this middleware never sets
// `Access-Control-Allow-Credentials: true`. Without that header the browser
// won't expose the response of a credentialed (cookie-carrying) cross-origin
// request to the page, nor send a preflighted one at all, so an allowed origin
// can act only with credentials it already holds. The native apps hold their
// own: the TV and desktop apps authenticate with `Authorization: Bearer` (the
// desktop fetches with `credentials: 'omit'`), never with the cookie. Do NOT
// add `Allow-Credentials: true` here without first replacing this with a
// strict origin allowlist.
func isNativeAppOrigin(origin string) bool {
	if origin == "null" || strings.HasPrefix(origin, "file://") {
		return true
	}
	_, ok := tauriWebviewOrigins[origin]
	return ok
}
