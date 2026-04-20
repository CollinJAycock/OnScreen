package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// ── mocks ────────────────────────────────────────────────────────────────────

type mockMaintSvc struct {
	listItems    []media.Item
	listErr      error
	listLimit    int32
	listCalled   bool
	dedupeRes    media.DedupeResult
	dedupeErr    error
	dedupeType   string
	dedupeLibID  *uuid.UUID
	dedupeCalled bool
}

func (m *mockMaintSvc) ListItemsMissingArt(_ context.Context, limit int32) ([]media.Item, error) {
	m.listCalled = true
	m.listLimit = limit
	return m.listItems, m.listErr
}
func (m *mockMaintSvc) DedupeTopLevelItems(_ context.Context, itemType string, libID *uuid.UUID) (media.DedupeResult, error) {
	m.dedupeCalled = true
	m.dedupeType = itemType
	m.dedupeLibID = libID
	return m.dedupeRes, m.dedupeErr
}

type maintEnricher struct {
	err       error
	errForIDs map[uuid.UUID]error
	calledIDs []uuid.UUID
}

func (m *maintEnricher) EnrichItem(_ context.Context, id uuid.UUID) error {
	m.calledIDs = append(m.calledIDs, id)
	if m.errForIDs != nil {
		if e, ok := m.errForIDs[id]; ok {
			return e
		}
	}
	return m.err
}
func (m *maintEnricher) MatchItem(_ context.Context, _ uuid.UUID, _ int) error { return nil }

// ── RefreshMissingArt ────────────────────────────────────────────────────────

func TestMaintenance_RefreshMissingArt_Defaults(t *testing.T) {
	svc := &mockMaintSvc{}
	enr := &maintEnricher{}
	h := NewMaintenanceHandler(svc, enr, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/refresh-missing-art", nil)
	rec := httptest.NewRecorder()
	h.RefreshMissingArt(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if svc.listLimit != 200 {
		t.Errorf("default limit: got %d, want 200", svc.listLimit)
	}
}

func TestMaintenance_RefreshMissingArt_ClampsTo1000(t *testing.T) {
	svc := &mockMaintSvc{}
	enr := &maintEnricher{}
	h := NewMaintenanceHandler(svc, enr, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/refresh-missing-art?limit=999999", nil)
	rec := httptest.NewRecorder()
	h.RefreshMissingArt(rec, req)

	if svc.listLimit != 1000 {
		t.Errorf("limit should clamp to 1000, got %d", svc.listLimit)
	}
}

func TestMaintenance_RefreshMissingArt_CountsSuccessAndFailure(t *testing.T) {
	good1 := uuid.New()
	good2 := uuid.New()
	bad := uuid.New()
	svc := &mockMaintSvc{listItems: []media.Item{
		{ID: good1, Title: "Good 1"},
		{ID: bad, Title: "Bad"},
		{ID: good2, Title: "Good 2"},
	}}
	enr := &maintEnricher{errForIDs: map[uuid.UUID]error{bad: errors.New("tmdb down")}}
	h := NewMaintenanceHandler(svc, enr, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/refresh-missing-art", nil)
	rec := httptest.NewRecorder()
	h.RefreshMissingArt(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	data := decodeData(t, rec)
	if int(data.Data["candidates"].(float64)) != 3 {
		t.Errorf("candidates: got %v, want 3", data.Data["candidates"])
	}
	if int(data.Data["refreshed"].(float64)) != 2 {
		t.Errorf("refreshed: got %v, want 2", data.Data["refreshed"])
	}
	failed := data.Data["failed"].([]any)
	if len(failed) != 1 {
		t.Errorf("failed: got %d, want 1", len(failed))
	}
}

func TestMaintenance_RefreshMissingArt_DBError(t *testing.T) {
	svc := &mockMaintSvc{listErr: errors.New("boom")}
	h := NewMaintenanceHandler(svc, &maintEnricher{}, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/refresh-missing-art", nil)
	rec := httptest.NewRecorder()
	h.RefreshMissingArt(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

// ── DedupeShows / DedupeMovies ───────────────────────────────────────────────

func TestMaintenance_DedupeShows_InvalidLibraryID(t *testing.T) {
	svc := &mockMaintSvc{}
	h := NewMaintenanceHandler(svc, &maintEnricher{}, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/dedupe-shows?library_id=bogus", nil)
	rec := httptest.NewRecorder()
	h.DedupeShows(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if svc.dedupeCalled {
		t.Error("Dedupe must not run for invalid library_id")
	}
}

func TestMaintenance_DedupeShows_NoLibraryScopesAll(t *testing.T) {
	svc := &mockMaintSvc{dedupeRes: media.DedupeResult{MergedItems: 2}}
	h := NewMaintenanceHandler(svc, &maintEnricher{}, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/dedupe-shows", nil)
	rec := httptest.NewRecorder()
	h.DedupeShows(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if svc.dedupeType != "show" {
		t.Errorf("type: got %q, want show", svc.dedupeType)
	}
	if svc.dedupeLibID != nil {
		t.Errorf("libID: got %v, want nil (all libraries)", svc.dedupeLibID)
	}
}

func TestMaintenance_DedupeMovies_ScopesByLibrary(t *testing.T) {
	libID := uuid.New()
	svc := &mockMaintSvc{}
	h := NewMaintenanceHandler(svc, &maintEnricher{}, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/dedupe-movies?library_id="+libID.String(), nil)
	rec := httptest.NewRecorder()
	h.DedupeMovies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if svc.dedupeType != "movie" {
		t.Errorf("type: got %q, want movie", svc.dedupeType)
	}
	if svc.dedupeLibID == nil || *svc.dedupeLibID != libID {
		t.Errorf("libID: got %v, want %s", svc.dedupeLibID, libID)
	}
}

func TestMaintenance_Dedupe_DBError(t *testing.T) {
	svc := &mockMaintSvc{dedupeErr: errors.New("boom")}
	h := NewMaintenanceHandler(svc, &maintEnricher{}, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/dedupe-shows", nil)
	rec := httptest.NewRecorder()
	h.DedupeShows(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
	_ = strings.TrimSpace(rec.Body.String())
}
