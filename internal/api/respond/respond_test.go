package respond

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSuccess(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	Success(rec, req, map[string]string{"hello": "world"})

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	assertContentType(t, rec)
	body := decodeBody(t, rec)
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatal("missing data envelope")
	}
	if data["hello"] != "world" {
		t.Errorf("data.hello: got %v, want %q", data["hello"], "world")
	}
}

func TestCreated(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	Created(rec, req, map[string]int{"id": 1})

	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusCreated)
	}
	body := decodeBody(t, rec)
	if _, ok := body["data"]; !ok {
		t.Error("missing data envelope")
	}
}

func TestNoContent(t *testing.T) {
	rec := httptest.NewRecorder()
	NoContent(rec)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body should be empty, got %d bytes", rec.Body.Len())
	}
}

func TestList(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	List(rec, req, []string{"a", "b"}, 100, "cursor123")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	body := decodeBody(t, rec)
	if _, ok := body["data"]; !ok {
		t.Error("missing data envelope")
	}
	meta, ok := body["meta"].(map[string]any)
	if !ok {
		t.Fatal("missing meta envelope")
	}
	if meta["total"] != float64(100) {
		t.Errorf("meta.total: got %v, want 100", meta["total"])
	}
	if meta["cursor"] != "cursor123" {
		t.Errorf("meta.cursor: got %v, want %q", meta["cursor"], "cursor123")
	}
}

func TestError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	Error(rec, req, http.StatusTeapot, "TEAPOT", "I'm a teapot")

	if rec.Code != http.StatusTeapot {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusTeapot)
	}
	body := decodeBody(t, rec)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatal("missing error envelope")
	}
	if errObj["code"] != "TEAPOT" {
		t.Errorf("error.code: got %v, want %q", errObj["code"], "TEAPOT")
	}
	if errObj["message"] != "I'm a teapot" {
		t.Errorf("error.message: got %v", errObj["message"])
	}
	// request_id should be present (may be empty)
	if _, ok := errObj["request_id"]; !ok {
		t.Error("missing request_id field")
	}
}

func TestNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	NotFound(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
	assertErrorCode(t, rec, "NOT_FOUND")
}

func TestBadRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	BadRequest(rec, req, "bad field")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
	assertErrorCode(t, rec, "BAD_REQUEST")
}

func TestValidationError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	ValidationError(rec, req, "name too short")

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	assertErrorCode(t, rec, "VALIDATION")
}

func TestUnauthorized(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	Unauthorized(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	assertErrorCode(t, rec, "UNAUTHORIZED")
}

func TestForbidden(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	Forbidden(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
	assertErrorCode(t, rec, "FORBIDDEN")
}

func TestInternalError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	InternalError(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	assertErrorCode(t, rec, "INTERNAL")
}

func TestJSON_SetsContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	JSON(rec, req, http.StatusOK, map[string]string{"k": "v"})

	assertContentType(t, rec)
}

func TestJSON_NilData(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	JSON(rec, req, http.StatusOK, nil)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func assertContentType(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/json")
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return body
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	body := decodeBody(t, rec)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatal("missing error envelope")
	}
	if errObj["code"] != code {
		t.Errorf("error.code: got %v, want %q", errObj["code"], code)
	}
}
