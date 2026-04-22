package middleware

import "net/http"

// SecurityHeaders adds baseline security headers to every response.
func SecurityHeaders(next http.Handler) http.Handler {
	// CSP: allow self-origin scripts & styles (Svelte uses inline styles),
	// external images (TMDB posters), blob: media (HLS.js), and no framing.
	// SvelteKit emits inline <script> tags for hydration, so script-src needs 'unsafe-inline'.
	const csp = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: https:; media-src 'self' blob:; " +
		"connect-src 'self'; frame-ancestors 'none'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		next.ServeHTTP(w, r)
	})
}
