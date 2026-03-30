package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/onscreen/onscreen/internal/observability"
)

// RequestID injects a UUID request ID into every request context and response
// header. Respects an existing X-Request-ID header from upstream proxies.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.New().String()
		}
		ctx := observability.ContextWithRequestID(r.Context(), id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requestIDFromContext is a convenience re-export used internally.
func requestIDFromContext(ctx context.Context) string {
	return observability.RequestIDFromContext(ctx)
}
