package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/onscreen/onscreen/internal/dbtools"
)

// BackupConfig is the JSON payload for the backup_database task.
//
// OutputDir is the directory on the server where the dump is written.
// If empty, falls back to os.TempDir() which is almost never what an
// operator wants — the API handler enforces a non-empty value at create
// time, but we still defend here.
type BackupConfig struct {
	OutputDir string `json:"output_dir"`
	// RetainCount is how many most-recent dumps to keep. Zero disables
	// rotation (every run accumulates a new file).
	RetainCount int `json:"retain_count"`
}

// BackupHandler runs pg_dump in custom format to OutputDir, then rotates
// older dumps to the RetainCount. Uses the same pg_dump binary as the
// admin backup API (installed via postgresql-client in the runtime image).
type BackupHandler struct {
	databaseURL string
}

// NewBackupHandler constructs a BackupHandler bound to a DATABASE_URL.
func NewBackupHandler(databaseURL string) *BackupHandler {
	return &BackupHandler{databaseURL: databaseURL}
}

// Run dumps the database to OutputDir/onscreen-backup-<timestamp>.dump and
// prunes older dumps to RetainCount.
func (h *BackupHandler) Run(ctx context.Context, rawCfg json.RawMessage) (string, error) {
	if h.databaseURL == "" {
		return "", fmt.Errorf("DATABASE_URL not configured")
	}
	var cfg BackupConfig
	if len(rawCfg) > 0 {
		if err := json.Unmarshal(rawCfg, &cfg); err != nil {
			return "", fmt.Errorf("parse config: %w", err)
		}
	}
	if cfg.OutputDir == "" {
		return "", fmt.Errorf("output_dir is required")
	}
	pgDump, err := dbtools.Find("pg_dump")
	if err != nil {
		return "", fmt.Errorf("pg_dump not found (looked in <exeDir>/pgsql/bin then PATH): %w", err)
	}
	outDir, err := resolveBackupDir(cfg.OutputDir)
	if err != nil {
		return "", err
	}
	cfg.OutputDir = outDir
	// Owner-only: a dump is the whole database (password hashes, encrypted
	// secrets, every user's history). 0755/0644 left it readable by every
	// local account on the host.
	if err := os.MkdirAll(cfg.OutputDir, 0o700); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}

	filename := "onscreen-backup-" + time.Now().UTC().Format("20060102-150405") + ".dump"
	dst := filepath.Join(cfg.OutputDir, filename)

	// Pre-create the dump owner-only; pg_dump -f truncates and keeps the mode.
	if f, ferr := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); ferr == nil {
		_ = f.Close()
	} else {
		return "", fmt.Errorf("create dump file: %w", ferr)
	}
	// Password goes in PGPASSWORD, not argv (visible in the process table).
	dsn, dsnEnv := dbtools.CommandDSN(h.databaseURL)
	cmd := exec.CommandContext(ctx, pgDump,
		"--format=custom",
		"--no-owner",
		"--no-acl",
		"--compress=5",
		"--file="+dst,
		dsn,
	)
	cmd.Env = dsnEnv
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(dst) // don't leave a partial file behind
		msg := strings.ReplaceAll(strings.TrimSpace(stderr.String()), h.databaseURL, "<DATABASE_URL>")
		msg = strings.ReplaceAll(msg, dsn, "<DATABASE_URL>")
		return "", fmt.Errorf("pg_dump failed: %w: %s", err, msg)
	}
	_ = os.Chmod(dst, 0o600) // belt and braces on platforms that ignore the pre-create mode

	info, err := os.Stat(dst)
	if err != nil {
		return "", fmt.Errorf("stat output: %w", err)
	}
	pruned := 0
	if cfg.RetainCount > 0 {
		pruned = rotateBackups(cfg.OutputDir, cfg.RetainCount)
	}

	return fmt.Sprintf("wrote %s (%d bytes), pruned %d older dumps", filename, info.Size(), pruned), nil
}

// rotateBackups keeps the newest retain dumps in dir (by filename, which
// embeds a sortable UTC timestamp) and removes the rest. Returns the
// count removed. Errors are swallowed — a failed prune doesn't fail the
// whole backup.
func rotateBackups(dir string, retain int) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var dumps []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "onscreen-backup-") && strings.HasSuffix(name, ".dump") {
			dumps = append(dumps, name)
		}
	}
	if len(dumps) <= retain {
		return 0
	}
	// Filenames sort lexicographically in timestamp order (YYYYMMDD-HHMMSS).
	// Newest is last. Remove from the front until only retain remain.
	// Using a manual sort to avoid another import; the standard library
	// slices.Sort would do but entries is already in directory order which
	// is not guaranteed to be lexicographic on every FS.
	for i := 0; i < len(dumps)-1; i++ {
		for j := i + 1; j < len(dumps); j++ {
			if dumps[i] > dumps[j] {
				dumps[i], dumps[j] = dumps[j], dumps[i]
			}
		}
	}
	toRemove := dumps[:len(dumps)-retain]
	removed := 0
	for _, name := range toRemove {
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			removed++
		}
	}
	return removed
}

// resolveBackupDir accepts an absolute or rooted output_dir and returns it
// absolute. A rooted path without a drive ("/var/backups/onscreen", the task
// form's default) is not IsAbs on Windows but has always meant "on the current
// drive" there, so it is resolved rather than refused — refusing broke every
// existing Windows backup task. A relative path (resolved against whatever the
// server's working directory happens to be) is refused.
func resolveBackupDir(dir string) (string, error) {
	if !filepath.IsAbs(dir) && !strings.HasPrefix(dir, "/") && !strings.HasPrefix(dir, `\`) {
		return "", fmt.Errorf("output_dir must be an absolute path")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve output_dir: %w", err)
	}
	return abs, nil
}
