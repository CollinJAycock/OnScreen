// Package dbtools resolves PostgreSQL client binaries (pg_dump, pg_restore,
// psql, …) the server shells out to for backup, restore, and migration
// helpers.
//
// Why a dedicated resolver instead of plain exec.LookPath:
//
// The Windows installer bundles the full pgsql/bin directory next to the
// server binary (installer/windows-msi/build.ps1 stages it; the .iss copies
// it to {app}\pgsql) but historically didn't put that directory on the
// service's PATH. A fresh install would then 503 on the admin backup
// endpoint because exec.LookPath("pg_dump") looked only at PATH and missed
// the bundled tools sitting right next to server.exe.
//
// Find() looks in <exeDir>/pgsql/bin first (the bundled location) and
// falls back to PATH. Works in production (installer bundle), dev (PATH),
// and OS-package installs (PATH) without any one of them having to know
// about the others.
package dbtools

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ErrNotFound is returned when no candidate location yields the requested
// tool. Wrap with %w if callers need errors.Is.
var ErrNotFound = errors.New("pg tool not found")

// Find returns the absolute path to a Postgres client binary, preferring
// the copy bundled next to the running executable.
//
// Search order:
//  1. <exeDir>/pgsql/bin/<name>[.exe]    — Windows installer / portable bundle
//  2. exec.LookPath(name)                — system PATH
//
// `name` should be the unsuffixed tool name (e.g. "pg_dump"); the resolver
// appends ".exe" on Windows. Returns ErrNotFound wrapping the original
// LookPath error if neither location succeeds.
func Find(name string) (string, error) {
	exe := name
	if runtime.GOOS == "windows" {
		exe = name + ".exe"
	}

	if dir, err := exeDir(); err == nil {
		candidate := filepath.Join(dir, "pgsql", "bin", exe)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate, nil
		}
	}

	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrNotFound, name, err)
	}
	return resolved, nil
}

// Available is a presence-only check, equivalent to Find(name) succeeding.
// Useful for capability probes that don't need the resolved path.
func Available(name string) bool {
	_, err := Find(name)
	return err == nil
}

// exeDir is a thin wrapper around os.Executable that returns just the
// directory. Pulled out so tests can stub the resolver's notion of "where
// the binary lives" without touching the global.
var exeDir = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// EvalSymlinks so a symlinked launcher still resolves to the real
	// install directory (Windows installers occasionally place a shim).
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}
	return filepath.Dir(resolved), nil
}
