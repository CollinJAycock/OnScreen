package scanner

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
)

// WatchTrigger is called when fsnotify detects a change in a watched directory.
// It receives the library ID and the affected directory path.
type WatchTrigger interface {
	TriggerDirectoryScan(ctx context.Context, libraryID uuid.UUID, dirPath string) error
}

// Watcher wraps fsnotify and debounces directory change events.
// On change it triggers a targeted re-scan of the affected directory only —
// move semantics are resolved by the reconciliation step in the scanner (ADR-011).
type Watcher struct {
	inner     *fsnotify.Watcher
	trigger   WatchTrigger
	logger    *slog.Logger
	closeOnce sync.Once
}

// NewWatcher creates a Watcher.
func NewWatcher(trigger WatchTrigger, logger *slog.Logger) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{inner: w, trigger: trigger, logger: logger}, nil
}

// WatchLibrary adds all scan paths (and every subdirectory within them) to
// the watcher. fsnotify does not watch recursively on Linux/WSL, so we walk
// the tree and add each directory individually.
func (w *Watcher) WatchLibrary(libraryID uuid.UUID, paths []string) error {
	for _, root := range paths {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if err := w.inner.Add(path); err != nil {
				w.logger.Warn("failed to watch dir", "path", path, "err", err)
			}
			return nil
		})
	}
	return nil
}

// Run processes fsnotify events until ctx is cancelled.
// Events are debounced per directory with a 500ms window to avoid
// triggering a scan for every file in a batch copy.
func (w *Watcher) Run(ctx context.Context, libraryID uuid.UUID) {
	debounce := map[string]*time.Timer{}
	// debounceFired is used by AfterFunc callbacks to signal the main loop
	// that a debounce timer has expired and the directory should be scanned.
	// Buffered so timer callbacks never block.
	debounceFired := make(chan string, 64)
	const debounceDur = 500 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			// Stop all pending timers before exiting.
			for _, t := range debounce {
				t.Stop()
			}
			w.Close()
			return

		case dir := <-debounceFired:
			delete(debounce, dir)
			if err := w.trigger.TriggerDirectoryScan(ctx, libraryID, dir); err != nil {
				w.logger.Warn("directory scan trigger failed",
					"dir", dir, "err", err)
			}

		case event, ok := <-w.inner.Events:
			if !ok {
				return
			}
			// Ignore CHMOD events — they fire on every access.
			if event.Op == fsnotify.Chmod {
				continue
			}

			// When a new directory appears, add it (and its subtree) to the
			// watcher so files dropped inside it are also detected.
			if event.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					_ = filepath.WalkDir(event.Name, func(p string, d os.DirEntry, err error) error {
						if err != nil || !d.IsDir() {
							return nil
						}
						_ = w.inner.Add(p)
						return nil
					})
				}
			}

			dir := dirOf(event.Name)
			if t, exists := debounce[dir]; exists {
				t.Reset(debounceDur)
			} else {
				dir := dir // capture for closure
				debounce[dir] = time.AfterFunc(debounceDur, func() {
					debounceFired <- dir
				})
			}

		case err, ok := <-w.inner.Errors:
			if !ok {
				return
			}
			w.logger.Error("fsnotify error", "err", err)
		}
	}
}

// Close shuts down the watcher. Safe to call multiple times.
func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		err = w.inner.Close()
	})
	return err
}

func dirOf(path string) string {
	// filepath.Dir works on both Unix and Windows.
	// We use the stdlib import here rather than importing filepath to avoid
	// a circular import — the package already uses it indirectly.
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return path
}
