package trickplay

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// fakeStore records calls so tests can assert the state machine without a DB.
type fakeStore struct {
	pending  int
	done     int
	failed   int
	lastErr  string
	sprites  int
	spec     Spec
	fileID   uuid.UUID
}

func (f *fakeStore) UpsertPending(_ context.Context, _, fileID uuid.UUID, spec Spec) error {
	f.pending++
	f.fileID = fileID
	f.spec = spec
	return nil
}
func (f *fakeStore) MarkDone(_ context.Context, _ uuid.UUID, spriteCount int) error {
	f.done++
	f.sprites = spriteCount
	return nil
}
func (f *fakeStore) MarkFailed(_ context.Context, _ uuid.UUID, reason string) error {
	f.failed++
	f.lastErr = reason
	return nil
}

type fakeLookup struct {
	path     string
	fileID   uuid.UUID
	duration int
	err      error
}

func (f fakeLookup) PrimaryFile(_ context.Context, _ uuid.UUID) (string, uuid.UUID, int, error) {
	return f.path, f.fileID, f.duration, f.err
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGenerateRejectsMissingFile(t *testing.T) {
	store := &fakeStore{}
	g := New(t.TempDir(), store, fakeLookup{path: ""}, silentLogger())
	err := g.Generate(context.Background(), uuid.New())
	if err != ErrNoFile {
		t.Fatalf("expected ErrNoFile, got %v", err)
	}
	if store.pending != 0 {
		t.Errorf("should not mark pending when there's no file")
	}
}

func TestGenerateRejectsZeroDuration(t *testing.T) {
	store := &fakeStore{}
	g := New(t.TempDir(), store, fakeLookup{path: "/tmp/x.mkv", duration: 0}, silentLogger())
	if err := g.Generate(context.Background(), uuid.New()); err == nil {
		t.Fatal("expected error for zero duration")
	}
	if store.pending != 0 {
		t.Errorf("zero duration should short-circuit before marking pending")
	}
}

func TestGenerateMarksFailedWhenFfmpegMissing(t *testing.T) {
	// Point the generator at a non-existent input — ffmpeg will return an
	// error which should translate to MarkFailed, not MarkDone.
	store := &fakeStore{}
	root := t.TempDir()
	g := New(root, store, fakeLookup{
		path:     filepath.Join(root, "does-not-exist.mkv"),
		fileID:   uuid.New(),
		duration: 120,
	}, silentLogger())

	itemID := uuid.New()
	err := g.Generate(context.Background(), itemID)
	if err == nil {
		t.Fatal("expected ffmpeg failure")
	}
	if store.pending != 1 {
		t.Errorf("pending should be set before ffmpeg runs: %+v", store)
	}
	if store.failed != 1 {
		t.Errorf("failed should be marked on ffmpeg error: %+v", store)
	}
	if store.done != 0 {
		t.Errorf("done should not be set on failure: %+v", store)
	}
	if store.lastErr == "" {
		t.Error("failure reason should be captured")
	}
}

func TestItemDirUsesItemID(t *testing.T) {
	g := New("/var/cache/tp", &fakeStore{}, fakeLookup{}, silentLogger())
	id := uuid.New()
	got := g.ItemDir(id)
	want := filepath.Join("/var/cache/tp", id.String())
	if got != want {
		t.Errorf("ItemDir = %q, want %q", got, want)
	}
}

func TestWithSpecOverridesGeneratorSpec(t *testing.T) {
	g := New("/var/cache/tp", &fakeStore{}, fakeLookup{}, silentLogger())
	custom := Spec{IntervalSec: 5, ThumbWidth: 160, ThumbHeight: 90, GridCols: 8, GridRows: 8}
	g2 := g.WithSpec(custom)
	if g2 == g {
		t.Fatal("WithSpec should return a clone, not mutate the receiver")
	}
	if g.spec != Default {
		t.Errorf("original spec mutated: got %+v", g.spec)
	}
	if g2.spec != custom {
		t.Errorf("clone spec wrong: got %+v want %+v", g2.spec, custom)
	}
}

func TestGeneratePropagatesLookupError(t *testing.T) {
	store := &fakeStore{}
	g := New(t.TempDir(), store, fakeLookup{err: errors.New("db down")}, silentLogger())
	err := g.Generate(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error from lookup failure")
	}
	if store.pending != 0 || store.failed != 0 {
		t.Errorf("lookup error must not touch the status row: %+v", store)
	}
}

// Sanity: regenerations wipe the prior output directory so stale sprites
// don't leak after a spec change.
func TestGenerateClearsPriorOutput(t *testing.T) {
	store := &fakeStore{}
	root := t.TempDir()
	itemID := uuid.New()
	priorDir := filepath.Join(root, itemID.String())
	if err := os.MkdirAll(priorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(priorDir, "sprite_stale.jpg")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := New(root, store, fakeLookup{
		path:     filepath.Join(root, "missing.mkv"),
		duration: 120,
	}, silentLogger())
	_ = g.Generate(context.Background(), itemID)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("prior sprite should have been cleared; stat err=%v", err)
	}
}
