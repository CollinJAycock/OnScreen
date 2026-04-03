package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/transcode"
)

// ── mock worker lister ──────────────────────────────────────────────────────

type mockWorkerLister struct {
	workers []transcode.WorkerRegistration
}

func (m *mockWorkerLister) ListWorkers(_ context.Context) ([]transcode.WorkerRegistration, error) {
	return m.workers, nil
}

// ── mock settings service ────────────────────────────────────────────────────

type mockSettingsService struct {
	key          string
	tvdbKey      string
	setErr       error
	setTVDBErr   error
	fleet        settings.WorkerFleetConfig
	setFleetErr  error
	setFleetCall *settings.WorkerFleetConfig // captures last SetWorkerFleet call
}

func (m *mockSettingsService) TMDBAPIKey(_ context.Context) string {
	return m.key
}
func (m *mockSettingsService) SetTMDBAPIKey(_ context.Context, key string) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.key = key
	return nil
}
func (m *mockSettingsService) TVDBAPIKey(_ context.Context) string {
	return m.tvdbKey
}
func (m *mockSettingsService) SetTVDBAPIKey(_ context.Context, key string) error {
	if m.setTVDBErr != nil {
		return m.setTVDBErr
	}
	if m.setErr != nil {
		return m.setErr
	}
	m.tvdbKey = key
	return nil
}
func (m *mockSettingsService) ArrAPIKey(_ context.Context) string                  { return "" }
func (m *mockSettingsService) SetArrAPIKey(_ context.Context, _ string) error      { return nil }
func (m *mockSettingsService) ArrPathMappings(_ context.Context) map[string]string { return nil }
func (m *mockSettingsService) SetArrPathMappings(_ context.Context, _ map[string]string) error {
	return nil
}
func (m *mockSettingsService) TranscodeEncoders(_ context.Context) string            { return "" }
func (m *mockSettingsService) SetTranscodeEncoders(_ context.Context, _ string) error { return nil }
func (m *mockSettingsService) TranscodeConfigGet(_ context.Context) settings.TranscodeConfig {
	return settings.TranscodeConfig{}
}
func (m *mockSettingsService) SetTranscodeConfig(_ context.Context, _ settings.TranscodeConfig) error {
	return nil
}
func (m *mockSettingsService) WorkerFleet(_ context.Context) settings.WorkerFleetConfig {
	return m.fleet
}
func (m *mockSettingsService) SetWorkerFleet(_ context.Context, cfg settings.WorkerFleetConfig) error {
	if m.setFleetErr != nil {
		return m.setFleetErr
	}
	m.fleet = cfg
	m.setFleetCall = &cfg
	return nil
}

func newSettingsHandler(svc *mockSettingsService) *SettingsHandler {
	return NewSettingsHandler(svc, slog.Default())
}

// ── Get ──────────────────────────────────────────────────────────────────────

