package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/library"
)

// mockReprober satisfies MetadataReprober.
type mockReprober struct {
	libID   *uuid.UUID
	called  bool
	cleared int64
	err     error
}

func (m *mockReprober) ClearActiveFileHashesForReprobe(_ context.Context, libID *uuid.UUID) (int64, error) {
	m.called = true
	m.libID = libID
	return m.cleared, m.err
}

// mockMaintLibSvc satisfies MaintenanceLibraryService.
type mockMaintLibSvc struct {
	mu        sync.Mutex
	libs      []library.Library
	listErr   error
	scanCalls []uuid.UUID
	scanErr   error
}

func (m *mockMaintLibSvc) List(_ context.Context) ([]library.Library, error) {
	return m.libs, m.listErr
}

func (m *mockMaintLibSvc) EnqueueScan(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	m.scanCalls = append(m.scanCalls, id)
	m.mu.Unlock()
	return m.scanErr
}

func (m *mockMaintLibSvc) scans() []uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]uuid.UUID, len(m.scanCalls))
	copy(out, m.scanCalls)
	return out
}

// ── ReprobeMetadata ──────────────────────────────────────────────────────────

func TestMaintenance_ReprobeMetadata_AllLibraries(t *testing.T) {
	libA, libB := uuid.New(), uuid.New()
	lib := &mockMaintLibSvc{libs: []library.Library{{ID: libA}, {ID: libB}}}
	rep := &mockReprober{cleared: 356}
	h := NewMaintenanceHandler(lib, rep, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/reprobe-metadata", nil)
	rec := httptest.NewRecorder()
	h.ReprobeMetadata(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if !rep.called || rep.libID != nil {
		t.Errorf("reprober: called=%v libID=%v (want called, nil scope)", rep.called, rep.libID)
	}
	scans := lib.scans()
	if len(scans) != 2 {
		t.Fatalf("expected scans for both libraries, got %v", scans)
	}
	data := decodeData(t, rec)
	if int(data.Data["files_cleared"].(float64)) != 356 {
		t.Errorf("files_cleared: got %v, want 356", data.Data["files_cleared"])
	}
	if int(data.Data["libraries_queued"].(float64)) != 2 {
		t.Errorf("libraries_queued: got %v, want 2", data.Data["libraries_queued"])
	}
}

func TestMaintenance_ReprobeMetadata_ScopesByLibrary(t *testing.T) {
	libID := uuid.New()
	lib := &mockMaintLibSvc{}
	rep := &mockReprober{cleared: 79}
	h := NewMaintenanceHandler(lib, rep, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/reprobe-metadata?library_id="+libID.String(), nil)
	rec := httptest.NewRecorder()
	h.ReprobeMetadata(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if rep.libID == nil || *rep.libID != libID {
		t.Errorf("reprober scope: got %v, want %s", rep.libID, libID)
	}
	// Scoped runs must NOT enumerate libraries — only the named one is scanned.
	scans := lib.scans()
	if len(scans) != 1 || scans[0] != libID {
		t.Errorf("expected exactly one scan of %s, got %v", libID, scans)
	}
}

func TestMaintenance_ReprobeMetadata_InvalidLibraryID(t *testing.T) {
	lib := &mockMaintLibSvc{}
	rep := &mockReprober{}
	h := NewMaintenanceHandler(lib, rep, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/reprobe-metadata?library_id=bogus", nil)
	rec := httptest.NewRecorder()
	h.ReprobeMetadata(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if rep.called {
		t.Error("must not clear hashes for an invalid library_id")
	}
}

func TestMaintenance_ReprobeMetadata_ClearError(t *testing.T) {
	lib := &mockMaintLibSvc{}
	rep := &mockReprober{err: errors.New("boom")}
	h := NewMaintenanceHandler(lib, rep, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/reprobe-metadata", nil)
	rec := httptest.NewRecorder()
	h.ReprobeMetadata(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
	if len(lib.scans()) != 0 {
		t.Error("must not enqueue scans when the hash clear failed")
	}
}
