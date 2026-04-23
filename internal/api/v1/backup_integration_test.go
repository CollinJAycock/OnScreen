//go:build integration

// Backup/restore round-trip against a real Postgres testcontainer.
// Gated by the `integration` build tag because it needs Docker, pg_dump,
// and pg_restore on PATH.
//
// Run with: go test -tags=integration ./internal/api/v1/ -run TestBackup
package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"testing"

	"github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/testdb"
)

func skipIfMissingPGTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH: %v", tool, err)
		}
	}
}

// TestBackup_RoundTrip_Integration is the canonical "backup → wipe →
// restore" round-trip we promised admins works. Inserts a sentinel row,
// dumps, deletes the sentinel, restores, verifies the sentinel returns.
func TestBackup_RoundTrip_Integration(t *testing.T) {
	skipIfMissingPGTools(t)

	pool, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()

	const sentinelKey = "backup_roundtrip_sentinel"
	const sentinelValue = `"42"`
	if _, err := pool.Exec(ctx,
		`INSERT INTO server_settings (key, value) VALUES ($1, $2::jsonb)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		sentinelKey, sentinelValue); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	expected, err := migrations.Highest()
	if err != nil {
		t.Fatalf("migrations.Highest: %v", err)
	}
	h := NewBackupHandler(dsn, expected, migrations.FS, slog.Default())

	// 1. Download backup.
	dumpReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/backup", nil)
	dumpResp := httptest.NewRecorder()
	h.Download(dumpResp, dumpReq)
	if dumpResp.Code != http.StatusOK {
		t.Fatalf("download: status %d, body=%s", dumpResp.Code, dumpResp.Body.String())
	}
	if v := dumpResp.Header().Get("X-OnScreen-Schema-Version"); v != strconv.FormatInt(expected, 10) {
		t.Errorf("X-OnScreen-Schema-Version: got %q want %d", v, expected)
	}
	dump := dumpResp.Body.Bytes()
	if len(dump) == 0 {
		t.Fatal("download: empty body")
	}

	// 2. Wipe the sentinel.
	if _, err := pool.Exec(ctx, `DELETE FROM server_settings WHERE key = $1`, sentinelKey); err != nil {
		t.Fatalf("delete sentinel: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM server_settings WHERE key = $1`, sentinelKey).Scan(&n); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if n != 0 {
		t.Fatalf("post-delete: expected 0 rows, got %d", n)
	}

	// 3. Restore.
	body, contentType := buildMultipart(t, "backup.dump", dump)
	restReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/restore", body)
	restReq.Header.Set("Content-Type", contentType)
	restResp := httptest.NewRecorder()
	h.Restore(restResp, restReq)
	if restResp.Code != http.StatusOK {
		t.Fatalf("restore: status %d, body=%s", restResp.Code, restResp.Body.String())
	}

	var envelope struct {
		Data struct {
			ExitError     string `json:"exit_error"`
			DumpVersion   int64  `json:"dump_version"`
			ServerVersion int64  `json:"server_version"`
			Migrated      bool   `json:"migrated"`
		} `json:"data"`
	}
	if err := json.Unmarshal(restResp.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode restore response: %v (body=%s)", err, restResp.Body.String())
	}
	if envelope.Data.ExitError != "" {
		t.Errorf("pg_restore exit error: %s", envelope.Data.ExitError)
	}
	if envelope.Data.DumpVersion != expected {
		t.Errorf("dump_version: got %d want %d", envelope.Data.DumpVersion, expected)
	}
	if envelope.Data.ServerVersion != expected {
		t.Errorf("server_version: got %d want %d", envelope.Data.ServerVersion, expected)
	}
	if envelope.Data.Migrated {
		t.Error("migrated should be false when dump_version == server_version")
	}

	// 4. Verify sentinel is back. The pg_restore --clean dropped and
	// recreated the table, so the existing pgxpool prepared statements
	// for that table are invalidated. Reset connections by closing the
	// pool and opening a fresh one would be ideal, but a fresh query
	// works because pgxpool re-prepares on relation OID change.
	var got string
	if err := pool.QueryRow(ctx,
		`SELECT value::text FROM server_settings WHERE key = $1`, sentinelKey).Scan(&got); err != nil {
		t.Fatalf("read sentinel after restore: %v", err)
	}
	if got != sentinelValue {
		t.Errorf("sentinel value: got %q want %q", got, sentinelValue)
	}
}

// TestBackup_DumpNewerThanServer_Integration proves the version gate
// refuses dumps from a newer schema unless ?force=true.
func TestBackup_DumpNewerThanServer_Integration(t *testing.T) {
	skipIfMissingPGTools(t)

	pool, dsn := testdb.NewWithDSN(t)
	_ = pool

	expected, err := migrations.Highest()
	if err != nil {
		t.Fatalf("migrations.Highest: %v", err)
	}

	// Take a fresh dump at the real version.
	dumpHandler := NewBackupHandler(dsn, expected, migrations.FS, slog.Default())
	dumpReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/backup", nil)
	dumpResp := httptest.NewRecorder()
	dumpHandler.Download(dumpResp, dumpReq)
	if dumpResp.Code != http.StatusOK {
		t.Fatalf("download: status %d", dumpResp.Code)
	}
	dump := dumpResp.Body.Bytes()

	// Then construct a handler that *thinks* it expects a lower version.
	staleHandler := NewBackupHandler(dsn, expected-1, migrations.FS, slog.Default())

	body, ct := buildMultipart(t, "backup.dump", dump)
	restReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/restore", body)
	restReq.Header.Set("Content-Type", ct)
	restResp := httptest.NewRecorder()
	staleHandler.Restore(restResp, restReq)
	if restResp.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d body=%s", restResp.Code, restResp.Body.String())
	}

	// Forced retry must succeed.
	body2, ct2 := buildMultipart(t, "backup.dump", dump)
	forceReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/restore?force=true", body2)
	forceReq.Header.Set("Content-Type", ct2)
	forceResp := httptest.NewRecorder()
	staleHandler.Restore(forceResp, forceReq)
	if forceResp.Code != http.StatusOK {
		t.Fatalf("forced restore: status %d body=%s", forceResp.Code, forceResp.Body.String())
	}
}

func buildMultipart(t *testing.T, filename string, content []byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("write multipart body: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, mw.FormDataContentType()
}
