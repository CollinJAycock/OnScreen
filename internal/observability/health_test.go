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
)

// stubPinger satisfies Pinger with a fixed err result.
type stubPinger struct{ err error }

func (s stubPinger) Ping(_ context.Context) error { return s.err }

func TestHealth_LiveAlwaysOK(t *testing.T) {
	live, _ := HealthHandler(stubPinger{}, stubPinger{}, nil, slog.Default())
	rec := httptest.NewRecorder()
	live(rec, httptest.NewRequest("GET", "/health/live", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

func TestHealth_ReadyAllOKReturns200(t *testing.T) {
	_, ready := HealthHandler(stubPinger{}, stubPinger{}, nil, discardLogger())
	rec := httptest.NewRecorder()
	ready(rec, httptest.NewRequest("GET", "/health/ready", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Checks["postgres"] != "ok" || body.Checks["valkey"] != "ok" {
		t.Errorf("checks = %v, want both ok", body.Checks)
	}
}

func TestHealth_ReadyDBDownReturns503(t *testing.T) {
	_, ready := HealthHandler(
		stubPinger{err: errors.New("connection refused")},
		stubPinger{},
		nil, discardLogger(),
	)
	rec := httptest.NewRecorder()
	ready(rec, httptest.NewRequest("GET", "/health/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 — DB down should fail readiness", rec.Code)
	}
	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
	if got := body.Checks["postgres"]; got == "ok" {
		t.Errorf("postgres check = %q, want unhealthy: ...", got)
	}
}

func TestHealth_ReadyValkeyDownReturns503(t *testing.T) {
	_, ready := HealthHandler(
		stubPinger{},
		stubPinger{err: errors.New("nope")},
		nil, discardLogger(),
	)
	rec := httptest.NewRecorder()
	ready(rec, httptest.NewRequest("GET", "/health/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestHealth_ReadyPendingMigrationsDegrades(t *testing.T) {
	migStatus := func() (int64, int64, int64, bool) {
		return 50, 48, 2, true // 2 pending
	}
	_, ready := HealthHandler(stubPinger{}, stubPinger{}, migStatus, discardLogger())

	rec := httptest.NewRecorder()
	ready(rec, httptest.NewRequest("GET", "/health/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 — pending migrations should fail readiness", rec.Code)
	}
	var body struct {
		Checks map[string]string `json:"checks"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if got := body.Checks["migrations"]; got == "ok" {
		t.Errorf("migrations check = %q, want pending", got)
	}
}

func TestHealth_ReadyMigrationsCaughtUpReturns200(t *testing.T) {
	migStatus := func() (int64, int64, int64, bool) {
		return 50, 50, 0, true
	}
	_, ready := HealthHandler(stubPinger{}, stubPinger{}, migStatus, discardLogger())

	rec := httptest.NewRecorder()
	ready(rec, httptest.NewRequest("GET", "/health/ready", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestHealth_ReadyMigrationStatusUnknownStillOK(t *testing.T) {
	// !ok from the status function (typically a transient query fail)
	// must NOT degrade the entire readiness check — the DB ping already
	// covers "DB is broken" and we don't want a flaky version-table read
	// to start crashloops.
	migStatus := func() (int64, int64, int64, bool) {
		return 0, 0, 0, false
	}
	_, ready := HealthHandler(stubPinger{}, stubPinger{}, migStatus, discardLogger())

	rec := httptest.NewRecorder()
	ready(rec, httptest.NewRequest("GET", "/health/ready", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (unknown migration status must not crashloop)", rec.Code)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
