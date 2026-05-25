package respond

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsReadOnlyError(t *testing.T) {
	if !IsReadOnlyError(&pgconn.PgError{Code: "25006"}) {
		t.Error("SQLSTATE 25006 should be a read-only error")
	}
	// Wrapped is still detected.
	if !IsReadOnlyError(fmt.Errorf("upsert: %w", &pgconn.PgError{Code: "25006"})) {
		t.Error("wrapped 25006 should be detected")
	}
	if IsReadOnlyError(&pgconn.PgError{Code: "23505"}) {
		t.Error("a unique-violation must not be treated as read-only")
	}
	if IsReadOnlyError(errors.New("plain error")) {
		t.Error("a non-pg error must not be read-only")
	}
}

func TestServiceUnavailable(t *testing.T) {
	rec := httptest.NewRecorder()
	ServiceUnavailable(rec, httptest.NewRequest(http.MethodPost, "/x", nil), "read-only standby")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
