package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
	mu        sync.Mutex
	err       error
	errForIDs map[uuid.UUID]error
	calledIDs []uuid.UUID
	// expect signals when len(calledIDs) reaches this value, so async
	// tests can synchronise with the background worker without sleeping.
	expect int
	done   chan struct{}
}

func (m *maintEnricher) EnrichItem(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	m.calledIDs = append(m.calledIDs, id)
	reached := m.expect > 0 && len(m.calledIDs) == m.expect
	m.mu.Unlock()
	if reached && m.done != nil {
		select {
		case m.done <- struct{}{}:
		default:
		}
	}
	if m.errForIDs != nil {
		if e, ok := m.errForIDs[id]; ok {
			return e
		}
	}
	return m.err
}

func (m *maintEnricher) calls() []uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]uuid.UUID, len(m.calledIDs))
	copy(out, m.calledIDs)
	return out
}

func (m *maintEnricher) awaitN(t *testing.T, n int) {
	t.Helper()
	select {
	case <-m.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("enricher EnrichItem not called %d times within 2s (got %d)", n, len(m.calls()))
	}
}
func (m *maintEnricher) MatchItem(_ context.Context, _ uuid.UUID, _ int) error { return nil }

type mockMaintLibSvc struct {
	mu         sync.Mutex
	purgeCalls []uuid.UUID
	purgeCount int64
	purgeErr   error
	// done is closed when PurgeDeleted is invoked, so async tests
	// can block on the goroutine completing without sleeping.
	done chan struct{}
}

func newMockMaintLibSvc() *mockMaintLibSvc {
	return &mockMaintLibSvc{done: make(chan struct{}, 1)}
}

func (m *mockMaintLibSvc) PurgeDeleted(_ context.Context, id uuid.UUID) (int64, error) {
	m.mu.Lock()
	m.purgeCalls = append(m.purgeCalls, id)
	m.mu.Unlock()
	select {
	case m.done <- struct{}{}:
	default:
	}
	return m.purgeCount, m.purgeErr
}

// awaitPurge blocks until PurgeDeleted has been called once or t fails.
// Used by async-handler tests to synchronise with the detached goroutine.
func (m *mockMaintLibSvc) awaitPurge(t *testing.T) {
	t.Helper()
	select {
	case <-m.done:
	case <-time.After(2 * time.Second):
		t.Fatal("PurgeDeleted not called within 2s")
	}
}

func (m *mockMaintLibSvc) calls() []uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]uuid.UUID, len(m.purgeCalls))
	copy(out, m.purgeCalls)
	return out
}

// ── RefreshMissingArt ────────────────────────────────────────────────────────

