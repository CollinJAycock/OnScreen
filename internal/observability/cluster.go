package observability

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/onscreen/onscreen/internal/cluster"
)

// ClusterStatusHandler reports this node's site identity, PostgreSQL role
// (primary vs read-only standby), and replication lag — the operability surface
// for multi-site DR / active-active reads (HA roadmap §6). Read by operators,
// monitoring, and geo-routing to know which site, and whether it's writable,
// is serving. Bounded by a 1s timeout so it never blocks.
func ClusterStatusHandler(siteID string, q cluster.Querier, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()

		type status struct {
			SiteID            string  `json:"site_id,omitempty"`
			Role              string  `json:"role"`
			ReplicationLagSec float64 `json:"replication_lag_seconds"`
		}
		out := status{SiteID: siteID, Role: string(cluster.RoleUnknown)}

		role, err := cluster.DetectRole(ctx, q)
		if err != nil {
			logger.WarnContext(ctx, "cluster status: detect role", "err", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		out.Role = string(role)

		if role == cluster.RoleStandby {
			if lag, lerr := cluster.ReplicationLag(ctx, q); lerr == nil {
				out.ReplicationLagSec = lag.Seconds()
			} else {
				logger.WarnContext(ctx, "cluster status: replication lag", "err", lerr)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
