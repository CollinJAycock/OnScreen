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
)

// ── mock settings service ────────────────────────────────────────────────────

type mockSettingsService struct {
	key        string
	tvdbKey    string
	setErr     error
	setTVDBErr error
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
