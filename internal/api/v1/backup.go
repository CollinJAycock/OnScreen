package v1

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib" // goose driver for post-restore migrate-up

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
)

// BackupHandler exposes admin-only DB backup/restore operations.
//
// Backup streams a pg_dump custom-format archive (already compressed) as
// the response body. Restore accepts a multipart upload, spools it to a
// temp file, peeks at the embedded goose_db_version table to determine
// the schema version captured in the dump, and gates pg_restore on that
// version:
//
//   - dump version > server version: refuse with 409 (the dump references
//     schema this binary doesn't know about; restoring would leave the
//     server unable to read the data). Operator can pass ?force=true to
//     attempt anyway.
//   - dump version == server version: restore proceeds normally.
//   - dump version  < server version: restore proceeds, then the handler
//     runs `goose up` against the restored database so the schema ends up
//     matching this binary's expectations. The operator does not have to
//     remember a follow-up step.
//
// Requires pg_dump and pg_restore on PATH. They ship in the runtime
// Docker image via the postgresql17-client Alpine package.
type BackupHandler struct {
	databaseURL     string
	logger          *slog.Logger
	audit           *audit.Logger
	expectedVersion int64
	migFS           fs.FS // used by goose to bring an older restored dump forward
}

// NewBackupHandler builds a handler bound to a database URL and the
// migrations FS that defines what schema version this binary expects.
// expectedVersion is precomputed (HighestVersion of migFS) so the handler
// doesn't re-scan on every request and can fail loudly at startup if the
// FS is malformed.
func NewBackupHandler(databaseURL string, expectedVersion int64, migFS fs.FS, logger *slog.Logger) *BackupHandler {
	return &BackupHandler{
		databaseURL:     databaseURL,
		logger:          logger,
		expectedVersion: expectedVersion,
		migFS:           migFS,
	}
}

// WithAudit wires the audit logger so backup/restore actions are recorded.
func (h *BackupHandler) WithAudit(a *audit.Logger) *BackupHandler {
	h.audit = a
	return h
}

// Download handles GET /api/v1/admin/backup.
//
// Streams a pg_dump custom-format archive named
// onscreen-backup-YYYYMMDD-HHMMSS-vN.dump where N is the current schema
// version. The archive is internally compressed, so there is no extra
// gzip layer. The schema version is also surfaced via the
// X-OnScreen-Schema-Version header so the UI can label the file without
// re-parsing the filename.
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
			"pg_dump failed: "+h.scrubDSN(strings.TrimSpace(stderr.String())))
		return
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		h.logger.Error("backup: stat temp failed", "err", err)
		respond.InternalError(w, r)
		return
	}

	filename := fmt.Sprintf("onscreen-backup-%s-v%d.dump",
		time.Now().UTC().Format("20060102-150405"), h.expectedVersion)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-OnScreen-Schema-Version", strconv.FormatInt(h.expectedVersion, 10))

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
	h.logger.Info("backup downloaded", "filename", filename, "bytes", info.Size(), "schema_version", h.expectedVersion)

	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
			actor := claims.UserID
			h.audit.Log(r.Context(), &actor, audit.ActionBackupDownload, filename,
				map[string]any{"bytes": info.Size(), "schema_version": h.expectedVersion}, audit.ClientIP(r))
		}
	}
}

