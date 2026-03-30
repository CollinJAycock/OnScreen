package observability

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Pinger is implemented by anything that can be health-checked with a ping.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthHandler returns handlers for /health/live and /health/ready.
//
//   - /health/live  → 200 if the process is alive (Docker healthcheck)
//   - /health/ready → 200 if DB + Valkey connections are healthy (load balancer)
//
// /health/ready uses a 1s timeout and never blocks.
func HealthHandler(db Pinger, valkey Pinger, logger *slog.Logger) (live, ready http.HandlerFunc) {
	live = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}

	ready = func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()

		type check struct {
			name  string
			pinger Pinger
		}
		checks := []check{
			{"postgres", db},
			{"valkey", valkey},
		}

		type result struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks"`
		}
		res := result{
			Status: "ok",
			Checks: make(map[string]string, len(checks)),
		}
		httpStatus := http.StatusOK

		for _, c := range checks {
			if err := c.pinger.Ping(ctx); err != nil {
				res.Checks[c.name] = "unhealthy: " + err.Error()
				res.Status = "degraded"
				httpStatus = http.StatusServiceUnavailable
				logger.Warn("health check failed", "component", c.name, "err", err)
			} else {
				res.Checks[c.name] = "ok"
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		_ = json.NewEncoder(w).Encode(res)
	}

	return live, ready
}
