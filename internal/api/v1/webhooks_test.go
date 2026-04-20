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

	"github.com/google/uuid"
)

// ── mock webhook service ─────────────────────────────────────────────────────

type mockWebhookService struct {
	endpoints []WebhookEndpoint
	endpoint  *WebhookEndpoint
	listErr   error
	getErr    error
	createErr error
	updateErr error
	deleteErr error
	testErr   error
}

func (m *mockWebhookService) List(_ context.Context) ([]WebhookEndpoint, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.endpoints, nil
}
func (m *mockWebhookService) Get(_ context.Context, _ uuid.UUID) (*WebhookEndpoint, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.endpoint, nil
}
func (m *mockWebhookService) Create(_ context.Context, url, secret string, events []string) (*WebhookEndpoint, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	return &WebhookEndpoint{ID: uuid.New(), URL: url, Events: events, Enabled: true}, nil
}
func (m *mockWebhookService) Update(_ context.Context, id uuid.UUID, url, secret string, events []string, enabled bool) (*WebhookEndpoint, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	return &WebhookEndpoint{ID: id, URL: url, Events: events, Enabled: enabled}, nil
}
func (m *mockWebhookService) Delete(_ context.Context, _ uuid.UUID) error {
	return m.deleteErr
}
func (m *mockWebhookService) SendTest(_ context.Context, _ uuid.UUID) error {
	return m.testErr
}

func newWebhookHandler(svc *mockWebhookService) *WebhookHandler {
	return NewWebhookHandler(svc, slog.Default())
}

// ── List ─────────────────────────────────────────────────────────────────────

