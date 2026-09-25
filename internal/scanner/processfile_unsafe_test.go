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

// missingRecorder records MarkMissing calls on top of the shared mock.
type missingRecorder struct {
	*mockMediaService
	missing []uuid.UUID
}

func (m *missingRecorder) MarkMissing(_ context.Context, id uuid.UUID) error {
	m.missing = append(m.missing, id)
	return nil
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
