package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/observability"
)

// RequestID injects a UUID request ID into every request context and response
// header. Respects an existing X-Request-ID header from upstream proxies, but
// only when it is a plausible correlation ID: the value is echoed into every
// log line, error body and the admin log view, so an arbitrary client-supplied
// string (megabytes long, or crafted to forge/confuse log correlation) is
// replaced with a fresh UUID.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = uuid.New().String()
		}
		ctx := observability.ContextWithRequestID(r.Context(), id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// validRequestID accepts 1–64 characters of [A-Za-z0-9._-] — enough for UUIDs,
// Cloudflare ray IDs and typical proxy trace IDs, nothing that can smuggle
// whitespace, control characters or JSON into log output.
func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// requestIDFromContext is a convenience re-export used internally.
func requestIDFromContext(ctx context.Context) string {
	return observability.RequestIDFromContext(ctx)
}
