package middleware

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

type nonceCtxKey struct{}

// NonceFromContext returns the per-request CSP nonce stamped by
// SecurityHeaders, or "" if the middleware didn't run (e.g. a unit test
// that exercises a handler directly). The SPA shell handler reads this
// to stamp `nonce="…"` on its inline <script> tags so they satisfy the
// `script-src 'nonce-…'` directive.
func NonceFromContext(ctx context.Context) string {
	s, _ := ctx.Value(nonceCtxKey{}).(string)
	return s
}

// newNonce returns a 128-bit base64 nonce. crypto/rand failure is
// treated as fatal-for-this-request by returning "" — the caller still
// emits a CSP, just without a usable nonce, so a transient RNG hiccup
// degrades to "inline scripts blocked" (fail closed) rather than
// "inline scripts allowed".
func newNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

// SecurityHeaders adds baseline security headers to every response.
//
// CSP notes:
//   - script-src is nonce-based: a fresh per-request nonce replaces the
//     old 'unsafe-inline'. The SPA shell's two inline <script> tags
//     (theme bootstrap + SvelteKit start) are stamped with the matching
//     nonce by the shell handler; everything they pull in is a
//     same-origin module (covered by 'self'), so no 'strict-dynamic'
//     is needed. An injected <script> without the nonce is blocked —
//     that's the XSS mitigation 'unsafe-inline' threw away.
//   - style-src KEEPS 'unsafe-inline'. The SPA renders dynamic inline
//     style="…" attributes pervasively (progress bars, fanart
//     backgrounds, layout math); nonces don't cover style attributes,
//     and there's no build-time hash for per-render values. Dropping it
//     would need every dynamic style refactored to CSS custom
//     properties — out of scope, and the XSS value of style-src
//     hardening is far lower than script-src.
//   - blob: appears on img-src / style-src / font-src so epub.js can
//     render archive resources from Blob URLs in its rendition iframe.
//   - static.cloudflareinsights.com / cloudflareinsights.com are
//     allow-listed for Cloudflare's auto-injected Web Analytics beacon
//     (loaded as an external <script src>, so the nonce switch doesn't
//     affect it; Rocket Loader, which inlines scripts, is an opt-in
//     incompatibility an operator would have to relax CSP for).
//   - www.gstatic.com is allow-listed on script-src + frame-src for the
//     Google Cast sender SDK (cast_sender.js + the Cast picker iframe).
//     The watch screen injects the script dynamically when a user opens
//     it, so without the allowance the SDK silently no-ops and the Cast
//     button is dead.
//   - base-uri 'self' and object-src 'none' are set explicitly. base-uri
//     does NOT fall back to default-src, so without it an injected <base>
//     could re-root every relative script URL to an attacker origin and
//     sidestep the script-src nonce. object-src 'none' kills legacy
//     <object>/<embed> plugin vectors unconditionally.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := newNonce()
		csp := "default-src 'self'; " +
			"base-uri 'self'; " +
			"object-src 'none'; " +
			"script-src 'self' 'nonce-" + nonce + "' https://static.cloudflareinsights.com https://www.gstatic.com; " +
			"style-src 'self' 'unsafe-inline' blob:; " +
			"img-src 'self' data: https: blob:; " +
			"font-src 'self' data: blob:; " +
			"media-src 'self' blob:; " +
			"connect-src 'self' https://cloudflareinsights.com; " +
			"frame-src 'self' https://www.gstatic.com; " +
			"frame-ancestors 'none'"

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", csp)
		// HSTS only when the request is actually HTTPS. Sending Strict-Transport-
		// Security over plain HTTP is ignored by browsers per RFC 6797 §7.2,
		// but gating it avoids confusing operators who curl --insecure in dev.
		// 1 year max-age, includeSubDomains; preload is opt-in by the operator.
		if IsSecure(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		ctx := context.WithValue(r.Context(), nonceCtxKey{}, nonce)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
