package respond

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func TestServerError_LogsAndAnswers500(t *testing.T) {
	logger, buf := captureLogger()
	rec := httptest.NewRecorder()
	ServerError(rec, httptest.NewRequest(http.MethodGet, "/x", nil), logger, errors.New("db down"), "list things", "user_id", "u1")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "list things") ||
		!strings.Contains(out, "db down") || !strings.Contains(out, "user_id=u1") {
		t.Fatalf("log line %q lacks level, message, error or attrs", out)
	}
}

// A client that hung up is not a server failure: no ERROR, no 5xx.
func TestServerError_ClientGoneIsQuiet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, tc := range map[string]struct {
		r   *http.Request
		err error
	}{
		"cancelled request": {httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx), fmt.Errorf("query: %w", context.Canceled)},
		"broken pipe":       {httptest.NewRequest(http.MethodGet, "/x", nil), fmt.Errorf("write: %w", syscall.EPIPE)},
	} {
		logger, buf := captureLogger()
		rec := httptest.NewRecorder()
		ServerError(rec, tc.r, logger, tc.err, "stream")
		if rec.Body.Len() != 0 || rec.Code != http.StatusOK { // recorder default: nothing written
			t.Errorf("%s: wrote %d %q", name, rec.Code, rec.Body.String())
		}
		if strings.Contains(buf.String(), "level=ERROR") {
			t.Errorf("%s: logged at ERROR: %s", name, buf.String())
		}
	}
}

// A server-side timeout while the client still waits is a real failure.
func TestServerError_ServerDeadlineIsAnError(t *testing.T) {
	logger, buf := captureLogger()
	rec := httptest.NewRecorder()
	ServerError(rec, httptest.NewRequest(http.MethodGet, "/x", nil), logger, fmt.Errorf("query: %w", context.DeadlineExceeded), "slow query")
	if rec.Code != http.StatusInternalServerError || !strings.Contains(buf.String(), "level=ERROR") {
		t.Fatalf("got %d, log %q", rec.Code, buf.String())
	}
}

func TestServerError_ReadOnlyIs503(t *testing.T) {
	logger, _ := captureLogger()
	rec := httptest.NewRecorder()
	ServerError(rec, httptest.NewRequest(http.MethodPost, "/x", nil), logger, fmt.Errorf("mark read: %w", &pgconn.PgError{Code: "25006"}), "mark read")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" ||
		!strings.Contains(rec.Body.String(), "READ_ONLY_DATABASE") {
		t.Fatalf("got %d %q (Retry-After %q)", rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"))
	}
}

func TestNotFoundOrServerError(t *testing.T) {
	errDomain := errors.New("thing not found")
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"no rows":          {fmt.Errorf("get: %w", pgx.ErrNoRows), http.StatusNotFound},
		"domain not found": {fmt.Errorf("get: %w", errDomain), http.StatusNotFound},
		"db outage":        {errors.New("connection refused"), http.StatusInternalServerError},
	} {
		logger, _ := captureLogger()
		rec := httptest.NewRecorder()
		NotFoundOrServerError(rec, httptest.NewRequest(http.MethodGet, "/x", nil), logger, tc.err, "get thing", errDomain)
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d", name, rec.Code, tc.want)
		}
	}
}