func TestSettings_Get(t *testing.T) {
	svc := &mockSettingsService{key: "abc123"}
	h := newSettingsHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data struct {
			TMDBAPIKey string `json:"tmdb_api_key"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data.TMDBAPIKey != "abc1****" {
		t.Errorf("tmdb_api_key: got %q, want %q", resp.Data.TMDBAPIKey, "abc1****")
	}
}

func TestSettings_Get_Empty(t *testing.T) {
	svc := &mockSettingsService{key: ""}
	h := newSettingsHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

// ── Update ───────────────────────────────────────────────────────────────────

func TestSettings_Update_Success(t *testing.T) {
	svc := &mockSettingsService{}
	h := newSettingsHandler(svc)

	body := `{"tmdb_api_key":"newkey"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/", strings.NewReader(body))
	h.Update(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if svc.key != "newkey" {
		t.Errorf("key not set: got %q, want %q", svc.key, "newkey")
	}
}

func TestSettings_Update_InvalidBody(t *testing.T) {
	h := newSettingsHandler(&mockSettingsService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/", strings.NewReader("not json"))
	h.Update(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSettings_Update_ServiceError(t *testing.T) {
	svc := &mockSettingsService{setErr: errors.New("db down")}
	h := newSettingsHandler(svc)

	body := `{"tmdb_api_key":"key"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/", strings.NewReader(body))
	h.Update(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestSettings_Update_OnlyTVDBKey(t *testing.T) {
	svc := &mockSettingsService{key: "existing-tmdb"}
	h := newSettingsHandler(svc)

	body := `{"tvdb_api_key":"new-tvdb-key"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/", strings.NewReader(body))
	h.Update(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if svc.tvdbKey != "new-tvdb-key" {
		t.Errorf("tvdb key: got %q, want %q", svc.tvdbKey, "new-tvdb-key")
	}
	// TMDB key should remain unchanged since it was not in the request body.
	if svc.key != "existing-tmdb" {
		t.Errorf("tmdb key changed unexpectedly: got %q, want %q", svc.key, "existing-tmdb")
	}
}

func TestSettings_Update_OnlyTMDBKey(t *testing.T) {
	svc := &mockSettingsService{tvdbKey: "existing-tvdb"}
	h := newSettingsHandler(svc)

	body := `{"tmdb_api_key":"new-tmdb-key"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/", strings.NewReader(body))
	h.Update(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if svc.key != "new-tmdb-key" {
		t.Errorf("tmdb key: got %q, want %q", svc.key, "new-tmdb-key")
	}
	if svc.tvdbKey != "existing-tvdb" {
		t.Errorf("tvdb key changed unexpectedly: got %q, want %q", svc.tvdbKey, "existing-tvdb")
	}
}

func TestSettings_Update_BothKeys(t *testing.T) {
	svc := &mockSettingsService{}
	h := newSettingsHandler(svc)

	body := `{"tmdb_api_key":"tmdb-val","tvdb_api_key":"tvdb-val"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/", strings.NewReader(body))
	h.Update(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if svc.key != "tmdb-val" {
		t.Errorf("tmdb key: got %q, want %q", svc.key, "tmdb-val")
	}
	if svc.tvdbKey != "tvdb-val" {
		t.Errorf("tvdb key: got %q, want %q", svc.tvdbKey, "tvdb-val")
	}
}

func TestSettings_Update_TVDBServiceError(t *testing.T) {
	svc := &mockSettingsService{setTVDBErr: errors.New("tvdb write failed")}
	h := newSettingsHandler(svc)

	body := `{"tvdb_api_key":"key"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/", strings.NewReader(body))
	h.Update(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestSettings_Update_EmptyBody(t *testing.T) {
	svc := &mockSettingsService{key: "unchanged", tvdbKey: "also-unchanged"}
	h := newSettingsHandler(svc)

	// Neither key is present — should succeed as a no-op.
	body := `{}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/", strings.NewReader(body))
	h.Update(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if svc.key != "unchanged" {
		t.Errorf("tmdb key changed unexpectedly: got %q", svc.key)
	}
	if svc.tvdbKey != "also-unchanged" {
		t.Errorf("tvdb key changed unexpectedly: got %q", svc.tvdbKey)
	}
}

// ── Fleet ───────────────────────────────────────────────────────────────────���

func TestSettings_GetFleet_Default(t *testing.T) {
	svc := &mockSettingsService{fleet: settings.WorkerFleetConfig{EmbeddedEnabled: true}}
	h := newSettingsHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/fleet", nil)
	h.GetFleet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data struct {
			EmbeddedEnabled bool `json:"embedded_enabled"`
			EmbeddedOnline  bool `json:"embedded_online"`
			Workers         []struct {
				Name   string `json:"name"`
				Addr   string `json:"addr"`
				Online bool   `json:"online"`
			} `json:"workers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Data.EmbeddedEnabled {
		t.Error("embedded_enabled: want true")
	}
	if resp.Data.EmbeddedOnline {
		t.Error("embedded_online: want false (no worker lister)")
	}
	if len(resp.Data.Workers) != 0 {
		t.Errorf("workers: want 0, got %d", len(resp.Data.Workers))
	}
}

func TestSettings_GetFleet_WithDiscoveredWorkers(t *testing.T) {
	// Fleet config stores overrides keyed by addr.
	svc := &mockSettingsService{
		fleet: settings.WorkerFleetConfig{
			EmbeddedEnabled: false,
			Workers: []settings.WorkerSlotConfig{
				{Addr: "10.0.0.5:7073", Name: "GPU Box", Encoder: "h264_nvenc"},
				{Addr: "10.0.0.6:7073", Name: "AMD Box", Encoder: "h264_amf"},
			},
		},
	}
	h := newSettingsHandler(svc)

	// Simulate two live workers discovered via Valkey.
	h.SetWorkerLister(&mockWorkerLister{workers: []transcode.WorkerRegistration{
		{ID: "w1", Addr: "10.0.0.5:7073", Capabilities: []string{"h264_nvenc", "libx264"}, MaxSessions: 8, ActiveSessions: 2},
		{ID: "w2", Addr: "10.0.0.6:7073", Capabilities: []string{"h264_amf", "libx264"}, MaxSessions: 4, ActiveSessions: 0},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/fleet", nil)
	h.GetFleet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data struct {
			EmbeddedEnabled bool `json:"embedded_enabled"`
			Workers         []struct {
				Addr           string   `json:"addr"`
				Name           string   `json:"name"`
				Encoder        string   `json:"encoder"`
				Online         bool     `json:"online"`
				ActiveSessions int      `json:"active_sessions"`
				MaxSessions    int      `json:"max_sessions"`
				Capabilities   []string `json:"capabilities"`
			} `json:"workers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.EmbeddedEnabled {
		t.Error("embedded_enabled: want false")
	}
	if len(resp.Data.Workers) != 2 {
		t.Fatalf("workers: want 2, got %d", len(resp.Data.Workers))
	}
	// Workers should have both live data and saved overrides merged.
	w0 := resp.Data.Workers[0]
	if w0.Name != "GPU Box" {
		t.Errorf("workers[0].name: got %q", w0.Name)
	}
	if w0.Encoder != "h264_nvenc" {
		t.Errorf("workers[0].encoder: got %q", w0.Encoder)
	}
	if !w0.Online {
		t.Error("workers[0].online: want true")
	}
	if w0.ActiveSessions != 2 {
		t.Errorf("workers[0].active_sessions: got %d", w0.ActiveSessions)
	}
	if w0.MaxSessions != 8 {
		t.Errorf("workers[0].max_sessions: got %d", w0.MaxSessions)
	}
	w1 := resp.Data.Workers[1]
	if w1.Addr != "10.0.0.6:7073" {
		t.Errorf("workers[1].addr: got %q", w1.Addr)
	}
	if w1.Name != "AMD Box" {
		t.Errorf("workers[1].name: got %q", w1.Name)
	}
}

func TestSettings_GetFleet_SavedOfflineWorkers(t *testing.T) {
	// Saved fleet has 3 workers (one with addr, two manually-added without addr),
	// but only 1 is live — the others should appear as offline in the response.
	svc := &mockSettingsService{
		fleet: settings.WorkerFleetConfig{
			EmbeddedEnabled: true,
			Workers: []settings.WorkerSlotConfig{
				{Addr: "10.0.0.5:7073", Name: "GPU Box", Encoder: "h264_nvenc"},
				{Addr: "", Name: "CPU Box", Encoder: "libx264"},
				{Addr: "", Name: "Planned", Encoder: "h264_qsv"},
			},
		},
	}
	h := newSettingsHandler(svc)

	// Only one worker is live.
	h.SetWorkerLister(&mockWorkerLister{workers: []transcode.WorkerRegistration{
		{ID: "w1", Addr: "10.0.0.5:7073", Capabilities: []string{"h264_nvenc"}, MaxSessions: 4, ActiveSessions: 1},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/fleet", nil)
	h.GetFleet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data struct {
			Workers []struct {
				ID      string `json:"id"`
				Addr    string `json:"addr"`
				Name    string `json:"name"`
				Encoder string `json:"encoder"`
				Online  bool   `json:"online"`
			} `json:"workers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data.Workers) != 3 {
		t.Fatalf("workers: want 3, got %d", len(resp.Data.Workers))
	}
	// First worker should be online (live), id = addr.
	if !resp.Data.Workers[0].Online {
		t.Error("workers[0]: want online")
	}
	if resp.Data.Workers[0].ID != "10.0.0.5:7073" {
		t.Errorf("workers[0].id: got %q", resp.Data.Workers[0].ID)
	}
	if resp.Data.Workers[0].Name != "GPU Box" {
		t.Errorf("workers[0].name: got %q", resp.Data.Workers[0].Name)
	}
	// Second and third are manually-added (no addr) — should get unique IDs.
	if resp.Data.Workers[1].Online {
		t.Error("workers[1]: want offline")
	}
	if resp.Data.Workers[1].ID != "manual-1" {
		t.Errorf("workers[1].id: got %q, want %q", resp.Data.Workers[1].ID, "manual-1")
	}
	if resp.Data.Workers[1].Name != "CPU Box" {
		t.Errorf("workers[1].name: got %q", resp.Data.Workers[1].Name)
	}
	if resp.Data.Workers[2].ID != "manual-2" {
		t.Errorf("workers[2].id: got %q, want %q", resp.Data.Workers[2].ID, "manual-2")
	}
	if resp.Data.Workers[2].Encoder != "h264_qsv" {
		t.Errorf("workers[2].encoder: got %q", resp.Data.Workers[2].Encoder)
	}
}

func TestSettings_UpdateFleet_Success(t *testing.T) {
	svc := &mockSettingsService{fleet: settings.WorkerFleetConfig{EmbeddedEnabled: true}}
	h := newSettingsHandler(svc)

	body := `{"embedded_enabled":false,"embedded_encoder":"h264_amf","workers":[{"addr":"10.0.0.5:7073","name":"NV","encoder":"h264_nvenc"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/settings/fleet", strings.NewReader(body))
	h.UpdateFleet(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if svc.setFleetCall == nil {
		t.Fatal("SetWorkerFleet was not called")
	}
	if svc.setFleetCall.EmbeddedEnabled {
		t.Error("embedded_enabled: want false")
	}
	if svc.setFleetCall.EmbeddedEncoder != "h264_amf" {
		t.Errorf("embedded_encoder: got %q", svc.setFleetCall.EmbeddedEncoder)
	}
	if len(svc.setFleetCall.Workers) != 1 {
		t.Fatalf("workers: want 1, got %d", len(svc.setFleetCall.Workers))
	}
	if svc.setFleetCall.Workers[0].Name != "NV" {
		t.Errorf("workers[0].name: got %q", svc.setFleetCall.Workers[0].Name)
	}
	if svc.setFleetCall.Workers[0].Encoder != "h264_nvenc" {
		t.Errorf("workers[0].encoder: got %q", svc.setFleetCall.Workers[0].Encoder)
	}
}

func TestSettings_UpdateFleet_InvalidBody(t *testing.T) {
	h := newSettingsHandler(&mockSettingsService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/settings/fleet", strings.NewReader("not json"))
	h.UpdateFleet(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSettings_UpdateFleet_ServiceError(t *testing.T) {
	svc := &mockSettingsService{setFleetErr: errors.New("db down")}
	h := newSettingsHandler(svc)

	body := `{"embedded_enabled":true,"embedded_encoder":"","workers":[]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/settings/fleet", strings.NewReader(body))
	h.UpdateFleet(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestSettings_UpdateFleet_EmptyWorkerList(t *testing.T) {
	svc := &mockSettingsService{}
	h := newSettingsHandler(svc)

	body := `{"embedded_enabled":true,"embedded_encoder":"","workers":[]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/settings/fleet", strings.NewReader(body))
	h.UpdateFleet(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if svc.setFleetCall == nil {
		t.Fatal("SetWorkerFleet was not called")
	}
	if len(svc.setFleetCall.Workers) != 0 {
		t.Errorf("workers: want 0, got %d", len(svc.setFleetCall.Workers))
	}
}

func TestSettings_UpdateFleet_MultipleWorkers(t *testing.T) {
	svc := &mockSettingsService{}
	h := newSettingsHandler(svc)

	body := `{
		"embedded_enabled": true,
		"embedded_encoder": "",
		"workers": [
			{"name": "NVIDIA", "addr": "10.0.0.5:7073", "encoder": "h264_nvenc"},
			{"name": "AMD",    "addr": "10.0.0.6:7073", "encoder": "h264_amf"},
			{"name": "Intel",  "addr": "10.0.0.7:7073", "encoder": "h264_qsv"}
		]
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/settings/fleet", strings.NewReader(body))
	h.UpdateFleet(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if len(svc.setFleetCall.Workers) != 3 {
		t.Fatalf("workers: want 3, got %d", len(svc.setFleetCall.Workers))
	}
	encoders := []string{
		svc.setFleetCall.Workers[0].Encoder,
		svc.setFleetCall.Workers[1].Encoder,
		svc.setFleetCall.Workers[2].Encoder,
	}
	want := []string{"h264_nvenc", "h264_amf", "h264_qsv"}
	for i, w := range want {
		if encoders[i] != w {
			t.Errorf("workers[%d].encoder: got %q, want %q", i, encoders[i], w)
		}
	}
}

// ── GetWorkers ──────────────────────────────────────────────────���────────────

func TestSettings_GetWorkers_NilLister(t *testing.T) {
	h := newSettingsHandler(&mockSettingsService{})
	// workerLister is nil by default

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/workers", nil)
	h.GetWorkers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data []interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 0 {
		t.Errorf("want empty array, got %d items", len(resp.Data))
	}
}
