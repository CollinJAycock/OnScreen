package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
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

func issueAssetToken(t *testing.T, tm *auth.TokenMaker) string {
	t.Helper()
	tok, err := tm.IssueAssetToken(auth.Claims{
		UserID:   uuid.New(),
		Username: "assetuser",
	})
	if err != nil {
		t.Fatalf("IssueAssetToken: %v", err)
	}
	return tok
}

func TestRequiredAllowQueryToken_AssetToken(t *testing.T) {
	// The whole point of this variant — `<img src="…?token=…">` works,
	// now with a purpose=asset token rather than a general access token.
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	token := issueAssetToken(t, tm)

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
	if gotClaims == nil || gotClaims.Username != "assetuser" {
		t.Errorf("claims not extracted from asset query token: got %v", gotClaims)
	}
}

func TestRequiredAllowQueryToken_StaleCookieDoesNotShadowQueryToken(t *testing.T) {
	// Regression: an expired/invalid access-token cookie must NOT short-circuit
	// the ?token= asset-token fallback. SSE (EventSource), <img>, and native
	// players can't resend a Bearer, so a cookie that lapsed mid-stream would
	// otherwise 401 the request even though a valid asset token is in the URL —
	// exactly the notifications/stream 401 seen on the beta.
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	token := issueAssetToken(t, tm)

	var gotClaims *auth.Claims
	handler := a.RequiredAllowQueryToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/notifications/stream?token="+token, nil)
	// A cookie that fails validation (stands in for an expired one — both make
	// extractClaims return an error rather than nil).
	req.AddCookie(&http.Cookie{Name: "onscreen_at", Value: "stale-invalid-cookie"})
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("stale cookie shadowed the asset token: got %d, want 200", rec.Code)
	}
	if gotClaims == nil || gotClaims.Username != "assetuser" {
		t.Errorf("asset token claims not used after stale cookie: got %v", gotClaims)
	}
}

func TestRequiredAllowQueryToken_GeneralTokenRejected(t *testing.T) {
	// Security regression guard: a general access token must NOT be
	// honoured in `?token=`. Putting a general-API credential in a URL
	// (server logs, Referer, browser history) was the leak the asset
	// token closes. Browsers authenticate assets via the httpOnly
	// cookie (the Bearer/cookie path), not this query path.
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	token := issueTestToken(t, tm, false) // general access token

	handler := a.RequiredAllowQueryToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("general access token must not authenticate via ?token=")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/artwork/poster.jpg?w=300&token="+token, nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("general token in ?token=: got %d, want 401", rec.Code)
	}
}

