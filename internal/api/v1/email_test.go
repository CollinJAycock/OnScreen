package v1

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmail_Enabled_NoSender(t *testing.T) {
	h := NewEmailHandler(nil, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/email/enabled", nil)
	rec := httptest.NewRecorder()
	h.Enabled(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Errorf("expected enabled=false in body: %s", rec.Body.String())
	}
}

func TestEmail_Enabled_WithSender(t *testing.T) {
	h := NewEmailHandler(dummySender(), slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/email/enabled", nil)
	rec := httptest.NewRecorder()
	h.Enabled(rec, req)

	if !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Errorf("expected enabled=true in body: %s", rec.Body.String())
	}
}

func TestEmail_SendTest_RejectsWhenSMTPNotConfigured(t *testing.T) {
	h := NewEmailHandler(nil, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/email/test", strings.NewReader(`{"to":"a@b.c"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SendTest(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestEmail_SendTest_RejectsMissingTo(t *testing.T) {
	h := NewEmailHandler(dummySender(), slog.Default())

	cases := []string{`{}`, `{"to":""}`, `not-json`}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/email/test", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.SendTest(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: got %d, want 400", body, rec.Code)
		}
	}
}
