//go:build integration

package downloads

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func waitOptimized(t *testing.T, svc *OptimizeService, id uuid.UUID, want string) OptimizedVersion {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		v, err := svc.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if v.Status == want {
			return v
		}
		if v.Status == StatusFailed && want != StatusFailed {
			t.Fatalf("optimize failed: %s", v.Error)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("optimized version %s never reached %s", id, want)
	return OptimizedVersion{}
}

// TestOptimize_Lifecycle_Integration: an optimized version encodes through
// the download runner into its own directory, is never evicted by the
// download cleanup, stops being offered once its source is replaced in place
// and is then deleted by the cleanup, and an admin delete removes it.
func TestOptimize_Lifecycle_Integration(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dlRoot, optRoot := t.TempDir(), t.TempDir()

	if _, err := f.pool.Exec(ctx, `UPDATE media_files SET file_hash = 'h1' WHERE id = $1`, f.file); err != nil {
		t.Fatal(err)
	}
	src := f.media.files[f.file]
	h1 := "h1"
	src.FileHash = &h1
	f.media.files[f.file] = src

	runner := NewRunner(f.pool, f.media, RunnerConfig{Root: dlRoot, OptimizedRoot: optRoot, TTL: time.Hour}, nil)
	svc := NewOptimizeService(f.pool, f.media, optRoot, nil).WithRunner(runner)
	go func() { _ = runner.Run(ctx) }()

	// The selection expands the movie to itself.
	targets, truncated, err := svc.Targets(ctx, Selection{ItemIDs: []uuid.UUID{f.item}})
	if err != nil || truncated || len(targets) != 1 || targets[0] != f.item {
		t.Fatalf("targets = %v truncated=%v err=%v", targets, truncated, err)
	}
	if lib, _, err := svc.Targets(ctx, Selection{LibraryID: libraryOf(t, f)}); err != nil || len(lib) != 1 {
		t.Fatalf("library targets = %v err=%v", lib, err)
	}

	out, _, err := svc.RequestItem(ctx, f.alice, f.item, OptimizeMobile)
	if err != nil || out != OptimizeQueued {
		t.Fatalf("request: %s %v", out, err)
	}
	if out, _, _ := svc.RequestItem(ctx, f.alice, f.item, OptimizeMobile); out != OptimizeExisting {
		t.Fatalf("repeat request = %s, want existing", out)
	}
	svc.Wake()
	list, counts, err := svc.List(ctx, ListFilter{ItemID: &f.item})
	// The runner may move it on between the list and the counts query.
	if err != nil || len(list) != 1 || counts[StatusQueued]+counts[StatusRunning]+counts[StatusReady] != 1 {
		t.Fatalf("list = %+v counts %v err %v", list, counts, err)
	}
	ready := waitOptimized(t, svc, list[0].ID, StatusReady)
	if ready.Width != 1280 || ready.Height != 720 || ready.BitrateKbps <= 0 || ready.DurationMS != 3000 ||
		ready.ItemTitle != "Test Movie" || ready.RequestedBy == nil || *ready.RequestedBy != f.alice {
		t.Fatalf("ready version = %+v", ready)
	}
	var expires *time.Time
	if err := f.pool.QueryRow(ctx, `SELECT expires_at FROM download_conversions WHERE id = $1`, ready.ID).Scan(&expires); err != nil || expires != nil {
		t.Fatalf("optimized version expires_at = %v (err %v), want NULL", expires, err)
	}
	path := filepath.Join(optRoot, ready.ID.String()+".mp4")
	probe, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name", "-of", "csv=p=0", path).Output()
	if err != nil || !strings.Contains(string(probe), "h264") || !strings.Contains(string(probe), "aac") {
		t.Fatalf("output streams %q err %v", probe, err)
	}
	if _, err := os.Stat(filepath.Join(dlRoot, ready.ID.String()+".mp4")); !os.IsNotExist(err) {
		t.Fatal("optimized version written into the download cache")
	}

	playable, err := svc.PlayableForItem(ctx, f.item)
	if err != nil || len(playable) != 1 {
		t.Fatalf("playable = %v err %v", playable, err)
	}
	if _, p, err := svc.PlayableFile(ctx, ready.ID); err != nil || p != path {
		t.Fatalf("playable file = %q err %v", p, err)
	}

	// The download cleanup never evicts it, whatever the size limit.
	cleaner := NewCleaner(f.pool, dlRoot, 1, nil).WithOptimized(optRoot)
	res, err := cleaner.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Evicted != 0 || res.Expired != 0 || res.Stale != 0 {
		t.Fatalf("cleanup touched the optimized version: %s", res)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("optimized file gone after cleanup: %v", err)
	}

	// An orphan in the directory (its row cascaded away) is removed.
	orphan := filepath.Join(optRoot, uuid.New().String()+".mp4")
	if err := os.WriteFile(orphan, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(orphan, old, old)

	// The source is replaced in place: no longer offered, then deleted.
	if _, err := f.pool.Exec(ctx, `UPDATE media_files SET file_hash = 'h2' WHERE id = $1`, f.file); err != nil {
		t.Fatal(err)
	}
	if playable, _ := svc.PlayableForItem(ctx, f.item); len(playable) != 0 {
		t.Fatalf("a version of a replaced source is still offered: %+v", playable)
	}
	res, err = cleaner.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stale != 1 || res.Orphans != 1 {
		t.Fatalf("cleanup = %s, want 1 stale + 1 orphan", res)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("stale version's file still on disk")
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan still on disk")
	}
	if _, err := svc.Get(ctx, ready.ID); !errors.Is(err, ErrOptimizedNotFound) {
		t.Fatalf("stale version row: %v", err)
	}

	// Re-request against the new source, then the admin deletes it.
	h2 := "h2"
	src.FileHash = &h2
	f.media.files[f.file] = src
	if out, _, err := svc.RequestItem(ctx, f.alice, f.item, OptimizeTV); err != nil || out != OptimizeQueued {
		t.Fatalf("re-request: %s %v", out, err)
	}
	list, _, _ = svc.List(ctx, ListFilter{ItemID: &f.item})
	if len(list) != 1 {
		t.Fatalf("list after re-request = %d", len(list))
	}
	if _, err := svc.Delete(ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, list[0].ID); !errors.Is(err, ErrOptimizedNotFound) {
		t.Fatalf("deleted version: %v", err)
	}
	if _, err := svc.Delete(ctx, list[0].ID); !errors.Is(err, ErrOptimizedNotFound) {
		t.Fatalf("second delete: %v", err)
	}

	// The schema keeps each purpose to its own quality vocabulary.
	if _, err := f.pool.Exec(ctx, `INSERT INTO download_conversions (media_item_id, media_file_id, quality, purpose) VALUES ($1, $2, '720p_4mbps', 'optimize')`, f.item, f.file); err == nil {
		t.Fatal("an optimize row with a download quality was accepted")
	}
}

// TestOptimize_DeletedSourceCascades_Integration: deleting the source file
// row takes its optimized versions with it.
func TestOptimize_DeletedSourceCascades_Integration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	svc := NewOptimizeService(f.pool, f.media, t.TempDir(), nil) // no runner: stays queued
	if out, _, err := svc.RequestItem(ctx, f.alice, f.item, OptimizeOriginal); err != nil || out != OptimizeQueued {
		t.Fatalf("request: %s %v", out, err)
	}
	if _, err := f.pool.Exec(ctx, `DELETE FROM media_files WHERE id = $1`, f.file); err != nil {
		t.Fatal(err)
	}
	list, _, err := svc.List(ctx, ListFilter{})
	if err != nil || len(list) != 0 {
		t.Fatalf("versions after the source was deleted: %+v err %v", list, err)
	}
}

func libraryOf(t *testing.T, f fixture) *uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT library_id FROM media_items WHERE id = $1`, f.item).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return &id
}
