package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/livetv"
)

// mockLiveTVService satisfies LiveTVService with in-memory state. Each
// test seeds whatever it needs and reads the resulting calls back through
// the exposed counters / fields.
type mockLiveTVService struct {
	mu       sync.Mutex
	tuners   map[uuid.UUID]livetv.TunerDevice
	channels map[uuid.UUID]livetv.Channel

	now []livetv.NowNextEntry

	listChansEnabledOnly *bool

	rescanCount  int
	deleteCount  int
	createErr    error
	listChansErr error
}

func newMockLiveTVService() *mockLiveTVService {
	return &mockLiveTVService{
		tuners:   make(map[uuid.UUID]livetv.TunerDevice),
		channels: make(map[uuid.UUID]livetv.Channel),
	}
}

func (m *mockLiveTVService) ListTuners(_ context.Context) ([]livetv.TunerDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]livetv.TunerDevice, 0, len(m.tuners))
	for _, t := range m.tuners {
		out = append(out, t)
	}
	return out, nil
}
func (m *mockLiveTVService) GetTuner(_ context.Context, id uuid.UUID) (livetv.TunerDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tuners[id]
	if !ok {
		return livetv.TunerDevice{}, errors.New("not found")
	}
	return t, nil
}
func (m *mockLiveTVService) CreateTuner(_ context.Context, p livetv.CreateTunerDeviceParams) (livetv.TunerDevice, error) {
	if m.createErr != nil {
		return livetv.TunerDevice{}, m.createErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := livetv.TunerDevice{
		ID: uuid.New(), Type: p.Type, Name: p.Name, Config: p.Config,
		TuneCount: p.TuneCount, Enabled: true,
	}
	m.tuners[t.ID] = t
	return t, nil
}
func (m *mockLiveTVService) UpdateTuner(_ context.Context, p livetv.UpdateTunerDeviceParams) (livetv.TunerDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tuners[p.ID]
	t.Name, t.Config, t.TuneCount = p.Name, p.Config, p.TuneCount
	m.tuners[p.ID] = t
	return t, nil
}
func (m *mockLiveTVService) SetTunerEnabled(_ context.Context, id uuid.UUID, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tuners[id]
	t.Enabled = enabled
	m.tuners[id] = t
	return nil
}
func (m *mockLiveTVService) DeleteTuner(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCount++
	delete(m.tuners, id)
	return nil
}
func (m *mockLiveTVService) RescanTuner(_ context.Context, _ uuid.UUID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rescanCount++
	return 7, nil
}
func (m *mockLiveTVService) ListChannels(_ context.Context, enabledOnly bool) ([]livetv.ChannelWithTuner, error) {
	if m.listChansErr != nil {
		return nil, m.listChansErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listChansEnabledOnly = &enabledOnly
	out := make([]livetv.ChannelWithTuner, 0, len(m.channels))
	for _, c := range m.channels {
		if enabledOnly && !c.Enabled {
			continue
		}
		out = append(out, livetv.ChannelWithTuner{Channel: c, TunerName: "test", TunerType: "fake"})
	}
	return out, nil
}
func (m *mockLiveTVService) GetChannel(_ context.Context, id uuid.UUID) (livetv.Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.channels[id]
	if !ok {
		return livetv.Channel{}, errors.New("not found")
	}
	return c, nil
}
func (m *mockLiveTVService) SetChannelEnabled(_ context.Context, id uuid.UUID, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.channels[id]
	c.Enabled = enabled
	m.channels[id] = c
	return nil
}
func (m *mockLiveTVService) NowAndNext(_ context.Context) ([]livetv.NowNextEntry, error) {
	return m.now, nil
}

// ── 503 path when service not configured ─────────────────────────────────────

func TestLiveTV_NotConfigured_Returns503(t *testing.T) {
	h := NewLiveTVHandler(nil, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/tv/channels", nil)
	rec := httptest.NewRecorder()
	h.ListChannels(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "LIVE_TV_NOT_CONFIGURED") {
		t.Errorf("body should carry stable error code; got %s", rec.Body.String())
	}
}

// ── Tuners ────────────────────────────────────────────────────────────────────

func TestLiveTV_ListTuners_Empty(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/tv/tuners", nil)
	rec := httptest.NewRecorder()
	h.ListTuners(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestLiveTV_CreateTuner_HappyPath(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default())

	body := `{"type":"hdhomerun","name":"Living Room HDHR","config":{"host_url":"http://10.0.0.50"},"tune_count":4}`
	req := httptest.NewRequest("POST", "/api/v1/tv/tuners", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.CreateTuner(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(svc.tuners) != 1 {
		t.Errorf("tuners: got %d, want 1", len(svc.tuners))
	}
	var resp TunerDeviceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Type != "hdhomerun" {
		t.Errorf("type: got %q", resp.Type)
	}
}

func TestLiveTV_CreateTuner_RejectsUnknownType(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default())
	body := `{"type":"satellite","name":"x","config":{}}`
	req := httptest.NewRequest("POST", "/api/v1/tv/tuners", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.CreateTuner(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown type should 400; got %d", rec.Code)
	}
}

func TestLiveTV_CreateTuner_RequiresName(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default())
	body := `{"type":"hdhomerun","config":{}}`
	req := httptest.NewRequest("POST", "/api/v1/tv/tuners", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.CreateTuner(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing name should 400; got %d", rec.Code)
	}
}

func TestLiveTV_CreateTuner_BadJSONIs400(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default())
	req := httptest.NewRequest("POST", "/api/v1/tv/tuners", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()
	h.CreateTuner(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestLiveTV_GetTuner_NotFoundIs404(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/tv/tuners/"+uuid.New().String(), nil)
	req = withChiParam(req, "id", uuid.New().String())
	rec := httptest.NewRecorder()
	h.GetTuner(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestLiveTV_GetTuner_InvalidIDIs400(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/tv/tuners/not-a-uuid", nil)
	req = withChiParam(req, "id", "not-a-uuid")
	rec := httptest.NewRecorder()
	h.GetTuner(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestLiveTV_UpdateTuner_AppliesEnabledToggle(t *testing.T) {
	svc := newMockLiveTVService()
	id := uuid.New()
	svc.tuners[id] = livetv.TunerDevice{ID: id, Type: "fake", Name: "old", Enabled: true}
	h := NewLiveTVHandler(svc, slog.Default())

	body := `{"name":"renamed","config":{},"enabled":false}`
	req := httptest.NewRequest("PATCH", "/api/v1/tv/tuners/"+id.String(), bytes.NewBufferString(body))
	req = withChiParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.UpdateTuner(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if svc.tuners[id].Enabled {
		t.Error("enabled toggle not applied")
	}
	if svc.tuners[id].Name != "renamed" {
		t.Errorf("name: got %q, want renamed", svc.tuners[id].Name)
	}
}

func TestLiveTV_DeleteTuner(t *testing.T) {
	svc := newMockLiveTVService()
	id := uuid.New()
	svc.tuners[id] = livetv.TunerDevice{ID: id}
	h := NewLiveTVHandler(svc, slog.Default())
	req := httptest.NewRequest("DELETE", "/api/v1/tv/tuners/"+id.String(), nil)
	req = withChiParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.DeleteTuner(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", rec.Code)
	}
	if svc.deleteCount != 1 {
		t.Errorf("DeleteTuner not called")
	}
}

func TestLiveTV_RescanTuner_ReturnsCount(t *testing.T) {
	svc := newMockLiveTVService()
	id := uuid.New()
	svc.tuners[id] = livetv.TunerDevice{ID: id}
	h := NewLiveTVHandler(svc, slog.Default())
	req := httptest.NewRequest("POST", "/api/v1/tv/tuners/"+id.String()+"/rescan", nil)
	req = withChiParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.RescanTuner(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]int
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["channel_count"] != 7 {
		t.Errorf("channel_count: got %d, want 7", resp["channel_count"])
	}
}

// ── Channels ─────────────────────────────────────────────────────────────────

func TestLiveTV_ListChannels_DefaultsToEnabledOnly(t *testing.T) {
	svc := newMockLiveTVService()
	svc.channels[uuid.New()] = livetv.Channel{Number: "5.1", Name: "WCBS", Enabled: true}
	svc.channels[uuid.New()] = livetv.Channel{Number: "9", Name: "Hidden", Enabled: false}
	h := NewLiveTVHandler(svc, slog.Default())

	req := httptest.NewRequest("GET", "/api/v1/tv/channels", nil)
	rec := httptest.NewRecorder()
	h.ListChannels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if svc.listChansEnabledOnly == nil || !*svc.listChansEnabledOnly {
		t.Errorf("expected enabledOnly=true by default; got %v", svc.listChansEnabledOnly)
	}
	var resp struct {
		Data []ChannelResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data) != 1 {
		t.Errorf("data len: got %d, want 1 (enabled only)", len(resp.Data))
	}
}

func TestLiveTV_ListChannels_EnabledFalseShowsAll(t *testing.T) {
	svc := newMockLiveTVService()
	svc.channels[uuid.New()] = livetv.Channel{Number: "5.1", Enabled: true}
	svc.channels[uuid.New()] = livetv.Channel{Number: "9", Enabled: false}
	h := NewLiveTVHandler(svc, slog.Default())

	req := httptest.NewRequest("GET", "/api/v1/tv/channels?enabled=false", nil)
	rec := httptest.NewRecorder()
	h.ListChannels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if svc.listChansEnabledOnly == nil || *svc.listChansEnabledOnly {
		t.Errorf("expected enabledOnly=false; got %v", svc.listChansEnabledOnly)
	}
}

func TestLiveTV_SetChannelEnabled(t *testing.T) {
	svc := newMockLiveTVService()
	id := uuid.New()
	svc.channels[id] = livetv.Channel{ID: id, Enabled: true}
	h := NewLiveTVHandler(svc, slog.Default())

	body := `{"enabled":false}`
	req := httptest.NewRequest("PATCH", "/api/v1/tv/channels/"+id.String(), bytes.NewBufferString(body))
	req = withChiParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.SetChannelEnabled(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if svc.channels[id].Enabled {
		t.Error("toggle not applied")
	}
}

func TestLiveTV_NowAndNext_PassesThrough(t *testing.T) {
	svc := newMockLiveTVService()
	chID := uuid.New()
	pid := uuid.New()
	title := "60 Minutes"
	svc.now = []livetv.NowNextEntry{
		{ChannelID: chID, Number: "2.1", ChannelName: "WCBS", ProgramID: &pid, Title: &title},
	}
	h := NewLiveTVHandler(svc, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/tv/channels/now-next", nil)
	rec := httptest.NewRecorder()
	h.NowAndNext(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []NowNextResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data) != 1 || resp.Data[0].Title == nil || *resp.Data[0].Title != title {
		t.Errorf("title not passed through: %+v", resp.Data)
	}
}

func TestLiveTV_NowAndNext_EmptyAllowed(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/tv/channels/now-next", nil)
	rec := httptest.NewRecorder()
	h.NowAndNext(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("empty channels should still 200; got %d", rec.Code)
	}
}
