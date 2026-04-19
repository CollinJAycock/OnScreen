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
// When allowed is empty, the middleware is a no-op — same-origin
// requests continue to work without any headers added.
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

	if !wildcard && len(set) == 0 {
		return func(next http.Handler) http.Handler { return next }
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
