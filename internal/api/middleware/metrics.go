package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/onscreen/onscreen/internal/observability"
)

// Metrics records per-request count + latency to Prometheus. The path label uses
// the chi route TEMPLATE (e.g. "/items/{id}"), resolved after the request is
// routed, so per-ID URLs collapse to one series instead of exploding cardinality.
// Unrouted requests (404s with no pattern) fall back to "<unrouted>" for the
// same reason — a scanner hitting random paths can't mint unbounded series.
func Metrics(m *observability.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rw, r)

			path := "<unrouted>"
			if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePattern() != "" {
				path = rctx.RoutePattern()
			}
			m.HTTPRequestsTotal.WithLabelValues(r.Method, path, strconv.Itoa(rw.status)).Inc()
			m.HTTPRequestDuration.WithLabelValues(r.Method, path).Observe(time.Since(start).Seconds())
		})
	}
}
