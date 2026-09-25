package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/onscreen/onscreen/internal/observability"
)

// knownMethods is the fixed set of HTTP methods that may appear as a metric
// label. Anything else collapses to "OTHER" so a client cannot mint unbounded
// Prometheus series (a new counter + histogram per distinct method) by sending
// requests with a different arbitrary method each time — an unauthenticated
// memory-exhaustion vector, since chi answers 405 only AFTER the label is
// recorded and series are never evicted.
var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true,
	http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
	http.MethodOptions: true, http.MethodConnect: true, http.MethodTrace: true,
}

func normalizeMethod(m string) string {
	if knownMethods[m] {
		return m
	}
	return "OTHER"
}

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
			method := normalizeMethod(r.Method)
			m.HTTPRequestsTotal.WithLabelValues(method, path, strconv.Itoa(rw.status)).Inc()
			m.HTTPRequestDuration.WithLabelValues(method, path).Observe(time.Since(start).Seconds())
		})
	}
}
