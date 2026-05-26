package dbtools

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// exeForGOOS returns the binary name with the platform-correct extension,
// matching what Find() looks for. Keeps the test honest on Windows where
// "pg_dump" on disk is "pg_dump.exe".
func exeForGOOS(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// withExeDir swaps the resolver's notion of "where the binary lives" for
// the duration of a test. Returns a restore function the caller defers.
func withExeDir(t *testing.T, dir string) {
	t.Helper()
	orig := exeDir
	exeDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { exeDir = orig })
}

// writeStubBinary creates an empty (but stat-able) file at
// <root>/pgsql/bin/<name>[.exe] so the resolver finds it. We don't need
// it to actually run — Find() only stat()s the candidate.
func writeStubBinary(t *testing.T, root, name string) string {
	t.Helper()
	binDir := filepath.Join(root, "pgsql", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(binDir, exeForGOOS(name))
	if err := os.WriteFile(path, []byte{}, 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func TestFind_PrefersBundledOverPath(t *testing.T) {
	// A bundled pg_dump next to the (fake) server binary must win over
	// whatever else is on PATH — operators bundling their own pg client
	// tools with the install want THOSE used, not a system copy of a
	// different major version.
	root := t.TempDir()
	want := writeStubBinary(t, root, "pg_dump")
	withExeDir(t, root)

	got, err := Find("pg_dump")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got != want {
		t.Errorf("Find: got %q, want %q", got, want)
	}
}

func TestFind_FallsBackToPATH(t *testing.T) {
	// No bundled copy → resolver must fall back to PATH. Use `go` as the
	// stand-in tool because every dev/CI environment that runs the Go
	// tests definitely has `go` on PATH. If THAT isn't there, the test
	// environment has bigger problems than this assertion.
	root := t.TempDir() // empty: no pgsql/bin/<name>
	withExeDir(t, root)

	got, err := Find("go")
	if err != nil {
		t.Fatalf("Find(go) via PATH fallback: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("Find: PATH fallback should return absolute path; got %q", got)
	}
}

func TestFind_NotFoundReturnsWrappedErr(t *testing.T) {
	// Unbundled + un-PATH-resolvable name must error and wrap ErrNotFound
	// so callers can errors.Is it. The error message should also include
	// the binary name so logs are immediately actionable.
	root := t.TempDir()
	withExeDir(t, root)

	const name = "definitely-not-a-real-pg-tool-xyz123"
	_, err := Find(name)
	if err == nil {
		t.Fatal("expected error for missing tool")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected error to wrap ErrNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("error should mention the tool name %q, got: %v", name, err)
	}
}

func TestAvailable_MatchesFind(t *testing.T) {
	// Available is a sugar wrapper; verify both legs (bundled and missing)
	// agree with Find so capability probes don't drift from the handler.
	root := t.TempDir()
	writeStubBinary(t, root, "pg_dump")
	withExeDir(t, root)

	if !Available("pg_dump") {
		t.Error("Available should be true for bundled tool")
	}
	if Available("definitely-not-a-real-pg-tool-xyz123") {
		t.Error("Available should be false for missing tool")
	}
}

func TestFind_IgnoresBundledDir(t *testing.T) {
	// Defensive: a *directory* named pg_dump in the bundle location must
	// NOT short-circuit the resolver — fall through to PATH. (Stat would
	// succeed but the resolver should still treat dirs as "not a binary.")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pgsql", "bin", exeForGOOS("go")), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	withExeDir(t, root)

	got, err := Find("go")
	if err != nil {
		t.Fatalf("Find with dir-shadowed name fell over: %v", err)
	}
	// PATH fallback returns an absolute path that lives OUTSIDE the
	// per-test temp dir.
	if strings.HasPrefix(got, root) {
		t.Errorf("Find should have fallen through the directory shadow; got %q (under tempdir)", got)
	}
}