func TestWebhook_List_Success(t *testing.T) {
	svc := &mockWebhookService{
		endpoints: []WebhookEndpoint{
			{ID: uuid.New(), URL: "https://example.com/hook", Events: []string{"play"}, Enabled: true},
		},
	}
	h := newWebhookHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWebhook_List_Error(t *testing.T) {
	svc := &mockWebhookService{listErr: errors.New("db down")}
	h := newWebhookHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── Create ───────────────────────────────────────────────────────────────────

func TestWebhook_Create_Success(t *testing.T) {
	h := newWebhookHandler(&mockWebhookService{})

	body := `{"url":"https://example.com/hook","events":["play","stop"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusCreated)
	}
}

func TestWebhook_Create_MissingURL(t *testing.T) {
	h := newWebhookHandler(&mockWebhookService{})

	body := `{"url":"","events":["play"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	h.Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestWebhook_Create_NoEvents(t *testing.T) {
	h := newWebhookHandler(&mockWebhookService{})

	body := `{"url":"https://example.com","events":[]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	h.Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestWebhook_Create_InvalidBody(t *testing.T) {
	h := newWebhookHandler(&mockWebhookService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader("not json"))
	h.Create(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestWebhook_Create_ServiceError(t *testing.T) {
	svc := &mockWebhookService{createErr: errors.New("db down")}
	h := newWebhookHandler(svc)

	body := `{"url":"https://example.com","events":["play"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	h.Create(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── Update ───────────────────────────────────────────────────────────────────

func TestWebhook_Update_Success(t *testing.T) {
	h := newWebhookHandler(&mockWebhookService{})
	id := uuid.New()

	body := `{"url":"https://example.com/new","events":["stop"],"enabled":false}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", id.String())
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data WebhookEndpoint `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data.Enabled {
		t.Error("expected enabled=false")
	}
}

func TestWebhook_Update_NotFound(t *testing.T) {
	svc := &mockWebhookService{getErr: ErrWebhookNotFound, updateErr: ErrWebhookNotFound}
	h := newWebhookHandler(svc)

	body := `{"url":"https://example.com","events":["play"]}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", uuid.New().String())
	h.Update(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestWebhook_Update_PreservesExistingEnabled(t *testing.T) {
	// When enabled is omitted from the PATCH body, the handler preserves the
	// existing endpoint's enabled state instead of defaulting.
	existing := &WebhookEndpoint{ID: uuid.New(), URL: "https://example.com/old", Events: []string{"play"}, Enabled: false}
	h := newWebhookHandler(&mockWebhookService{endpoint: existing})

	body := `{"url":"https://example.com","events":["play"]}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", existing.ID.String())
	h.Update(rec, req)

	var resp struct {
		Data WebhookEndpoint `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data.Enabled {
		t.Error("expected enabled=false (preserved from existing)")
	}
}

// ── Delete ───────────────────────────────────────────────────────────────────

func TestWebhook_Delete_Success(t *testing.T) {
	h := newWebhookHandler(&mockWebhookService{})

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("DELETE", "/", nil), "id", uuid.New().String())
	h.Delete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestWebhook_Delete_NotFound(t *testing.T) {
	svc := &mockWebhookService{deleteErr: ErrWebhookNotFound}
	h := newWebhookHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("DELETE", "/", nil), "id", uuid.New().String())
	h.Delete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// ── Test webhook ─────────────────────────────────────────────────────────────

func TestWebhook_Test_Success(t *testing.T) {
	h := newWebhookHandler(&mockWebhookService{})

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", uuid.New().String())
	h.Test(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestWebhook_Test_NotFound(t *testing.T) {
	svc := &mockWebhookService{testErr: ErrWebhookNotFound}
	h := newWebhookHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", uuid.New().String())
	h.Test(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestWebhook_Test_Error(t *testing.T) {
	svc := &mockWebhookService{testErr: errors.New("connection refused")}
	h := newWebhookHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", uuid.New().String())
	h.Test(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── validateWebhookURL ──────────────────────────────────────────────────────

func TestValidateWebhookURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid https", "https://example.com/callback", false},
		{"valid http", "http://example.com/callback", false},
		{"ftp scheme rejected", "ftp://example.com/hook", true},
		{"no scheme", "example.com/hook", true},
		{"empty", "", true},
		{"loopback ipv4", "http://127.0.0.1/hook", true},
		{"loopback localhost", "http://localhost/hook", true},
		{"private 10.x", "http://10.0.0.1/hook", true},
		{"private 192.168.x", "http://192.168.1.1/hook", true},
		{"private 172.16.x", "http://172.16.0.1/hook", true},
		{"link-local", "http://169.254.1.1/hook", true},
		{"unresolvable host", "http://this-host-does-not-exist-xyz.invalid/hook", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWebhookURL(context.Background(), tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateWebhookURL(%q) err=%v, wantErr=%v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestWebhook_Create_SSRF_Blocked(t *testing.T) {
	h := newWebhookHandler(&mockWebhookService{})

	body := `{"url":"http://127.0.0.1:8080/internal","events":["play"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	h.Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

// ── Update (additional coverage) ────────────────────────────────────────────

func TestWebhook_Update_PartialOnlyEvents(t *testing.T) {
	// PATCH with only events — URL and enabled should be preserved from existing.
	existing := &WebhookEndpoint{
		ID: uuid.New(), URL: "https://example.com/old", Events: []string{"play"}, Enabled: true,
	}
	h := newWebhookHandler(&mockWebhookService{endpoint: existing})

	body := `{"events":["play","stop"]}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", existing.ID.String())
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data WebhookEndpoint `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data.URL != "https://example.com/old" {
		t.Errorf("URL should be preserved: got %q, want %q", resp.Data.URL, "https://example.com/old")
	}
	if !resp.Data.Enabled {
		t.Error("enabled should be preserved as true")
	}
	if len(resp.Data.Events) != 2 {
		t.Errorf("events: got %d items, want 2", len(resp.Data.Events))
	}
}

func TestWebhook_Update_InvalidURL(t *testing.T) {
	existing := &WebhookEndpoint{
		ID: uuid.New(), URL: "https://example.com/old", Events: []string{"play"}, Enabled: true,
	}
	h := newWebhookHandler(&mockWebhookService{endpoint: existing})

	body := `{"url":"ftp://bad-scheme.com/hook"}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", existing.ID.String())
	h.Update(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestWebhook_Update_InvalidBody(t *testing.T) {
	h := newWebhookHandler(&mockWebhookService{})

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader("not json")), "id", uuid.New().String())
	h.Update(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestWebhook_Update_InvalidID(t *testing.T) {
	h := newWebhookHandler(&mockWebhookService{})

	body := `{"events":["play"]}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", "not-a-uuid")
	h.Update(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestWebhook_Update_ServiceError(t *testing.T) {
	// Get succeeds but Update fails with a non-not-found error.
	existing := &WebhookEndpoint{
		ID: uuid.New(), URL: "https://example.com/old", Events: []string{"play"}, Enabled: true,
	}
	svc := &mockWebhookService{endpoint: existing, updateErr: errors.New("db down")}
	h := newWebhookHandler(svc)

	body := `{"events":["play"]}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", existing.ID.String())
	h.Update(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestWebhook_Update_GetServiceError(t *testing.T) {
	// Get fails with a non-not-found error (e.g. DB error during fetch).
	svc := &mockWebhookService{getErr: errors.New("db connection lost")}
	h := newWebhookHandler(svc)

	body := `{"events":["play"]}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", uuid.New().String())
	h.Update(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestWebhook_Update_SSRF_Blocked(t *testing.T) {
	existing := &WebhookEndpoint{
		ID: uuid.New(), URL: "https://example.com/old", Events: []string{"play"}, Enabled: true,
	}
	h := newWebhookHandler(&mockWebhookService{endpoint: existing})

	body := `{"url":"http://192.168.1.1/internal"}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", existing.ID.String())
	h.Update(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

// ── Delete (additional coverage) ────────────────────────────────────────────

func TestWebhook_Delete_InvalidID(t *testing.T) {
	h := newWebhookHandler(&mockWebhookService{})

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("DELETE", "/", nil), "id", "not-a-uuid")
	h.Delete(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestWebhook_Delete_ServiceError(t *testing.T) {
	svc := &mockWebhookService{deleteErr: errors.New("db down")}
	h := newWebhookHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("DELETE", "/", nil), "id", uuid.New().String())
	h.Delete(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── Test webhook (additional coverage) ──────────────────────────────────────

func TestWebhook_Test_InvalidID(t *testing.T) {
	h := newWebhookHandler(&mockWebhookService{})

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", "not-a-uuid")
	h.Test(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ── validateWebhookURL (additional edge cases) ──────────────────────────────

func TestValidateWebhookURL_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		{"empty string", "", true, "URL scheme must be http or https"},
		{"just scheme no host", "http://", true, "URL must include a hostname"},
		{"https no host", "https://", true, "URL must include a hostname"},
		{"data scheme", "data:text/html,hello", true, "URL scheme must be http or https"},
		{"javascript scheme", "javascript:alert(1)", true, "URL scheme must be http or https"},
		{"file scheme", "file:///etc/passwd", true, "URL scheme must be http or https"},
		{"loopback ipv6", "http://[::1]/hook", true, "URL must not point to a private or loopback address"},
		{"unspecified addr", "http://[::]/hook", true, "URL must not point to a private or loopback address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWebhookURL(context.Background(), tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateWebhookURL(%q) err=%v, wantErr=%v", tt.url, err, tt.wantErr)
			}
			if tt.errMsg != "" && err != nil && err.Error() != tt.errMsg {
				t.Errorf("validateWebhookURL(%q) errMsg=%q, want=%q", tt.url, err.Error(), tt.errMsg)
			}
		})
	}
}