func TestRequiredAllowQueryToken_AssetTokenRejectedAsBearer(t *testing.T) {
	// The asset token is purpose-scoped — it must not unlock general API
	// routes if presented as a Bearer (the inverse of the leak we close).
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	token := issueAssetToken(t, tm)

	handler := a.Required(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("asset token must not unlock general API routes")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/items", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("asset token via Bearer: got %d, want 401", rec.Code)
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

// ── Stream-purpose tokens ────────────────────────────────────────────────────
//
// Stream tokens are 24 h, scoped to a single file_id, and flagged
// purpose=stream. They:
//   - work via `?token=` on the stream / subtitle routes when the
//     URL's {id} matches the token's file_id;
//   - are rejected via Bearer (the general-API path);
//   - are rejected via `?token=` when the file_id doesn't match.

func issueStreamToken(t *testing.T, tm *auth.TokenMaker, fileID uuid.UUID) string {
	t.Helper()
	tok, err := tm.IssueStreamToken(auth.Claims{
		UserID:   uuid.New(),
		Username: "streamuser",
	}, fileID)
	if err != nil {
		t.Fatalf("IssueStreamToken: %v", err)
	}
	return tok
}

func TestStreamToken_QueryAcceptedOnMatchingFileID(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	fileID := uuid.New()
	token := issueStreamToken(t, tm, fileID)

	// chi.URLParam pulls from the route's RouteContext, which the
	// raw httptest.NewRequest doesn't set. Wire a tiny chi router
	// so the {id} param resolves the same way it does in production.
	mux := chi.NewRouter()
	mux.With(a.RequiredAllowQueryToken).Get("/media/stream/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/stream/"+fileID.String()+"?token="+token, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("matching file_id: got %d, want 200", rec.Code)
	}
}

func TestStreamToken_QueryRejectedOnMismatchedFileID(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	tokenForFileA := issueStreamToken(t, tm, uuid.New())
	differentFileID := uuid.New()

	mux := chi.NewRouter()
	mux.With(a.RequiredAllowQueryToken).Get("/media/stream/{id}", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler ran with mismatched file_id")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/stream/"+differentFileID.String()+"?token="+tokenForFileA, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("mismatched file_id: got %d, want 401", rec.Code)
	}
}

func TestStreamToken_BearerRejectedOnGeneralRoute(t *testing.T) {
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	token := issueStreamToken(t, tm, uuid.New())

	handler := a.Required(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("stream token must not unlock general API routes")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/items", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("stream token via Bearer: got %d, want 401", rec.Code)
	}
}

func TestStreamToken_QueryRejectedOnNonFileScopedRoute(t *testing.T) {
	// /artwork/* has no {id}/{fileId} param — a stream token in a
	// query there can't bind to anything, so it must be rejected.
	tm := testTokenMaker(t)
	a := NewAuthenticator(tm)
	token := issueStreamToken(t, tm, uuid.New())

	mux := chi.NewRouter()
	mux.With(a.RequiredAllowQueryToken).Get("/artwork/*", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("stream token must not unlock /artwork")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/artwork/poster.jpg?token="+token, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("stream token on /artwork: got %d, want 401", rec.Code)
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
		"style-src 'self' 'unsafe-inline' blob:",
		"img-src 'self' data: https: blob:",
		"font-src 'self' data: blob:",
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
		"script-src 'self' 'nonce-",
		"connect-src 'self'",
	}
	for _, sub := range containsChecks {
		if !strings.Contains(csp, sub) {
			t.Errorf("CSP missing prefix %q in %q", sub, csp)
		}
	}

	// script-src must NOT carry 'unsafe-inline' anymore — that's the
	// whole point of the nonce switch. (style-src legitimately keeps it.)
	scriptDirective := ""
	for _, part := range splitCSP(csp) {
		if strings.HasPrefix(part, "script-src ") {
			scriptDirective = part
		}
	}
	if scriptDirective == "" {
		t.Fatalf("no script-src directive in %q", csp)
	}
	if strings.Contains(scriptDirective, "'unsafe-inline'") {
		t.Errorf("script-src must not contain 'unsafe-inline' (nonce-based now): %q", scriptDirective)
	}
}

func TestSecurityHeaders_CSPNoncePerRequest(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handler must see the same nonce that's in the header so the
		// shell can stamp its inline scripts to match.
		if NonceFromContext(r.Context()) == "" {
			t.Error("nonce missing from request context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	nonceOf := func() (string, string) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		csp := rec.Header().Get("Content-Security-Policy")
		// Extract the nonce-… token from script-src.
		const marker = "'nonce-"
		i := strings.Index(csp, marker)
		if i < 0 {
			return "", csp
		}
		rest := csp[i+len(marker):]
		j := strings.Index(rest, "'")
		if j < 0 {
			return "", csp
		}
		return rest[:j], csp
	}

	n1, csp1 := nonceOf()
	n2, _ := nonceOf()
	if n1 == "" || n2 == "" {
		t.Fatalf("CSP carried no nonce: %q", csp1)
	}
	if n1 == n2 {
		t.Errorf("nonce must be per-request; got the same value twice: %q", n1)
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

func TestSecurityHeaders_CSPAllowsGoogleCastSDK(t *testing.T) {
	// Regression guard: the watch screen injects cast_sender.js from
	// www.gstatic.com when the user opens it, and the Cast picker renders
	// inside an iframe served from the same host. Blocking either showed
	// up on QA as a CSP violation on the player and a dead Cast button.
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	parts := splitCSP(csp)
	var scriptDirective, frameDirective string
	for _, p := range parts {
		if strings.HasPrefix(p, "script-src ") {
			scriptDirective = p
		}
		if strings.HasPrefix(p, "frame-src ") {
			frameDirective = p
		}
	}
	if !strings.Contains(scriptDirective, "https://www.gstatic.com") {
		t.Errorf("script-src should allow https://www.gstatic.com (Cast sender SDK) — got %q", scriptDirective)
	}
	if !strings.Contains(frameDirective, "https://www.gstatic.com") {
		t.Errorf("frame-src should allow https://www.gstatic.com (Cast picker iframe) — got %q", frameDirective)
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
	mw := a.ViewAs(stubImpersonationLookup{user: target}, nil)

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
	mw := a.ViewAs(stubImpersonationLookup{user: target}, nil)

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
	mw := a.ViewAs(stubImpersonationLookup{user: target}, nil)

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
	mw := a.ViewAs(stubImpersonationLookup{user: target}, nil)

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
	mw := a.ViewAs(stubImpersonationLookup{err: ErrUserNotFound}, nil)

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
