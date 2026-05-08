package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

type stubScanLister struct {
	ids []uuid.UUID
}

func (s *stubScanLister) InFlightScans() []uuid.UUID { return s.ids }

type stubJobsCounters struct {
	missingArt    int
	missingArtErr error
	unmatched     int
	unmatchedErr  error
}

func (s *stubJobsCounters) CountItemsMissingArt(_ context.Context) (int, error) {
	return s.missingArt, s.missingArtErr
}
func (s *stubJobsCounters) CountUnmatchedItems(_ context.Context) (int, error) {
	return s.unmatched, s.unmatchedErr
}

type stubLibNamer struct {
	names map[uuid.UUID]string
}

func (s *stubLibNamer) NameOf(_ context.Context, id uuid.UUID) (string, bool) {
	n, ok := s.names[id]
	return n, ok
}

func TestJobsHandler_ReturnsScansWithLibraryNames(t *testing.T) {
	libA := uuid.New()
	libB := uuid.New()
	scans := &stubScanLister{ids: []uuid.UUID{libA, libB}}
	counters := &stubJobsCounters{missingArt: 12, unmatched: 3}
	namer := &stubLibNamer{names: map[uuid.UUID]string{libA: "Movies"}}

	h := NewJobsHandler(scans, counters, namer, slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	data := decodeData(t, rec)
	if int(data.Data["missing_art_count"].(float64)) != 12 {
		t.Errorf("missing_art_count: got %v, want 12", data.Data["missing_art_count"])
	}
	if int(data.Data["unmatched_count"].(float64)) != 3 {
		t.Errorf("unmatched_count: got %v, want 3", data.Data["unmatched_count"])
	}
	rawScans, _ := data.Data["scans"].([]any)
	if len(rawScans) != 2 {
		t.Fatalf("scans count: got %d, want 2", len(rawScans))
	}
	// First entry has a name; second falls back to UUID-only.
	first := rawScans[0].(map[string]any)
	if first["library_id"] != libA.String() || first["library_name"] != "Movies" {
		t.Errorf("first scan: got %v, want library_id=%s name=Movies", first, libA)
	}
	second := rawScans[1].(map[string]any)
	if second["library_id"] != libB.String() {
		t.Errorf("second scan library_id: got %v, want %s", second["library_id"], libB)
	}
	if _, present := second["library_name"]; present {
		t.Errorf("second scan: library_name should be omitted (omitempty) when namer returns false")
	}
}

func TestJobsHandler_NoScansReturnsEmptyArray(t *testing.T) {
	h := NewJobsHandler(&stubScanLister{}, &stubJobsCounters{}, nil, slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)

	data := decodeData(t, rec)
	rawScans, ok := data.Data["scans"].([]any)
	if !ok {
		t.Fatalf("scans should be an array, got %T", data.Data["scans"])
	}
	if len(rawScans) != 0 {
		t.Errorf("scans length: got %d, want 0", len(rawScans))
	}
}

func TestJobsHandler_CounterErrorsDegradeToZero(t *testing.T) {
	counters := &stubJobsCounters{
		missingArtErr: errors.New("db down"),
		unmatchedErr:  errors.New("db down"),
	}
	h := NewJobsHandler(&stubScanLister{}, counters, nil, slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("transient counter failure should not 500; got %d", rec.Code)
	}
	data := decodeData(t, rec)
	if int(data.Data["missing_art_count"].(float64)) != 0 {
		t.Errorf("missing_art_count: got %v, want 0", data.Data["missing_art_count"])
	}
	if int(data.Data["unmatched_count"].(float64)) != 0 {
		t.Errorf("unmatched_count: got %v, want 0", data.Data["unmatched_count"])
	}
}
