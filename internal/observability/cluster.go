package observability

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/onscreen/onscreen/internal/cluster"
)

// ClusterStatusHandler reports this node's site identity, PostgreSQL role
// (primary vs read-only standby), and replication lag — the operability surface
// for multi-site DR / active-active reads (HA roadmap §6). Read by operators,
// monitoring, and geo-routing to know which site, and whether it's writable,
// is serving. Bounded by a 1s timeout so it never blocks.
func ClusterStatusHandler(siteID string, q cluster.Querier, logger *slog.Logger) http.HandlerFunc {
	type status struct {
		SiteID            string  `json:"site_id,omitempty"`
		Role              string  `json:"role"`
		ReplicationLagSec float64 `json:"replication_lag_seconds"`
	}
	// This endpoint is public and unauthenticated (geo-routing / load balancers
	// poll it), and every call ran up to two Postgres queries. Serve a result
	// cached for a short TTL so a flood of requests costs one query per TTL
	// instead of one per request, while health checkers still see a fresh-enough
	// role and lag.
	const cacheTTL = 2 * time.Second
	var (
		mu       sync.Mutex
		cachedAt time.Time
		cached   status
		cachedOK bool
	)
	return func(w http.ResponseWriter, r *http.Request) {
		// The lock is held across the refresh so concurrent callers wait for
		// one query (bounded by the 1 s timeout) instead of stampeding.
		mu.Lock()
		defer mu.Unlock()
		if time.Since(cachedAt) < cacheTTL {
			writeClusterStatus(w, cached, cachedOK)
			return
		}

		// Detached from the caller: the result is shared with every poller for
		// the TTL, so one client disconnecting mid-refresh must not cancel the
		// query and publish a spurious 503 ("role unknown") to all of them.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Second)
		defer cancel()

		out := status{SiteID: siteID, Role: string(cluster.RoleUnknown)}
		ok := true
		role, err := cluster.DetectRole(ctx, q)
		if err != nil {
			logger.WarnContext(ctx, "cluster status: detect role", "err", err)
			ok = false
		} else {
			out.Role = string(role)
			if role == cluster.RoleStandby {
				if lag, lerr := cluster.ReplicationLag(ctx, q); lerr == nil {
					out.ReplicationLagSec = lag.Seconds()
				} else {
					logger.WarnContext(ctx, "cluster status: replication lag", "err", lerr)
				}
			}
		}

		cached, cachedOK, cachedAt = out, ok, time.Now()
		writeClusterStatus(w, out, ok)
	}
}

// writeClusterStatus renders a cluster status body, 503 when the role could not
// be determined.
func writeClusterStatus(w http.ResponseWriter, v any, ok bool) {
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(v)
}
