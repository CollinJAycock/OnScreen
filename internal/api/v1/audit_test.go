package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

type mockAuditDB struct {
	rows   []gen.ListAuditLogRow
	err    error
	gotArg gen.ListAuditLogParams
	called bool
}

func (m *mockAuditDB) ListAuditLog(_ context.Context, arg gen.ListAuditLogParams) ([]gen.ListAuditLogRow, error) {
	m.called = true
	m.gotArg = arg
	return m.rows, m.err
}

func TestAudit_List_Defaults(t *testing.T) {
	db := &mockAuditDB{}
	h := NewAuditHandler(db, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if db.gotArg.Limit != 50 || db.gotArg.Offset != 0 {
		t.Errorf("defaults: got limit=%d offset=%d, want 50/0", db.gotArg.Limit, db.gotArg.Offset)
	}
}

func TestAudit_List_AppliesParams(t *testing.T) {
	db := &mockAuditDB{}
	h := NewAuditHandler(db, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?limit=10&offset=25", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if db.gotArg.Limit != 10 || db.gotArg.Offset != 25 {
		t.Errorf("params: got limit=%d offset=%d, want 10/25", db.gotArg.Limit, db.gotArg.Offset)
	}
}

func TestAudit_List_ClampsLimit(t *testing.T) {
	db := &mockAuditDB{}
	h := NewAuditHandler(db, slog.Default())

	// limit > 200 is clamped to max; negative offset falls back to 0.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?limit=9999&offset=-1", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if db.gotArg.Limit != 200 {
		t.Errorf("over-max limit should clamp to 200: got %d", db.gotArg.Limit)
	}
	if db.gotArg.Offset != 0 {
		t.Errorf("negative offset should fall back to 0: got %d", db.gotArg.Offset)
	}
}

func TestAudit_List_MapsRows(t *testing.T) {
	userID := uuid.New()
	target := "library:abc"
	db := &mockAuditDB{
		rows: []gen.ListAuditLogRow{
			{
				ID:        uuid.New(),
				UserID:    pgtype.UUID{Bytes: userID, Valid: true},
				Action:    "library.delete",
				Target:    &target,
				Detail:    []byte(`{"note":"test"}`),
				IpAddr:    "10.0.0.1",
				CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			},
		},
	}
	h := NewAuditHandler(db, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "library.delete") {
		t.Errorf("body missing action: %s", body)
	}
	if !strings.Contains(body, "library:abc") {
		t.Errorf("body missing target: %s", body)
	}
	if !strings.Contains(body, "10.0.0.1") {
		t.Errorf("body missing ip: %s", body)
	}
}

func TestAudit_List_DBError(t *testing.T) {
	db := &mockAuditDB{err: errors.New("boom")}
	h := NewAuditHandler(db, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}