// Restore handles POST /api/v1/admin/restore.
//
// Accepts a multipart form with field "file" containing a pg_dump
// custom-format archive. The upload is spooled to a temp file. The
// handler reads the dump's goose_db_version to detect schema version,
// gates the restore on it (see type doc), runs pg_restore --clean
// --if-exists, and — if the dump was older than this binary expects —
// follows up with `goose up` to bring the schema forward.
//
// Query params:
//
//	?force=true — bypass the "dump newer than server" refusal.
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

	dumpVersion, vErr := extractDumpVersion(r.Context(), tmpPath)
	if vErr != nil {
		// An unreadable goose_db_version isn't itself a fatal error —
		// dumps from very old installs may predate the table or have
		// it empty. Log and treat as "unknown."
		h.logger.Warn("restore: could not detect dump schema version",
			"err", vErr, "filename", hdr.Filename)
	}

	force := r.URL.Query().Get("force") == "true"
	if dumpVersion > 0 && h.expectedVersion > 0 && dumpVersion > h.expectedVersion && !force {
		respond.Error(w, r, http.StatusConflict, "DUMP_NEWER_THAN_SERVER",
			fmt.Sprintf("dump is from a newer server (schema v%d) than this binary expects (v%d). "+
				"Upgrade the server before restoring, or retry with ?force=true to attempt anyway.",
				dumpVersion, h.expectedVersion))
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

	// If the dump was older than what this binary expects, bring the schema
	// forward so the running server doesn't immediately 500 on missing
	// columns. We do this even when pg_restore returned a non-fatal error,
	// since the data restore likely still landed and goose up is idempotent.
	var migrateErr error
	migrated := false
	if dumpVersion > 0 && h.expectedVersion > 0 && dumpVersion < h.expectedVersion && h.migFS != nil {
		if err := runGooseUp(r.Context(), h.databaseURL, h.migFS); err != nil {
			migrateErr = err
			h.logger.Error("restore: goose up after restore failed",
				"err", err, "from_version", dumpVersion, "to_version", h.expectedVersion)
		} else {
			migrated = true
			h.logger.Info("restore: schema migrated forward",
				"from_version", dumpVersion, "to_version", h.expectedVersion)
		}
	}

	h.logger.Info("restore complete",
		"filename", hdr.Filename,
		"size", hdr.Size,
		"exit_err", runErr,
		"stderr_bytes", stderr.Len(),
		"dump_version", dumpVersion,
		"server_version", h.expectedVersion,
		"migrated", migrated,
	)

	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
			actor := claims.UserID
			h.audit.Log(r.Context(), &actor, audit.ActionBackupRestore, hdr.Filename,
				map[string]any{
					"size":           hdr.Size,
					"exit_error":     errString(runErr),
					"dump_version":   dumpVersion,
					"server_version": h.expectedVersion,
					"migrated":       migrated,
					"forced":         force,
				},
				audit.ClientIP(r))
		}
	}

	respond.Success(w, r, map[string]any{
		"filename":       hdr.Filename,
		"size":           hdr.Size,
		"exit_error":     h.scrubDSN(errString(runErr)),
		"stderr":         h.scrubDSN(stderr.String()),
		"dump_version":   dumpVersion,
		"server_version": h.expectedVersion,
		"migrated":       migrated,
		"migrate_error":  h.scrubDSN(errString(migrateErr)),
		"forced":         force,
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// scrubDSN replaces every occurrence of the DATABASE_URL with a placeholder.
// pg_dump / pg_restore echo the connection string in error messages, and we
// stream stderr back to the admin client over JSON. The admin can already
// read the env var, but rendering the password into the browser response
// (and from there into client logs / error trackers / screenshots) is an
// avoidable disclosure.
func (h *BackupHandler) scrubDSN(s string) string {
	if h.databaseURL == "" {
		return s
	}
	return strings.ReplaceAll(s, h.databaseURL, "<DATABASE_URL>")
}

// extractDumpVersion runs pg_restore in data-only mode against the archive
// to dump just the goose_db_version table as SQL, then parses out the
// highest applied version_id. Returns 0 with an error if the table is
// missing or no rows are present.
func extractDumpVersion(ctx context.Context, archivePath string) (int64, error) {
	cmd := exec.CommandContext(ctx, "pg_restore",
		"--data-only",
		"--table=goose_db_version",
		"--file=-",
		archivePath,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("pg_restore --data-only goose_db_version: %w (stderr: %s)",
			err, strings.TrimSpace(stderr.String()))
	}
	return parseGooseVersion(stdout.Bytes())
}

// parseGooseVersion finds the COPY block for goose_db_version in the
// pg_restore --data-only output and returns the highest version_id where
// is_applied is true. Format reference (column order is fixed by goose):
//
//	COPY public.goose_db_version (id, version_id, is_applied, tstamp) FROM stdin;
//	1	0	t	2026-01-01 00:00:00
//	2	1	t	2026-01-01 00:00:01
//	\.
func parseGooseVersion(out []byte) (int64, error) {
	scanner := bufio.NewScanner(bytes.NewReader(out))
	// goose_db_version rows are short; the default 64KiB buffer is plenty.
	inCopy := false
	var max int64
	var rows int
	for scanner.Scan() {
		line := scanner.Text()
		if !inCopy {
			if strings.HasPrefix(line, "COPY public.goose_db_version") ||
				strings.HasPrefix(line, "COPY goose_db_version") {
				inCopy = true
			}
			continue
		}
		if line == `\.` {
			break
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		// fields[1] = version_id, fields[2] = is_applied (t/f)
		if fields[2] != "t" {
			continue
		}
		v, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		rows++
		if v > max {
			max = v
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if !inCopy {
		return 0, errors.New("goose_db_version COPY block not found in dump")
	}
	if rows == 0 {
		return 0, errors.New("goose_db_version present but no applied rows")
	}
	return max, nil
}

// runGooseUp opens a one-shot stdlib connection and runs goose up against
// the embedded migrations. We can't use the existing pgxpool because this
// runs after a destructive --clean restore that may have invalidated the
// pool's prepared statements; a fresh connection is the safe choice.
func runGooseUp(ctx context.Context, dsn string, migFS fs.FS) error {
	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open db for migrate: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
