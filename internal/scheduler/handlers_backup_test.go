package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestBackupHandlerValidatesInputs(t *testing.T) {
	ctx := context.Background()

	// No DATABASE_URL.
	h := NewBackupHandler("")
	if _, err := h.Run(ctx, json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error when DATABASE_URL empty")
	}

	// Bad JSON.
	h = NewBackupHandler("postgres://x")
	if _, err := h.Run(ctx, json.RawMessage(`{not json`)); err == nil {
		t.Fatal("expected JSON parse error")
	}

	// Missing output_dir.
	if _, err := h.Run(ctx, json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error when output_dir missing")
	}
}

func TestRotateBackupsKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"onscreen-backup-20260101-000000.dump",
		"onscreen-backup-20260102-000000.dump",
		"onscreen-backup-20260103-000000.dump",
		"onscreen-backup-20260104-000000.dump",
		"onscreen-backup-20260105-000000.dump",
		"unrelated-file.txt", // should not be touched
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	removed := rotateBackups(dir, 2)
	if removed != 3 {
		t.Fatalf("expected 3 removed, got %d", removed)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	sort.Strings(got)
	want := []string{
		"onscreen-backup-20260104-000000.dump",
		"onscreen-backup-20260105-000000.dump",
		"subdir",
		"unrelated-file.txt",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("after rotate got %v, want %v", got, want)
	}
}

func TestRotateBackupsNoOpWhenUnderRetain(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"onscreen-backup-20260101-000000.dump",
		"onscreen-backup-20260102-000000.dump",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if removed := rotateBackups(dir, 5); removed != 0 {
		t.Fatalf("expected 0 removed, got %d", removed)
	}
}

func TestRotateBackupsMissingDirReturnsZero(t *testing.T) {
	if got := rotateBackups(filepath.Join(t.TempDir(), "nope"), 3); got != 0 {
		t.Fatalf("expected 0 from missing dir, got %d", got)
	}
}

func TestRotateBackupsExactRetainCount(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"onscreen-backup-20260101-000000.dump",
		"onscreen-backup-20260102-000000.dump",
		"onscreen-backup-20260103-000000.dump",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := rotateBackups(dir, 3); got != 0 {
		t.Fatalf("exact match should remove nothing, got %d", got)
	}
}

func TestRotateBackupsIgnoresUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"onscreen-backup-20260101-000000.dump",
		"onscreen-backup-20260102-000000.dump",
		"not-a-backup.dump",            // wrong prefix
		"onscreen-backup-20260103.sql", // wrong suffix
		"onscreen-backup-xyz.dump",     // wrong prefix pattern? actually starts with the prefix
	}
	for _, n := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// retain=1 — should leave the newest onscreen-backup-*.dump file plus
	// all unrelated files untouched.
	rotateBackups(dir, 1)

	entries, _ := os.ReadDir(dir)
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	if !names["not-a-backup.dump"] || !names["onscreen-backup-20260103.sql"] {
		t.Fatalf("rotate touched unrelated files: %v", names)
	}
}

// The Backup handler should treat a nil rawCfg identically to an empty one —
// both must fail validation since output_dir is required.
func TestBackupHandlerNilConfigRequiresOutputDir(t *testing.T) {
	h := NewBackupHandler("postgres://x")
	if _, err := h.Run(context.Background(), nil); err == nil {
		t.Fatal("expected error when output_dir missing (nil cfg)")
	}
}
