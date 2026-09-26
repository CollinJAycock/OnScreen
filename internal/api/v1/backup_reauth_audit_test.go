package v1

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

type denyReauth struct{}

func (denyReauth) VerifyReauth(context.Context, uuid.UUID, string, string) error {
	return errors.New("bad password")
}

type auditCapture struct{ ch chan gen.InsertAuditLogParams }

func (a *auditCapture) InsertAuditLog(_ context.Context, p gen.InsertAuditLogParams) error {
	a.ch <- p
	return nil
}

// fakePGRestoreOnPath puts a pg_restore that just fails first on PATH, so
// Restore gets past its tool lookup (the dump-version probe failing is only a
// warning) and reaches the step-up check without real Postgres tools.
func fakePGRestoreOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	name, body := "pg_restore", "#!/bin/sh\nexit 1\n"
	if runtime.GOOS == "windows" {
		name, body = "pg_restore.bat", "@exit /b 1\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// A restore refused at step-up must leave an audit record (like a denied
// worker-credentials reveal), not just a WARN line.
func TestBackupRestore_ReauthFailureIsAudited(t *testing.T) {
	fakePGRestoreOnPath(t)
	capture := &auditCapture{ch: make(chan gen.InsertAuditLogParams, 1)}
	h := NewBackupHandler("postgres://unused", 0, nil, slog.Default()).
		WithReauth(denyReauth{}).
		WithAudit(audit.New(capture, slog.Default()))

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "evil.dump")
	_, _ = fw.Write([]byte("not a real dump"))
	_ = mw.WriteField("password", "wrong")
	_ = mw.Close()

	uid := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/restore", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uid, IsAdmin: true}))
	rec := httptest.NewRecorder()
	h.Restore(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	select {
	case p := <-capture.ch:
		if p.Action != auditActionBackupRestoreDenied {
			t.Errorf("audit action: got %q, want %q", p.Action, auditActionBackupRestoreDenied)
		}
		if !p.UserID.Valid || uuid.UUID(p.UserID.Bytes) != uid {
			t.Errorf("audit actor: got %v, want %s", p.UserID, uid)
		}
		if p.Target == nil || *p.Target != "evil.dump" {
			t.Errorf("audit target: got %v, want evil.dump", p.Target)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no audit entry written for the refused restore")
	}
}
