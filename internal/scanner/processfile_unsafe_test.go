package scanner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// missingRecorder records MarkMissing and PromoteExpiredMissing calls on top
// of the shared mock.
type missingRecorder struct {
	*mockMediaService
	missing []uuid.UUID
	purges  []time.Duration // grace passed to each PromoteExpiredMissing call
}

func (m *missingRecorder) MarkMissing(_ context.Context, id uuid.UUID) error {
	m.missing = append(m.missing, id)
	return nil
}

func (m *missingRecorder) PromoteExpiredMissing(_ context.Context, grace time.Duration) (int, error) {
	m.purges = append(m.purges, grace)
	return 0, nil
}

func writePlaylistAs(t *testing.T, name string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#EXTM3U\n#EXTINF:10,\nfile:///etc/passwd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// movie.mkv was indexed as real Matroska, then replaced at the same path by an
// #EXTM3U playlist. The rescan refuses to index it — but used to return
// without touching the old row, which stayed active with Container=matroska
// for playback to trust. The row must be taken out of service.
func TestProcessFile_IndexedFileSwappedForPlaylistIsMarkedMissing(t *testing.T) {
	dir, path := writePlaylistAs(t, "movie.mkv")
	svc := &missingRecorder{mockMediaService: newMockMediaService()}
	container := "matroska,webm"
	existing := &media.File{
		ID:          uuid.New(),
		MediaItemID: uuid.New(),
		FilePath:    path,
		FileSize:    123456789, // the old real file's size — the swap changed it
		Container:   &container,
		Status:      "active",
		ScannedAt:   time.Now().Add(-24 * time.Hour),
	}
	svc.fileByPath[path] = existing

	s := New(svc, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, file, _, err := s.processFile(context.Background(), uuid.New(), "movie", path, []string{dir})
	if !errors.Is(err, ErrUnsafeContainer) {
		t.Fatalf("processFile: got %v, want ErrUnsafeContainer", err)
	}
	if file != nil {
		t.Errorf("a playlist must not be (re)indexed; got file %+v", file)
	}
	if len(svc.missing) != 1 || svc.missing[0] != existing.ID {
		t.Errorf("MarkMissing calls = %v, want exactly [%s] — the stale active row would keep "+
			"its old container and be transcoded", svc.missing, existing.ID)
	}
	if len(svc.fileCalls) != 0 {
		t.Errorf("CreateOrUpdateFile called %d times for a refused playlist", len(svc.fileCalls))
	}
}

// A playlist that was never indexed has no row to retire — refuse, touch nothing.
func TestProcessFile_NewPlaylistIsRefusedWithoutSideEffects(t *testing.T) {
	dir, path := writePlaylistAs(t, "movie.mkv")
	svc := &missingRecorder{mockMediaService: newMockMediaService()}

	s := New(svc, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, _, _, err := s.processFile(context.Background(), uuid.New(), "movie", path, []string{dir})
	if !errors.Is(err, ErrUnsafeContainer) {
		t.Fatalf("processFile: got %v, want ErrUnsafeContainer", err)
	}
	if len(svc.missing) != 0 || len(svc.fileCalls) != 0 {
		t.Errorf("unexpected side effects: missing=%v fileCalls=%d", svc.missing, len(svc.fileCalls))
	}
}

// The scan that marks a swapped file missing must not also hard-delete it. The
// scan-end sweep used to delete EVERY status=missing file on every scan, scoped
// watcher scans included, so the swap skipped the grace period entirely and
// restoring the real file created a new item. Now: scoped scans never purge,
// full scans purge only past the configured grace.
func TestScan_SwappedFileIsNotPurgedWithinGrace(t *testing.T) {
	setup := func(t *testing.T) (dir string, svc *missingRecorder, existing *media.File) {
		dir, path := writePlaylistAs(t, "movie.mkv")
		svc = &missingRecorder{mockMediaService: newMockMediaService()}
		existing = &media.File{
			ID:          uuid.New(),
			MediaItemID: uuid.New(),
			FilePath:    path,
			FileSize:    123456789,
			Status:      "active",
			ScannedAt:   time.Now().Add(-24 * time.Hour),
		}
		svc.fileByPath[path] = existing
		return dir, svc, existing
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("scoped scan", func(t *testing.T) {
		dir, svc, existing := setup(t)
		s := New(svc, nil, stubConc{}, logger)
		if _, err := s.ScanDirectory(context.Background(), uuid.New(), "movie", dir, []string{dir}); err != nil {
			t.Fatalf("ScanDirectory: %v", err)
		}
		if len(svc.missing) != 1 || svc.missing[0] != existing.ID {
			t.Fatalf("MarkMissing calls = %v, want [%s]", svc.missing, existing.ID)
		}
		if len(svc.purges) != 0 {
			t.Errorf("a scoped scan purged missing files (graces %v); the swap must keep its grace", svc.purges)
		}
	})

	t.Run("full scan uses the configured grace", func(t *testing.T) {
		dir, svc, _ := setup(t)
		s := New(svc, nil, stubConc{}, logger).
			WithMissingFileGrace(func() time.Duration { return 42 * time.Minute })
		if _, err := s.ScanLibrary(context.Background(), uuid.New(), "movie", []string{dir}); err != nil {
			t.Fatalf("ScanLibrary: %v", err)
		}
		if len(svc.purges) != 1 || svc.purges[0] != 42*time.Minute {
			t.Errorf("purge graces = %v, want exactly [42m]", svc.purges)
		}
	})

	t.Run("full scan default grace", func(t *testing.T) {
		dir, svc, _ := setup(t)
		s := New(svc, nil, stubConc{}, logger)
		if _, err := s.ScanLibrary(context.Background(), uuid.New(), "movie", []string{dir}); err != nil {
			t.Fatalf("ScanLibrary: %v", err)
		}
		if len(svc.purges) != 1 || svc.purges[0] != defaultMissingFileGrace {
			t.Errorf("purge graces = %v, want exactly [%v]", svc.purges, defaultMissingFileGrace)
		}
	})
}

// VerifySource is an ffprobe; like ProbeFile it must refuse a playlist before
// ffprobe opens it (the HLS demuxer would follow the references inside).
func TestVerifySource_RefusesPlaylistWithoutProbing(t *testing.T) {
	_, path := writePlaylistAs(t, "movie.mkv")
	status, err := VerifySource(context.Background(), path)
	if status != SourceUnreadable || !errors.Is(err, ErrUnsafeContainer) {
		t.Errorf("VerifySource = (%q, %v), want (%q, ErrUnsafeContainer)", status, err, SourceUnreadable)
	}
}

func TestLooksLikeReferenceContainer_Exported(t *testing.T) {
	_, playlist := writePlaylistAs(t, "a.mkv")
	if !LooksLikeReferenceContainer(playlist) {
		t.Error("an #EXTM3U file was not recognised")
	}
	bin := filepath.Join(t.TempDir(), "b.mkv")
	if err := os.WriteFile(bin, []byte("\x1a\x45\xdf\xa3binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if LooksLikeReferenceContainer(bin) {
		t.Error("binary Matroska header flagged as a playlist")
	}
	if LooksLikeReferenceContainer(filepath.Join(t.TempDir(), "absent.mkv")) {
		t.Error("an unreadable path must report false (callers handle missing files separately)")
	}
}
