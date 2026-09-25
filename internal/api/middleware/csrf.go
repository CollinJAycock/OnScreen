package middleware

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// CSRFGuard blocks cross-site state-changing requests that ride a browser's
// auth cookie.
//
// The cookie session is protected by SameSite=Lax, but SameSite is scoped to
// the registrable SITE, not the ORIGIN: a sibling subdomain or another port on
// the same host (common on a homelab where several apps share a domain) is
// "same-site" and its script can drive a cookie-authenticated POST. This guard
// closes that by requiring positive proof the request did NOT originate cross-
// site before letting a cookie-authenticated unsafe method through.
//
// It only ever affects requests that (a) use an unsafe method and (b) carry an
// OnScreen auth cookie and (c) do NOT carry an Authorization: Bearer header.
// Native clients authenticate with Bearer (no ambient credential, no CSRF
// surface) and are untouched; safe methods are untouched; unauthenticated
// requests are untouched.
//
// Decision, for a guarded request:
//   - Sec-Fetch-Site is browser-set and unspoofable. same-origin / none → allow;
//     same-site / cross-site → block. This covers essentially all modern browsers.
//   - If Sec-Fetch-Site is absent (very old / non-browser client), fall back to
//     Origin: allow when it matches this server's own origin or a configured
//     allowlist entry, block when it is present and does not.
//   - If neither header is present, allow: a cross-site attacker's browser always
//     sends at least one of them, so their absence means a same-origin or
//     non-browser caller, which SameSite could not have protected against anyway.
func CSRFGuard(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" && o != "*" {
			allowed[strings.ToLower(o)] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Login CSRF runs the other way round: the victim has NO session
			// yet, so the cookie gate below never engages. A cross-site form
			// (enctype=text/plain can carry a JSON-shaped body) posting the
			// ATTACKER's credentials would leave the victim's browser silently
			// signed in to the attacker's account — or, on a fresh install,
			// create the first admin with a password the attacker chose.
			// Browsers can only send such a request without a CORS preflight
			// when the Content-Type is a form/text type, so refusing a
			// cross-origin non-JSON body here closes it while leaving every
			// real client alone (web and TV apps send application/json; native
			// clients send no Sec-Fetch-Site/Origin at all).
			if !isSafeMethod(r.Method) && credentialPaths[r.URL.Path] &&
				crossOriginRequest(r, allowed) && !isJSONBody(r) {
				denyCSRF(w)
				return
			}
			if isSafeMethod(r.Method) || hasBearer(r) || !hasAuthCookie(r) {
				next.ServeHTTP(w, r)
				return
			}

			switch strings.ToLower(r.Header.Get("Sec-Fetch-Site")) {
			case "same-origin", "none":
				next.ServeHTTP(w, r)
				return
			case "same-site", "cross-site":
				denyCSRF(w)
				return
			}

			// No Sec-Fetch-Site — fall back to Origin.
			if origin := r.Header.Get("Origin"); origin != "" {
				if originMatches(origin, r) || allowed[strings.ToLower(strings.TrimRight(origin, "/"))] {
					next.ServeHTTP(w, r)
					return
				}
				denyCSRF(w)
				return
			}

			// Neither signal present — a cross-site browser attack would have
			// carried one, so this is same-origin or a non-browser client.
			next.ServeHTTP(w, r)
		})
	}
}

// credentialPaths are the unauthenticated endpoints that establish a session
// (or, for register on a fresh install, the admin account).
var credentialPaths = map[string]bool{
	"/api/v1/auth/login":       true,
	"/api/v1/auth/ldap/login":  true,
	"/api/v1/auth/totp/verify": true,
	"/api/v1/auth/register":    true,
	"/api/v1/invites/accept":   true,
}

// crossOriginRequest reports whether a browser marked the request as coming
// from another origin: Sec-Fetch-Site when present, else a foreign Origin.
func crossOriginRequest(r *http.Request, allowed map[string]bool) bool {
	switch strings.ToLower(r.Header.Get("Sec-Fetch-Site")) {
	case "same-origin", "none":
		return false
	case "same-site", "cross-site":
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	return !originMatches(origin, r) && !allowed[strings.ToLower(strings.TrimRight(origin, "/"))]
}

// isJSONBody reports whether the request declares a JSON body — a type a
// browser will only send cross-origin after a CORS preflight.
func isJSONBody(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && (mt == "application/json" || strings.HasSuffix(mt, "+json"))
}

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

func hasBearer(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// hasAuthCookie reports whether the request carries an OnScreen auth cookie —
// the access-token cookie or the refresh cookie (the refresh cookie is
// SameSite=Strict and scoped to /api/v1/auth, but a same-site sibling can still
// send it, so guard it too).
func hasAuthCookie(r *http.Request) bool {
	for _, name := range []string{"onscreen_at", "onscreen_rt"} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return true
		}
	}
	return false
}

// originMatches reports whether the Origin header names this server's own
// origin, comparing host:port (the scheme a TLS-terminating proxy presents can
// differ from what the app sees, so the host comparison is the reliable part).
//
// A reverse proxy commonly rewrites Host without the port (nginx `$host`), so
// a site published on a non-default port would never match on r.Host alone and
// every cookie write from an old browser would be refused. X-Forwarded-Host
// (first entry) is accepted as the addressed host too: a cross-site page cannot
// add that header to a form post or other preflight-free request, so the only
// value it can carry is the one the proxy set from what the browser addressed.
func originMatches(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		first, _, _ := strings.Cut(fwd, ",")
		return strings.EqualFold(u.Host, strings.TrimSpace(first))
	}
	return false
}

func denyCSRF(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    "CSRF_BLOCKED",
			"message": "cross-site request blocked; retry from the app's own origin",
		},
	})
}
