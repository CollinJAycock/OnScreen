package observability

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// clusterQ is a fake cluster.Querier dispatching on the scan destination type so
// it satisfies both DetectRole (*bool) and ReplicationLag (*pgtype.Float8).
type clusterQ struct {
	inRecovery bool
	lag        float64
	err        error
}

func (q clusterQ) QueryRow(context.Context, string, ...any) pgx.Row {
	return clusterRow{q}
}

type clusterRow struct{ q clusterQ }

func (r clusterRow) Scan(dest ...any) error {
	if r.q.err != nil {
		return r.q.err
	}
	switch d := dest[0].(type) {
	case *bool:
		*d = r.q.inRecovery
	case *pgtype.Float8:
		*d = pgtype.Float8{Float64: r.q.lag, Valid: true}
	}
	return nil
}

func clusterLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func doClusterStatus(t *testing.T, site string, q clusterQ) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	ClusterStatusHandler(site, q, clusterLogger())(rec, httptest.NewRequest(http.MethodGet, "/health/cluster", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, body
}

func TestClusterStatusHandler_Primary(t *testing.T) {
	code, body := doClusterStatus(t, "site-a", clusterQ{inRecovery: false})
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["role"] != "primary" {
		t.Errorf("role = %v, want primary", body["role"])
	}
	if body["site_id"] != "site-a" {
		t.Errorf("site_id = %v, want site-a", body["site_id"])
	}
	if body["replication_lag_seconds"].(float64) != 0 {
		t.Errorf("primary lag = %v, want 0", body["replication_lag_seconds"])
	}
}

func TestClusterStatusHandler_Standby_ReportsLag(t *testing.T) {
	code, body := doClusterStatus(t, "site-b", clusterQ{inRecovery: true, lag: 3})
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["role"] != "standby" {
		t.Errorf("role = %v, want standby", body["role"])
	}
	if body["replication_lag_seconds"].(float64) != 3 {
		t.Errorf("lag = %v, want 3", body["replication_lag_seconds"])
	}
}

func TestClusterStatusHandler_DBError503(t *testing.T) {
	code, body := doClusterStatus(t, "site-a", clusterQ{err: errors.New("db down")})
	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", code)
	}
	if body["role"] != "unknown" {
		t.Errorf("role = %v, want unknown", body["role"])
	}
}

// ctxClusterQ fails the query when its context is already cancelled, like pgx.
type ctxClusterQ struct{ clusterQ }

func (q ctxClusterQ) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if ctx.Err() != nil {
		return clusterRow{clusterQ{err: ctx.Err()}}
	}
	return q.clusterQ.QueryRow(ctx, sql, args...)
}

// The refreshed status is cached and shared with every poller, so a client that
// disconnects mid-refresh must not cancel the query and publish a 503 to all
// load balancers for the TTL.
func TestClusterStatusHandler_CallerCancelDoesNotPoisonCache(t *testing.T) {
	h := ClusterStatusHandler("site-a", ctxClusterQ{clusterQ{inRecovery: false}}, clusterLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the first caller has already gone
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/health/cluster", nil).WithContext(ctx))

	rec = httptest.NewRecorder() // a healthy poller inside the TTL
	h(rec, httptest.NewRequest(http.MethodGet, "/health/cluster", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("poller after a cancelled refresh got %d, want 200 (cache poisoned)", rec.Code)
	}
}
