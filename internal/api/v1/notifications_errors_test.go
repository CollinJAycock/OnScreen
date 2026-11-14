package v1

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onscreen/onscreen/internal/notification"
)

// A failed mark-read used to be an unlogged 500. It is logged now, and a
// write that reached a read-only replica (the bug that sent these UPDATEs to
// the RO pool) is a retryable 503.
func TestNotifications_WriteFailuresAreLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	h := NewNotificationHandler(&mockNotifDB{markAllErr: errors.New("connection refused")}, notification.NewBroker(), logger)
	rec := httptest.NewRecorder()
	h.MarkAllRead(rec, notifAuthedRequest(httptest.NewRequest("POST", "/api/v1/notifications/read-all", nil)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	if !strings.Contains(buf.String(), "connection refused") || !strings.Contains(buf.String(), "level=ERROR") {
		t.Fatalf("500 without a log line: %q", buf.String())
	}

	readOnly := fmt.Errorf("mark read: %w", &pgconn.PgError{Code: "25006"})
	h = NewNotificationHandler(&mockNotifDB{markErr: readOnly}, notification.NewBroker(), logger)
	rec = httptest.NewRecorder()
	id := uuid.New().String()
	h.MarkRead(rec, notifAuthedRequest(withChiParam(httptest.NewRequest("POST", "/api/v1/notifications/"+id+"/read", nil), "id", id)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("read-only replica: status %d, want 503", rec.Code)
	}
}
