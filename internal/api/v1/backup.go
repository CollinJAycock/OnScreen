package v1

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
)

// BackupHandler exposes admin-only DB backup/restore operations.
//
// Backup streams a pg_dump custom-format archive (already compressed) as
// the response body. Restore accepts a multipart upload, spools it to a
// temp file, and runs pg_restore --clean --if-exists against the live
// database — the caller is expected to have confirmed the wipe at the UI
// layer.
//
// Requires pg_dump and pg_restore on PATH. They ship in the runtime
// Docker image via the postgresql17-client Alpine package.
//
// Schema version note: a restored dump carries whatever schema the dump
// was taken at. Restoring an older dump onto a newer server reverts the
// schema; the operator must re-run migrations afterward. Gating the
// restore on schema version would require peeking into the archive and
// is deferred until a user reports real pain.
type BackupHandler struct {
	databaseURL string
	logger      *slog.Logger
	audit       *audit.Logger
}

func NewBackupHandler(databaseURL string, logger *slog.Logger) *BackupHandler {
	return &BackupHandler{databaseURL: databaseURL, logger: logger}
}

// WithAudit wires the audit logger so backup/restore actions are recorded.
func (h *BackupHandler) WithAudit(a *audit.Logger) *BackupHandler {
	h.audit = a
	return h
}

// Download handles GET /api/v1/admin/backup.
//
// Streams a pg_dump custom-format archive named
// onscreen-backup-YYYYMMDD-HHMMSS.dump. The archive is internally
// compressed, so there is no extra gzip layer.
//
// pg_dump must exist on PATH. The Docker runtime installs it via
// postgresql-client; for local dev, install the Postgres client tools.
func (h *BackupHandler) Download(w http.ResponseWriter, r *http.Request) {
	if h.databaseURL == "" {
		h.logger.Error("backup: DATABASE_URL not configured")
		respond.InternalError(w, r)
		return
	}
	if _, err := exec.LookPath("pg_dump"); err != nil {
		h.logger.Error("backup: pg_dump not on PATH", "err", err)
		respond.Error(w, r, http.StatusServiceUnavailable, "PG_DUMP_UNAVAILABLE",
			"pg_dump is not installed on the server. Install the Postgres client tools (postgresql-client) and restart.")
		return
	}

	// Buffer the dump to a temp file before sending so a mid-stream
	// pg_dump failure produces a real HTTP error instead of a truncated
	// download. The cost is one extra disk write per backup.
	tmp, err := os.CreateTemp("", "onscreen-backup-*.dump")
	if err != nil {
		h.logger.Error("backup: create temp failed", "err", err)
		respond.InternalError(w, r)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	cmd := exec.CommandContext(r.Context(), "pg_dump",
		"--format=custom",
		"--no-owner",
		"--no-acl",
		"--compress=5",
		h.databaseURL,
	)
	var stderr strings.Builder
	cmd.Stdout = tmp
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if cerr := tmp.Close(); cerr != nil && runErr == nil {
		runErr = cerr
	}
	if runErr != nil {
		h.logger.Error("pg_dump failed",
			"err", runErr,
			"stderr", strings.TrimSpace(stderr.String()),
		)
		respond.Error(w, r, http.StatusInternalServerError, "PG_DUMP_FAILED",
			"pg_dump failed: "+strings.TrimSpace(stderr.String()))
		return
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		h.logger.Error("backup: stat temp failed", "err", err)
		respond.InternalError(w, r)
		return
	}

	filename := "onscreen-backup-" + time.Now().UTC().Format("20060102-150405") + ".dump"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))

	f, err := os.Open(tmpPath)
	if err != nil {
		h.logger.Error("backup: open temp failed", "err", err)
		respond.InternalError(w, r)
		return
	}
	defer f.Close()
	if _, err := io.Copy(w, f); err != nil {
		h.logger.Warn("backup: copy to client failed", "err", err)
		return
	}
	h.logger.Info("backup downloaded", "filename", filename, "bytes", info.Size())

	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
			actor := claims.UserID
			h.audit.Log(r.Context(), &actor, audit.ActionBackupDownload, filename,
				map[string]any{"bytes": info.Size()}, audit.ClientIP(r))
		}
	}
}

// Restore handles POST /api/v1/admin/restore.
//
// Accepts a multipart form with field "file" containing a pg_dump
// custom-format archive. The upload is spooled to a temp file, then
// pg_restore --clean --if-exists is run against the live database.
//
// pg_restore exits non-zero on non-fatal errors (e.g. dropping objects
// that do not exist). The handler returns both the exit error and the
// captured stderr so the operator can judge whether the restore
// succeeded materially.
func (h *BackupHandler) Restore(w http.ResponseWriter, r *http.Request) {
	if h.databaseURL == "" {
		h.logger.Error("restore: DATABASE_URL not configured")
		respond.InternalError(w, r)
		return
	}
	if _, err := exec.LookPath("pg_restore"); err != nil {
		h.logger.Error("restore: pg_restore not on PATH", "err", err)
		respond.Error(w, r, http.StatusServiceUnavailable, "PG_RESTORE_UNAVAILABLE",
			"pg_restore is not installed on the server. Install the Postgres client tools (postgresql-client) and restart.")
		return
	}

	// 2 GiB upload cap — a custom-format dump of a huge library is still
	// normally well under this.
	const maxUpload = int64(2) << 30
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		respond.BadRequest(w, r, "invalid upload")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		respond.BadRequest(w, r, "missing file field")
		return
	}
	defer file.Close()

	tmp, err := os.CreateTemp("", "onscreen-restore-*.dump")
	if err != nil {
		h.logger.Error("restore: create temp failed", "err", err)
		respond.InternalError(w, r)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, file); err != nil {
		_ = tmp.Close()
		h.logger.Error("restore: copy upload failed", "err", err)
		respond.InternalError(w, r)
		return
	}
	if err := tmp.Close(); err != nil {
		h.logger.Error("restore: close temp failed", "err", err)
		respond.InternalError(w, r)
		return
	}

	cmd := exec.CommandContext(r.Context(), "pg_restore",
		"--clean",
		"--if-exists",
		"--no-owner",
		"--no-acl",
		"--dbname", h.databaseURL,
		tmpPath,
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	h.logger.Info("restore complete",
		"filename", hdr.Filename,
		"size", hdr.Size,
		"exit_err", runErr,
		"stderr_bytes", stderr.Len(),
	)

	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
			actor := claims.UserID
			h.audit.Log(r.Context(), &actor, audit.ActionBackupRestore, hdr.Filename,
				map[string]any{"size": hdr.Size, "exit_error": errString(runErr)},
				audit.ClientIP(r))
		}
	}

	respond.Success(w, r, map[string]any{
		"filename":   hdr.Filename,
		"size":       hdr.Size,
		"exit_error": errString(runErr),
		"stderr":     stderr.String(),
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
