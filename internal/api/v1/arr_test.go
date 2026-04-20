package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

type mockArrSettings struct{ key string }

func (m *mockArrSettings) ArrAPIKey(_ context.Context) string                  { return m.key }
func (m *mockArrSettings) ArrPathMappings(_ context.Context) map[string]string { return nil }

type mockArrLibs struct {
	foundID uuid.UUID
	findErr error
	scanErr error
	lastDir string
}

func (m *mockArrLibs) FindLibraryByPath(_ context.Context, _ string) (uuid.UUID, error) {
	return m.foundID, m.findErr
}
func (m *mockArrLibs) TriggerDirectoryScan(_ context.Context, _ uuid.UUID, dir string) error {
	m.lastDir = dir
	return m.scanErr
}

func arrRequest(t *testing.T, h *ArrHandler, apikey string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	url := "/api/v1/arr/webhook"
	if apikey != "" {
		url += "?apikey=" + apikey
	}
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Webhook(rec, req)
	return rec
}

func TestArr_NoKeyConfigured(t *testing.T) {
	h := NewArrHandler(&mockArrSettings{key: ""}, &mockArrLibs{}, slog.Default())
	rec := arrRequest(t, h, "anything", map[string]string{"eventType": "Test"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

func TestArr_WrongKey(t *testing.T) {
	h := NewArrHandler(&mockArrSettings{key: "correct"}, &mockArrLibs{}, slog.Default())
	rec := arrRequest(t, h, "wrong", map[string]string{"eventType": "Test"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

func TestArr_TestEvent(t *testing.T) {
	h := NewArrHandler(&mockArrSettings{key: "mykey"}, &mockArrLibs{}, slog.Default())
	rec := arrRequest(t, h, "mykey", map[string]string{"eventType": "Test"})
	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
}

func TestArr_HeaderAuth(t *testing.T) {
	h := NewArrHandler(&mockArrSettings{key: "mykey"}, &mockArrLibs{}, slog.Default())
	body, _ := json.Marshal(map[string]string{"eventType": "Test"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/arr/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", "mykey")
	rec := httptest.NewRecorder()
	h.Webhook(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
}

func TestArr_RadarrDownload(t *testing.T) {
	libID := uuid.New()
	libs := &mockArrLibs{foundID: libID}
	h := NewArrHandler(&mockArrSettings{key: "k"}, libs, slog.Default())

	payload := map[string]any{
		"eventType": "Download",
		"movie":     map[string]any{"title": "Test Movie", "year": 2024, "folderPath": "/movies/Test Movie (2024)"},
		"movieFile": map[string]any{"path": "/movies/Test Movie (2024)/test.mkv"},
	}
	rec := arrRequest(t, h, "k", payload)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
	wantDir := filepath.Dir("/movies/Test Movie (2024)/test.mkv")
	if libs.lastDir != wantDir {
		t.Errorf("scanned dir = %q, want %q", libs.lastDir, wantDir)
	}
}

func TestArr_SonarrDownload(t *testing.T) {
	libID := uuid.New()
	libs := &mockArrLibs{foundID: libID}
	h := NewArrHandler(&mockArrSettings{key: "k"}, libs, slog.Default())

	payload := map[string]any{
		"eventType":   "Download",
		"series":      map[string]any{"title": "Test Show", "path": "/tv/Test Show"},
		"episodeFile": map[string]any{"path": "/tv/Test Show/Season 01/ep.mkv"},
	}
	rec := arrRequest(t, h, "k", payload)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
	wantDir := filepath.Dir("/tv/Test Show/Season 01/ep.mkv")
	if libs.lastDir != wantDir {
		t.Errorf("scanned dir = %q, want %q", libs.lastDir, wantDir)
	}
}

func TestArr_GrabEventIgnored(t *testing.T) {
	h := NewArrHandler(&mockArrSettings{key: "k"}, &mockArrLibs{}, slog.Default())
	rec := arrRequest(t, h, "k", map[string]string{"eventType": "Grab"})
	if rec.Code != http.StatusNoContent {
		t.Errorf("got %d, want 204", rec.Code)
	}
}