func TestMaintenance_RefreshMissingArt_Defaults(t *testing.T) {
	svc := &mockMaintSvc{}
	enr := &maintEnricher{}
	h := NewMaintenanceHandler(svc, &mockMaintLibSvc{}, enr, slog.Default())

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
	h := NewMaintenanceHandler(svc, &mockMaintLibSvc{}, enr, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/refresh-missing-art?limit=999999", nil)
	rec := httptest.NewRecorder()
	h.RefreshMissingArt(rec, req)

	if svc.listLimit != 1000 {
		t.Errorf("limit should clamp to 1000, got %d", svc.listLimit)
	}
}

func TestMaintenance_RefreshMissingArt_QueuesEveryCandidateAsync(t *testing.T) {
	good1 := uuid.New()
	good2 := uuid.New()
	bad := uuid.New()
	svc := &mockMaintSvc{listItems: []media.Item{
		{ID: good1, Title: "Good 1"},
		{ID: bad, Title: "Bad"},
		{ID: good2, Title: "Good 2"},
	}}
	enr := &maintEnricher{
		errForIDs: map[uuid.UUID]error{bad: errors.New("tmdb down")},
		expect:    3,
		done:      make(chan struct{}, 1),
	}
	h := NewMaintenanceHandler(svc, &mockMaintLibSvc{}, enr, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/refresh-missing-art", nil)
	rec := httptest.NewRecorder()
	h.RefreshMissingArt(rec, req)

	// Handler returns immediately with the queued count — the enrich
	// loop runs in a detached goroutine so a 5-30 minute walk doesn't
	// time out the HTTP request (real bug from QA: 90s curl timeout
	// killed the work mid-batch and left items half-enriched).
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	data := decodeData(t, rec)
	if int(data.Data["candidates"].(float64)) != 3 {
		t.Errorf("candidates: got %v, want 3", data.Data["candidates"])
	}
	if int(data.Data["queued"].(float64)) != 3 {
		t.Errorf("queued: got %v, want 3", data.Data["queued"])
	}

	// Wait for the worker to finish processing all 3 items and assert
	// every candidate (including the failing one) was attempted.
	enr.awaitN(t, 3)
	called := enr.calls()
	if len(called) != 3 {
		t.Fatalf("EnrichItem call count: got %d, want 3", len(called))
	}
	got := map[uuid.UUID]bool{}
	for _, id := range called {
		got[id] = true
	}
	for _, id := range []uuid.UUID{good1, good2, bad} {
		if !got[id] {
			t.Errorf("EnrichItem not called for %s", id)
		}
	}
}

func TestMaintenance_RefreshMissingArt_DBError(t *testing.T) {
	svc := &mockMaintSvc{listErr: errors.New("boom")}
	h := NewMaintenanceHandler(svc, &mockMaintLibSvc{}, &maintEnricher{}, slog.Default())

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
	h := NewMaintenanceHandler(svc, &mockMaintLibSvc{}, &maintEnricher{}, slog.Default())

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
	h := NewMaintenanceHandler(svc, &mockMaintLibSvc{}, &maintEnricher{}, slog.Default())

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
	h := NewMaintenanceHandler(svc, &mockMaintLibSvc{}, &maintEnricher{}, slog.Default())

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
	h := NewMaintenanceHandler(svc, &mockMaintLibSvc{}, &maintEnricher{}, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/dedupe-shows", nil)
	rec := httptest.NewRecorder()
	h.DedupeShows(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
	_ = strings.TrimSpace(rec.Body.String())
}

// ── PurgeDeletedLibrary ──────────────────────────────────────────────────────
//
// Handler returns 202 Accepted and runs the cascade DELETE on a
// detached goroutine — synchronous would blow past Cloudflare's 100s
// edge timeout for a multi-thousand-item library and roll back. Tests
// use the mock's `done` channel to synchronise with the goroutine.

func TestMaintenance_PurgeDeletedLibrary_RequiresLibraryID(t *testing.T) {
	lib := newMockMaintLibSvc()
	h := NewMaintenanceHandler(&mockMaintSvc{}, lib, &maintEnricher{}, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/purge-deleted-library", nil)
	rec := httptest.NewRecorder()
	h.PurgeDeletedLibrary(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if calls := lib.calls(); len(calls) != 0 {
		t.Errorf("library.PurgeDeleted should not be called without library_id, calls=%v", calls)
	}
}

func TestMaintenance_PurgeDeletedLibrary_RejectsBadUUID(t *testing.T) {
	lib := newMockMaintLibSvc()
	h := NewMaintenanceHandler(&mockMaintSvc{}, lib, &maintEnricher{}, slog.Default())

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/maintenance/purge-deleted-library?library_id=not-a-uuid", nil)
	rec := httptest.NewRecorder()
	h.PurgeDeletedLibrary(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if calls := lib.calls(); len(calls) != 0 {
		t.Error("library.PurgeDeleted should not be called when UUID parse fails")
	}
}

// TestMaintenance_PurgeDeletedLibrary_AsyncAccepted: handler returns
// 202 immediately, goroutine completes the purge in the background.
// The done channel synchronises the assertion so we don't have a flake.
func TestMaintenance_PurgeDeletedLibrary_AsyncAccepted(t *testing.T) {
	libID := uuid.New()
	lib := newMockMaintLibSvc()
	lib.purgeCount = 178
	h := NewMaintenanceHandler(&mockMaintSvc{}, lib, &maintEnricher{}, slog.Default())

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/maintenance/purge-deleted-library?library_id="+libID.String(), nil)
	rec := httptest.NewRecorder()
	h.PurgeDeletedLibrary(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status: got %d, want 202", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"accepted"`) {
		t.Errorf("body should signal accepted, got %q", rec.Body.String())
	}
	lib.awaitPurge(t)
	calls := lib.calls()
	if len(calls) != 1 || calls[0] != libID {
		t.Errorf("expected 1 PurgeDeleted(%s), got %v", libID, calls)
	}
}

// TestMaintenance_PurgeDeletedLibrary_GoroutineErrorIsLogged: a service
// error inside the goroutine must NOT crash the handler — it just
// gets logged. The handler already returned 202 so the response code
// is unchanged; we just verify the call was made and we didn't panic.
func TestMaintenance_PurgeDeletedLibrary_GoroutineErrorIsLogged(t *testing.T) {
	lib := newMockMaintLibSvc()
	lib.purgeErr = errors.New("db down")
	h := NewMaintenanceHandler(&mockMaintSvc{}, lib, &maintEnricher{}, slog.Default())

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/maintenance/purge-deleted-library?library_id="+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	h.PurgeDeletedLibrary(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status: got %d, want 202 (handler returned before the goroutine)", rec.Code)
	}
	lib.awaitPurge(t)
	if calls := lib.calls(); len(calls) != 1 {
		t.Errorf("expected 1 PurgeDeleted call even on error, got %v", calls)
	}
}
