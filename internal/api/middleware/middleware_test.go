package middleware

import (
	"context"
	"errors"
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

// ── RequiredAllowQueryToken middleware ───────────────────────────────────────
// The asset-route variant. Bearer + cookie paths must still work; the
// only addition is that a `?token=<paseto>` query param is accepted
// when neither header nor cookie is present.

func TestRequiredAllowQueryToken_QueryToken(t *testing.T) {
	// The whole point of this variant — `<img src="…?token=…">` works.
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	token := issueTestToken(t, tm, false)

	var gotClaims *auth.Claims
	handler := a.RequiredAllowQueryToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/artwork/poster.jpg?w=300&token="+token, nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if gotClaims == nil || gotClaims.Username != "testuser" {
		t.Errorf("claims not extracted from query token: got %v", gotClaims)
	}
}

func TestRequiredAllowQueryToken_BearerHeaderStillWorks(t *testing.T) {
	// Programmatic clients keep using Authorization — the query path
	// is additive, not a replacement.
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	token := issueTestToken(t, tm, false)

	handler := a.RequiredAllowQueryToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/artwork/x.jpg", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

func TestRequiredAllowQueryToken_NoCarriers_Unauthorized(t *testing.T) {
	// Anonymous request — no Bearer, no cookie, no token query — must
	// still be rejected. The variant adds an auth carrier, doesn't
	// disable auth.
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)

	handler := a.RequiredAllowQueryToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler ran without auth")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/artwork/x.jpg", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestRequiredAllowQueryToken_InvalidQueryToken_Unauthorized(t *testing.T) {
	// Garbage in the query param shouldn't be silently treated as
	// "no carrier" — we explicitly tried to authenticate via query
	// and it failed.
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)

	handler := a.RequiredAllowQueryToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler ran with invalid token")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/artwork/x.jpg?token=not-a-valid-paseto", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
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

// ── SessionEpoch revocation ─────────────────────────────────────────────────

type stubEpochReader struct {
	epoch int64
	err   error
}

func (s stubEpochReader) GetSessionEpoch(_ context.Context, _ uuid.UUID) (int64, error) {
	return s.epoch, s.err
}

func TestRequired_DeletedUser_FailsClosed(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm).WithEpochReader(stubEpochReader{err: ErrUserNotFound})
	token := issueTestToken(t, tm, false)

	handler := a.Required(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for deleted user")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("deleted user: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequired_EpochReaderTransientError_FailsOpen(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm).WithEpochReader(stubEpochReader{err: errors.New("connection refused")})
	token := issueTestToken(t, tm, false)

	called := false
	handler := a.Required(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("transient DB error should fail open, not reject")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("transient DB error: got %d, want %d", rec.Code, http.StatusOK)
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

	// Verify key directives are present. script-src and connect-src use
	// substring match because they carry an allow-list of external hosts
	// that grows over time (Cloudflare Insights, etc.) — the test should
	// not have to know every entry.
	exactDirectives := []string{
		"default-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: https:",
		"media-src 'self' blob:",
		"frame-ancestors 'none'",
	}
	for _, d := range exactDirectives {
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

	// Substring checks — these directives carry expanding allow-lists.
	containsChecks := []string{
		"script-src 'self' 'unsafe-inline'",
		"connect-src 'self'",
	}
	for _, sub := range containsChecks {
		if !strings.Contains(csp, sub) {
			t.Errorf("CSP missing prefix %q in %q", sub, csp)
		}
	}
}

func TestSecurityHeaders_CSPAllowsCloudflareInsights(t *testing.T) {
	// Regression guard: Cloudflare proxies (which the beta deployment
	// runs behind) auto-inject the Web Analytics beacon from
	// static.cloudflareinsights.com. Blocking it surfaced as a console
	// CSP violation on the live site.
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "https://static.cloudflareinsights.com") {
		t.Errorf("script-src should allow Cloudflare Insights beacon — CSP = %q", csp)
	}
	if !strings.Contains(csp, "https://cloudflareinsights.com") {
		t.Errorf("connect-src should allow Cloudflare Insights POST — CSP = %q", csp)
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

// ── ViewAs (admin impersonation) ────────────────────────────────────────────

type stubImpersonationLookup struct {
	user ImpersonatedUser
	err  error
}

func (s stubImpersonationLookup) GetUserForImpersonation(_ context.Context, _ uuid.UUID) (ImpersonatedUser, error) {
	return s.user, s.err
}

func TestViewAs_NoParam_PassesThrough(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	target := ImpersonatedUser{ID: uuid.New(), Username: "kid", IsAdmin: false, MaxContentRating: "PG"}
	mw := a.ViewAs(stubImpersonationLookup{user: target})

	var seen *auth.Claims
	handler := a.Required(mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/hub", nil)
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, tm, true))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if seen == nil || seen.Username == "kid" {
		t.Error("no view_as param: claims must be unchanged (admin sees their own claims)")
	}
}

func TestViewAs_AdminSwapsClaims(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	target := ImpersonatedUser{ID: uuid.New(), Username: "kid", IsAdmin: false, MaxContentRating: "PG"}
	mw := a.ViewAs(stubImpersonationLookup{user: target})

	var seen *auth.Claims
	handler := a.Required(mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/hub?view_as="+target.ID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, tm, true))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if seen == nil {
		t.Fatal("expected handler to be called with substituted claims")
	}
	if seen.UserID != target.ID {
		t.Errorf("UserID: got %s, want %s — handler must see target's id, not admin's", seen.UserID, target.ID)
	}
	if seen.Username != "kid" {
		t.Errorf("Username: got %q, want %q", seen.Username, "kid")
	}
	if seen.IsAdmin {
		t.Error("IsAdmin: admin must drop their role while impersonating a non-admin so admin-only handlers refuse")
	}
	if seen.MaxContentRating != "PG" {
		t.Errorf("MaxContentRating: got %q, want PG — content-rating gate must follow the target", seen.MaxContentRating)
	}
}

func TestViewAs_NonAdminCallerIsForbidden(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	target := ImpersonatedUser{ID: uuid.New(), Username: "kid"}
	mw := a.ViewAs(stubImpersonationLookup{user: target})

	handler := a.Required(mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called when non-admin tries to view_as")
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/hub?view_as="+target.ID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, tm, false))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: got %d, want 403 — view_as must require admin", rec.Code)
	}
}

func TestViewAs_NonGETIsForbidden(t *testing.T) {
	// view_as on a write request would let an admin accidentally
	// (or maliciously) mutate state as a target user. The middleware
	// must refuse non-GET regardless of admin status.
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	target := ImpersonatedUser{ID: uuid.New(), Username: "kid"}
	mw := a.ViewAs(stubImpersonationLookup{user: target})

	handler := a.Required(mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for POST + view_as")
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/favorites?view_as="+target.ID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, tm, true))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("POST with view_as: got %d, want 403", rec.Code)
	}
}

func TestViewAs_UnknownTargetIs404(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	mw := a.ViewAs(stubImpersonationLookup{err: ErrUserNotFound})

	handler := a.Required(mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for unknown target")
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/hub?view_as="+uuid.New().String(), nil)
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, tm, true))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown target: got %d, want 404 — admins must not be able to probe live user IDs", rec.Code)
	}
}
