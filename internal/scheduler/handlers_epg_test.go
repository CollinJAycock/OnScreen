package scheduler

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/livetv"
)

type stubEPGService struct {
	mu        sync.Mutex
	sources   []livetv.EPGSource
	refreshed []uuid.UUID
	errFor    map[uuid.UUID]error
	result    livetv.RefreshResult
}

func (s *stubEPGService) ListEPGSources(_ context.Context) ([]livetv.EPGSource, error) {
	return s.sources, nil
}
func (s *stubEPGService) RefreshEPGSource(_ context.Context, id uuid.UUID) (livetv.RefreshResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshed = append(s.refreshed, id)
	if err, ok := s.errFor[id]; ok {
		return livetv.RefreshResult{}, err
	}
	return s.result, nil
}

func TestEPGRefreshHandler_SkipsNotDueSources(t *testing.T) {
	lastPull := time.Now().Add(-5 * time.Minute)
	svc := &stubEPGService{
		sources: []livetv.EPGSource{{
			ID: uuid.New(), Enabled: true,
			RefreshIntervalMin: 360, LastPullAt: &lastPull,
		}},
	}
	h := NewEPGRefreshHandler(svc)
	summary, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(svc.refreshed) != 0 {
		t.Errorf("should have skipped not-due source; refreshed %d", len(svc.refreshed))
	}
	if !strings.Contains(summary, "skipped=1") {
		t.Errorf("summary should report skipped: %s", summary)
	}
}

func TestEPGRefreshHandler_RefreshesDueSources(t *testing.T) {
	lastPull := time.Now().Add(-7 * time.Hour)
	svc := &stubEPGService{
		sources: []livetv.EPGSource{{
			ID: uuid.New(), Enabled: true,
			RefreshIntervalMin: 360, LastPullAt: &lastPull,
		}},
		result: livetv.RefreshResult{ProgramsIngested: 42},
	}
	h := NewEPGRefreshHandler(svc)
	summary, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(svc.refreshed) != 1 {
		t.Errorf("should have refreshed 1 source; got %d", len(svc.refreshed))
	}
	if !strings.Contains(summary, "refreshed=1") || !strings.Contains(summary, "programs=42") {
		t.Errorf("summary: %s", summary)
	}
}

func TestEPGRefreshHandler_RefreshesNeverPulled(t *testing.T) {
	svc := &stubEPGService{
		sources: []livetv.EPGSource{{
			ID: uuid.New(), Enabled: true,
			RefreshIntervalMin: 360, LastPullAt: nil,
		}},
	}
	h := NewEPGRefreshHandler(svc)
	if _, err := h.Run(context.Background(), nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(svc.refreshed) != 1 {
		t.Errorf("never-pulled source should refresh; got %d", len(svc.refreshed))
	}
}

func TestEPGRefreshHandler_SkipsDisabled(t *testing.T) {
	svc := &stubEPGService{
		sources: []livetv.EPGSource{{
			ID: uuid.New(), Enabled: false, RefreshIntervalMin: 360,
		}},
	}
	h := NewEPGRefreshHandler(svc)
	if _, err := h.Run(context.Background(), nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(svc.refreshed) != 0 {
		t.Errorf("disabled source should not refresh")
	}
}

func TestEPGRefreshHandler_ForceBypassesInterval(t *testing.T) {
	lastPull := time.Now().Add(-1 * time.Minute)
	svc := &stubEPGService{
		sources: []livetv.EPGSource{{
			ID: uuid.New(), Enabled: true,
			RefreshIntervalMin: 360, LastPullAt: &lastPull,
		}},
	}
	h := NewEPGRefreshHandler(svc)
	if _, err := h.Run(context.Background(), []byte(`{"force":true}`)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(svc.refreshed) != 1 {
		t.Errorf("force should refresh even when not due; got %d", len(svc.refreshed))
	}
}

func TestEPGRefreshHandler_PerSourceErrorDoesntFailRun(t *testing.T) {
	bad := uuid.New()
	good := uuid.New()
	svc := &stubEPGService{
		sources: []livetv.EPGSource{
			{ID: bad, Enabled: true, RefreshIntervalMin: 360},
			{ID: good, Enabled: true, RefreshIntervalMin: 360},
		},
		errFor: map[uuid.UUID]error{bad: errors.New("network down")},
	}
	h := NewEPGRefreshHandler(svc)
	summary, err := h.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("one bad source should not fail the whole run: %v", err)
	}
	if !strings.Contains(summary, "failed=1") || !strings.Contains(summary, "refreshed=1") {
		t.Errorf("summary should report both: %s", summary)
	}
}
