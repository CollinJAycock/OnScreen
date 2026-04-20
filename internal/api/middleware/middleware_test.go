package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
)

func testTokenMaker(t *testing.T) *auth.TokenMaker {
	t.Helper()
	key := auth.DeriveKey32("test-secret-key-that-is-32-bytes!")
	tm, err := auth.NewTokenMaker(key)
	if err != nil {
		t.Fatalf("NewTokenMaker: %v", err)
	}
	return tm
}

func issueTestToken(t *testing.T, tm *auth.TokenMaker, isAdmin bool) string {
	t.Helper()
	token, err := tm.IssueAccessToken(auth.Claims{
		UserID:   uuid.New(),
		Username: "testuser",
		IsAdmin:  isAdmin,
	})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return token
}

// ── Required middleware ─────────────────────────────────────────────────────

func TestRequired_NoToken(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)

	handler := a.Required(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequired_InvalidToken(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)

	handler := a.Required(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer garbage-token")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequired_ValidToken(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	token := issueTestToken(t, tm, false)

	var gotClaims *auth.Claims
	handler := a.Required(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	if gotClaims == nil {
		t.Fatal("claims not set in context")
	}
	if gotClaims.Username != "testuser" {
		t.Errorf("username: got %q, want %q", gotClaims.Username, "testuser")
	}
}

// ── Optional middleware ─────────────────────────────────────────────────────

func TestOptional_NoToken(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)

	var gotClaims *auth.Claims
	handler := a.Optional(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	if gotClaims != nil {
		t.Error("expected nil claims for unauthenticated request")
	}
}

func TestOptional_ValidToken(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	token := issueTestToken(t, tm, true)

	var gotClaims *auth.Claims
	handler := a.Optional(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	if gotClaims == nil {
		t.Fatal("claims not set in context")
	}
	if !gotClaims.IsAdmin {
		t.Error("expected IsAdmin to be true")
	}
}

// ── AdminRequired middleware ────────────────────────────────────────────────

func TestAdminRequired_NonAdmin(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	token := issueTestToken(t, tm, false)

	handler := a.AdminRequired(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for non-admin")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestAdminRequired_Admin(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	token := issueTestToken(t, tm, true)

	handler := a.AdminRequired(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAdminRequired_NoToken(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)

	handler := a.AdminRequired(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// ── SecurityHeaders middleware ──────────────────────────────────────────────

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"Permissions-Policy":     "camera=(), microphone=(), geolocation=()",
	}
	for header, expected := range want {
		got := rec.Header().Get(header)
		if got != expected {
			t.Errorf("%s: got %q, want %q", header, got, expected)
		}
	}
}

func TestSecurityHeaders_CSP(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header missing")
	}

	// Verify key directives are present.
	requiredDirectives := []string{
		"default-src 'self'",
		"script-src 'self' 'unsafe-inline'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: https:",
		"media-src 'self' blob:",
		"frame-ancestors 'none'",
	}
	for _, d := range requiredDirectives {
		found := false
		for _, part := range splitCSP(csp) {
			if part == d {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("CSP missing directive %q in %q", d, csp)
		}
	}
}

// splitCSP splits a CSP header on "; " boundaries.
func splitCSP(csp string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(csp); i++ {
		if i+1 < len(csp) && csp[i] == ';' && csp[i+1] == ' ' {
			parts = append(parts, csp[start:i])
			start = i + 2
			i++ // skip the space
		}
	}
	if start < len(csp) {
		parts = append(parts, csp[start:])
	}
	return parts
}

// ── MaxBytesBody middleware ─────────────────────────────────────────────────

func TestMaxBytesBody_UnderLimit(t *testing.T) {
	handler := MaxBytesBody(100)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 200)
		n, _ := r.Body.Read(buf)
		w.Write(buf[:n])
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader("hello"))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestMaxBytesBody_OverLimit(t *testing.T) {
	handler := MaxBytesBody(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 200)
		_, err := r.Body.Read(buf)
		if err == nil {
			t.Error("expected error reading body over limit")
		}
	}))

	rec := httptest.NewRecorder()
	body := strings.Repeat("x", 100)
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	handler.ServeHTTP(rec, req)
}

// ── WithClaims helper ───────────────────────────────────────────────────────

func TestWithClaims(t *testing.T) {
	claims := &auth.Claims{
		UserID:   uuid.New(),
		Username: "injected",
		IsAdmin:  true,
	}
	ctx := WithClaims(httptest.NewRequest("GET", "/", nil).Context(), claims)
	got := ClaimsFromContext(ctx)
	if got == nil || got.Username != "injected" {
		t.Errorf("WithClaims roundtrip failed: got %v", got)
	}
}
