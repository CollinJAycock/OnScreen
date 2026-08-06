package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

type stubIntegrityStore struct {
	files     []media.File
	listErr   error
	limit     int32
	libraryID *uuid.UUID
	verdicts  map[uuid.UUID]string
	details   map[uuid.UUID]*string
	setErrFor map[uuid.UUID]error
	remaining int64
}

func (s *stubIntegrityStore) ListFilesForIntegrityCheck(_ context.Context, libraryID *uuid.UUID, limit int32) ([]media.File, error) {
	s.limit = limit
	s.libraryID = libraryID
	return s.files, s.listErr
}

func (s *stubIntegrityStore) CountFilesForIntegrityCheck(_ context.Context, _ *uuid.UUID) (int64, error) {
	return s.remaining, nil
}

func (s *stubIntegrityStore) SetFileIntegrity(_ context.Context, id uuid.UUID, status string, detail *string) error {
	if err, ok := s.setErrFor[id]; ok {
		return err
	}
	if s.verdicts == nil {
		s.verdicts = map[uuid.UUID]string{}
		s.details = map[uuid.UUID]*string{}
	}
	s.verdicts[id] = status
	s.details[id] = detail
	return nil
}

func integrityFile(id uuid.UUID, path string) media.File {
	dur := int64(3_600_000)
	return media.File{ID: id, FilePath: path, DurationMS: &dur, IntegrityStatus: "unchecked"}
}

func TestIntegrityProbeHandler_NoFiles(t *testing.T) {
	h := NewIntegrityProbeHandler(&stubIntegrityStore{},
		func(context.Context, string, int64) (string, *string, error) {
			t.Fatal("probe must not run with no files")
			return "", nil, nil
		}, slog.Default())
	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out, "no files awaiting") {
		t.Errorf("output: got %q", out)
	}
}

func TestIntegrityProbeHandler_DefaultAndClampedLimit(t *testing.T) {
	store := &stubIntegrityStore{}
	h := NewIntegrityProbeHandler(store,
		func(context.Context, string, int64) (string, *string, error) { return "ok", nil, nil },
		slog.Default())
	if _, err := h.Run(context.Background(), nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if store.limit != 250 {
		t.Errorf("default limit: got %d, want 250", store.limit)
	}
	if _, err := h.Run(context.Background(), []byte(`{"limit":99999}`)); err != nil {
		t.Fatalf("err: %v", err)
	}
	if store.limit != 1000 {
		t.Errorf("clamped limit: got %d, want 1000", store.limit)
	}
}

func TestIntegrityProbeHandler_LibraryScope(t *testing.T) {
	store := &stubIntegrityStore{}
	h := NewIntegrityProbeHandler(store,
		func(context.Context, string, int64) (string, *string, error) { return "ok", nil, nil },
		slog.Default())
	lib := uuid.New()
	if _, err := h.Run(context.Background(), []byte(`{"library_id":"`+lib.String()+`"}`)); err != nil {
		t.Fatalf("err: %v", err)
	}
	if store.libraryID == nil || *store.libraryID != lib {
		t.Errorf("library scope not passed through: %v", store.libraryID)
	}
	if _, err := h.Run(context.Background(), []byte(`{"library_id":"not-a-uuid"}`)); err == nil {
		t.Error("bad library_id should error")
	}
}

func TestIntegrityProbeHandler_RecordsVerdictsAndFailsOpen(t *testing.T) {
	okID, badID, errID := uuid.New(), uuid.New(), uuid.New()
	store := &stubIntegrityStore{
		files: []media.File{
			integrityFile(okID, "/m/fine.mkv"),
			integrityFile(badID, "/m/fake.mkv"),
			integrityFile(errID, "/m/unreachable.mkv"),
		},
		remaining: 7,
	}
	detail := "spot-decode failed at 50%"
	h := NewIntegrityProbeHandler(store,
		func(_ context.Context, path string, durationMS int64) (string, *string, error) {
			if durationMS != 3_600_000 {
				t.Errorf("durationMS not passed through: %d", durationMS)
			}
			switch path {
			case "/m/fake.mkv":
				return "damaged", &detail, nil
			case "/m/unreachable.mkv":
				return "", nil, errors.New("stat source: no such file")
			}
			return "ok", nil, nil
		}, slog.Default())

	out, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if store.verdicts[okID] != "ok" {
		t.Errorf("ok file verdict: %q", store.verdicts[okID])
	}
	if store.verdicts[badID] != "damaged" || store.details[badID] == nil || *store.details[badID] != detail {
		t.Errorf("damaged file verdict: %q detail %v", store.verdicts[badID], store.details[badID])
	}
	if _, wrote := store.verdicts[errID]; wrote {
		t.Error("probe error must leave the file unchecked (no verdict write)")
	}
	for _, want := range []string{"checked=2", "ok=1", "damaged=1", "failed=1", "remaining=7"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}

func TestIntegrityProbeHandler_ListErrorBubbles(t *testing.T) {
	h := NewIntegrityProbeHandler(&stubIntegrityStore{listErr: errors.New("db down")},
		func(context.Context, string, int64) (string, *string, error) { return "ok", nil, nil },
		slog.Default())
	if _, err := h.Run(context.Background(), nil); err == nil {
		t.Fatal("expected err when list fails")
	}
}

func TestIntegrityProbeHandler_StopsOnCancelledContext(t *testing.T) {
	store := &stubIntegrityStore{files: []media.File{
		integrityFile(uuid.New(), "/m/a.mkv"),
		integrityFile(uuid.New(), "/m/b.mkv"),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	probes := 0
	h := NewIntegrityProbeHandler(store,
		func(context.Context, string, int64) (string, *string, error) {
			probes++
			cancel() // shutdown mid-run
			return "ok", nil, nil
		}, slog.Default())
	if _, err := h.Run(ctx, nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if probes != 1 {
		t.Errorf("probes after cancel: got %d, want 1", probes)
	}
}
