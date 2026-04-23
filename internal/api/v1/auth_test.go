package v1

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
)

// ── mock auth service ────────────────────────────────────────────────────────

type mockAuthService struct {
	loginErr    error
	loginResult *TokenPair

	refreshErr    error
	refreshResult *TokenPair

	createUserErr    error
	createUserResult *UserInfo

	userCount    int64
	userCountErr error
}

func (m *mockAuthService) LoginLocal(_ context.Context, _, _ string) (*TokenPair, error) {
	if m.loginErr != nil {
		return nil, m.loginErr
	}
	return m.loginResult, nil
}
func (m *mockAuthService) Refresh(_ context.Context, _ string) (*TokenPair, error) {
	if m.refreshErr != nil {
		return nil, m.refreshErr
	}
	return m.refreshResult, nil
}
func (m *mockAuthService) Logout(_ context.Context, _ string) error { return nil }
func (m *mockAuthService) CreateUser(_ context.Context, _, _, _ string, _ bool) (*UserInfo, error) {
	if m.createUserErr != nil {
		return nil, m.createUserErr
	}
	return m.createUserResult, nil
}
func (m *mockAuthService) CreateFirstAdmin(_ context.Context, _, _, _ string) (*UserInfo, error) {
	if m.createUserErr != nil {
		return nil, m.createUserErr
	}
	return m.createUserResult, nil
}
func (m *mockAuthService) UserCount(_ context.Context) (int64, error) {
	if m.userCountErr != nil {
		return 0, m.userCountErr
	}
	return m.userCount, nil
}

func newAuthHandler(svc *mockAuthService) *AuthHandler {
	return NewAuthHandler(svc, slog.Default())
}

// ── Login ────────────────────────────────────────────────────────────────────

func TestLogin_Success(t *testing.T) {
	svc := &mockAuthService{
		loginResult: &TokenPair{
			AccessToken:  "access-tok",
			RefreshToken: "refresh-tok",
			UserID:       uuid.New(),
			Username:     "alice",
		},
	}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"username":"alice","password":"password12345"}`))
	h.Login(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp map[string]json.RawMessage
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, ok := resp["data"]; !ok {
		t.Error("response missing data envelope")
	}
}

func TestLogin_InvalidCredentials(t *testing.T) {
	svc := &mockAuthService{loginErr: errors.New("bad password")}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"username":"alice","password":"wrongpass1"}`))
	h.Login(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestLogin_InvalidBody(t *testing.T) {
	h := newAuthHandler(&mockAuthService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`not json`))
	h.Login(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ── Refresh ──────────────────────────────────────────────────────────────────

func TestRefresh_Success(t *testing.T) {
	svc := &mockAuthService{
		refreshResult: &TokenPair{AccessToken: "new-access"},
	}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh",
		strings.NewReader(`{"refresh_token":"old-tok"}`))
	h.Refresh(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRefresh_Invalid(t *testing.T) {
	svc := &mockAuthService{refreshErr: errors.New("expired")}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh",
		strings.NewReader(`{"refresh_token":"bad"}`))
	h.Refresh(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// ── Logout ───────────────────────────────────────────────────────────────────

func TestLogout_ReturnsNoContent(t *testing.T) {
	h := newAuthHandler(&mockAuthService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/logout",
		strings.NewReader(`{"refresh_token":"tok"}`))
	h.Logout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestLogin_ShortPassword_AcceptedByLogin(t *testing.T) {
	// Login has no minimum password length — any non-empty password is forwarded
	// to the service. The service decides if credentials are valid.
	svc := &mockAuthService{
		loginResult: &TokenPair{
			AccessToken:  "at",
			RefreshToken: "rt",
			UserID:       uuid.New(),
			Username:     "alice",
		},
	}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"username":"alice","password":"short"}`))
	h.Login(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d — login should accept any non-empty password", rec.Code, http.StatusOK)
	}
}

func TestLogin_SingleCharPassword_AcceptedByLogin(t *testing.T) {
	svc := &mockAuthService{
		loginResult: &TokenPair{
			AccessToken:  "at",
			RefreshToken: "rt",
			UserID:       uuid.New(),
			Username:     "alice",
		},
	}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"username":"alice","password":"x"}`))
	h.Login(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d — login should accept single-char password", rec.Code, http.StatusOK)
	}
}

func TestLogin_EmptyPassword_Rejected(t *testing.T) {
	h := newAuthHandler(&mockAuthService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"username":"alice","password":""}`))
	h.Login(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ── Register ─────────────────────────────────────────────────────────────────

func TestRegister_FirstUser_AlwaysAdmin(t *testing.T) {
	svc := &mockAuthService{
		userCount: 0,
		createUserResult: &UserInfo{
			ID:       uuid.New(),
			Username: "admin",
			IsAdmin:  true,
		},
	}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"admin","password":"password12345","is_admin":false}`))
	h.Register(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusCreated)
	}
}

func TestRegister_SubsequentUser_RequiresAdmin(t *testing.T) {
	svc := &mockAuthService{userCount: 1}
	h := newAuthHandler(svc)

	// No auth context — should be forbidden.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"user2","password":"password12345"}`))
	h.Register(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRegister_SubsequentUser_AdminCanCreate(t *testing.T) {
	svc := &mockAuthService{
		userCount: 1,
		createUserResult: &UserInfo{
			ID:       uuid.New(),
			Username: "user2",
			IsAdmin:  false,
		},
	}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"user2","password":"password12345"}`))
	// Inject admin claims into context.
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{
		UserID:   uuid.New(),
		Username: "admin",
		IsAdmin:  true,
	})
	req = req.WithContext(ctx)
	h.Register(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusCreated)
	}
}

func TestRegister_ShortPassword(t *testing.T) {
	svc := &mockAuthService{userCount: 0}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"admin","password":"short"}`))
	h.Register(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRegister_EmptyUsername(t *testing.T) {
	svc := &mockAuthService{userCount: 0}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"","password":"password12345"}`))
	h.Register(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRegister_InvalidUsernameChars(t *testing.T) {
	svc := &mockAuthService{userCount: 0}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"bad user!","password":"password12345"}`))
	h.Register(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRegister_DuplicateUsername(t *testing.T) {
	svc := &mockAuthService{
		userCount:     0,
		createUserErr: ErrUserExists,
	}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"taken","password":"password12345"}`))
	h.Register(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusConflict)
	}
}

// ── SetupStatus ──────────────────────────────────────────────────────────────

func TestSetupStatus_Required(t *testing.T) {
	h := newAuthHandler(&mockAuthService{userCount: 0})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/setup/status", nil)
	h.SetupStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data struct {
			SetupRequired bool `json:"setup_required"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.Data.SetupRequired {
		t.Error("expected setup_required=true when user count is 0")
	}
}

func TestSetupStatus_NotRequired(t *testing.T) {
	h := newAuthHandler(&mockAuthService{userCount: 1})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/setup/status", nil)
	h.SetupStatus(rec, req)

	var resp struct {
		Data struct {
			SetupRequired bool `json:"setup_required"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data.SetupRequired {
		t.Error("expected setup_required=false when users exist")
	}
}

func TestSetupStatus_DBError(t *testing.T) {
	h := newAuthHandler(&mockAuthService{userCountErr: errors.New("db down")})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/setup/status", nil)
	h.SetupStatus(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── Refresh (additional coverage) ────────────────────────────────────────────

func TestRefresh_MissingToken(t *testing.T) {
	h := newAuthHandler(&mockAuthService{})

	rec := httptest.NewRecorder()
	// Empty JSON body and no cookie — should be unauthorized.
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh",
		strings.NewReader(`{}`))
	h.Refresh(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRefresh_EmptyBody_NoCookie(t *testing.T) {
	h := newAuthHandler(&mockAuthService{})

	rec := httptest.NewRecorder()
	// Completely empty body (not valid JSON) and no cookie.
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh",
		strings.NewReader(``))
	h.Refresh(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRefresh_FromCookie(t *testing.T) {
	svc := &mockAuthService{
		refreshResult: &TokenPair{AccessToken: "new-access", RefreshToken: "new-refresh"},
	}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	// Empty body, but refresh token provided via cookie.
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh",
		strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: "onscreen_rt", Value: "cookie-refresh-tok"})
	h.Refresh(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRefresh_ExpiredToken(t *testing.T) {
	svc := &mockAuthService{refreshErr: errors.New("token expired")}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh",
		strings.NewReader(`{"refresh_token":"expired-tok"}`))
	h.Refresh(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	// Verify cookies are cleared on expired token.
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "onscreen_at" && c.MaxAge < 0 {
			found = true
		}
	}
	if !found {
		t.Error("expected auth cookies to be cleared on expired refresh token")
	}
}

// ── Logout (additional coverage) ─────────────────────────────────────────────

func TestLogout_WithCookie(t *testing.T) {
	h := newAuthHandler(&mockAuthService{})

	rec := httptest.NewRecorder()
	// No body token, but refresh token via cookie.
	req := httptest.NewRequest("POST", "/api/v1/auth/logout",
		strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: "onscreen_rt", Value: "cookie-refresh-tok"})
	h.Logout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	// Verify cookies are cleared.
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "onscreen_rt" && c.MaxAge < 0 {
			found = true
		}
	}
	if !found {
		t.Error("expected refresh cookie to be cleared on logout")
	}
}

func TestLogout_NoToken(t *testing.T) {
	h := newAuthHandler(&mockAuthService{})

	rec := httptest.NewRecorder()
	// No body token and no cookie — should still return 204 and clear cookies.
	req := httptest.NewRequest("POST", "/api/v1/auth/logout",
		strings.NewReader(`{}`))
	h.Logout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestLogout_WithBodyToken(t *testing.T) {
	h := newAuthHandler(&mockAuthService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/logout",
		strings.NewReader(`{"refresh_token":"body-tok"}`))
	h.Logout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	// Verify cookies are cleared even when token comes from body.
	cookies := rec.Result().Cookies()
	cleared := 0
	for _, c := range cookies {
		if (c.Name == "onscreen_at" || c.Name == "onscreen_rt") && c.MaxAge < 0 {
			cleared++
		}
	}
	if cleared != 2 {
		t.Errorf("expected 2 cleared cookies, got %d", cleared)
	}
}

// ── Register (additional coverage) ───────────────────────────────────────────

func TestRegister_UserCountDBError(t *testing.T) {
	svc := &mockAuthService{userCountErr: errors.New("db down")}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"admin","password":"password12345"}`))
	h.Register(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestRegister_InvalidBody(t *testing.T) {
	svc := &mockAuthService{userCount: 0}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`not json`))
	h.Register(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRegister_WhitespaceOnlyUsername(t *testing.T) {
	svc := &mockAuthService{userCount: 0}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"   ","password":"password12345"}`))
	h.Register(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRegister_UsernameWithSpaces(t *testing.T) {
	svc := &mockAuthService{userCount: 0}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"has space","password":"password12345"}`))
	h.Register(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d — usernames with spaces should be rejected", rec.Code, http.StatusBadRequest)
	}
}

func TestRegister_UsernameTooShort(t *testing.T) {
	svc := &mockAuthService{userCount: 0}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"a","password":"password12345"}`))
	h.Register(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d — single-char username should be rejected", rec.Code, http.StatusBadRequest)
	}
}

func TestRegister_SubsequentUser_NonAdminForbidden(t *testing.T) {
	svc := &mockAuthService{userCount: 1}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"user2","password":"password12345"}`))
	// Inject non-admin claims into context.
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{
		UserID:   uuid.New(),
		Username: "regular",
		IsAdmin:  false,
	})
	req = req.WithContext(ctx)
	h.Register(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d — non-admin should not be able to register users", rec.Code, http.StatusForbidden)
	}
}

func TestRegister_CreateUserInternalError(t *testing.T) {
	svc := &mockAuthService{
		userCount:     0,
		createUserErr: errors.New("unexpected db error"),
	}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"admin","password":"password12345"}`))
	h.Register(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestRegister_FirstUser_IsAdminRegardlessOfInput(t *testing.T) {
	// Verify that even when is_admin is explicitly false, the first user becomes admin.
	// We check that CreateUser is called — the mock returns IsAdmin: true to confirm.
	svc := &mockAuthService{
		userCount: 0,
		createUserResult: &UserInfo{
			ID:       uuid.New(),
			Username: "firstuser",
			IsAdmin:  true,
		},
	}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"firstuser","password":"password12345","is_admin":false}`))
	h.Register(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusCreated)
	}
	// Verify the response indicates admin.
	var resp struct {
		Data UserInfo `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.Data.IsAdmin {
		t.Error("expected first user to be admin regardless of is_admin input")
	}
}

func TestRegister_AdminCreatesNonAdmin(t *testing.T) {
	svc := &mockAuthService{
		userCount: 5,
		createUserResult: &UserInfo{
			ID:       uuid.New(),
			Username: "newuser",
			IsAdmin:  false,
		},
	}
	h := newAuthHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/register",
		strings.NewReader(`{"username":"newuser","email":"new@example.com","password":"password12345","is_admin":false}`))
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{
		UserID:   uuid.New(),
		Username: "admin",
		IsAdmin:  true,
	})
	req = req.WithContext(ctx)
	h.Register(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusCreated)
	}
	var resp struct {
		Data UserInfo `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data.Username != "newuser" {
		t.Errorf("username: got %q, want %q", resp.Data.Username, "newuser")
	}
}

// ── isSecure / trusted-proxy gate ───────────────────────────────────────────

func TestIsSecure_TLSConn(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{}
	if !isSecure(r) {
		t.Error("TLS connection must be Secure")
	}
}

func TestIsSecure_PlainHTTP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.5:443"
	if isSecure(r) {
		t.Error("plain HTTP must not be Secure")
	}
}

func TestIsSecure_TrustsForwardedProtoFromLoopback(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.RemoteAddr = "127.0.0.1:55123"
	if !isSecure(r) {
		t.Error("X-Forwarded-Proto from loopback must be trusted")
	}
}

func TestIsSecure_TrustsForwardedProtoFromPrivate(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.RemoteAddr = "10.0.5.7:443"
	if !isSecure(r) {
		t.Error("X-Forwarded-Proto from RFC1918 must be trusted")
	}
}

func TestIsSecure_RejectsForwardedProtoFromPublic(t *testing.T) {
	// An attacker with a direct connection to an exposed OnScreen instance
	// must not be able to flip the Secure flag with a header.
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.RemoteAddr = "203.0.113.5:55555"
	if isSecure(r) {
		t.Error("X-Forwarded-Proto from public IP must NOT be trusted")
	}
}

func TestClearAuthCookies_MatchesSetAttributes(t *testing.T) {
	// Browsers will silently ignore a deletion cookie whose Path/Secure/SameSite
	// don't match the cookie that was originally set. Logout would then leave
	// the access cookie alive until its TTL expires.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	r.RemoteAddr = "127.0.0.1:55555"
	r.Header.Set("X-Forwarded-Proto", "https")
	clearAuthCookies(w, r)

	cookies := w.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("expected 2 cookies, got %d", len(cookies))
	}
	for _, c := range cookies {
		if c.MaxAge != -1 {
			t.Errorf("%s: MaxAge: got %d, want -1", c.Name, c.MaxAge)
		}
		if !c.HttpOnly {
			t.Errorf("%s: must be HttpOnly", c.Name)
		}
		if !c.Secure {
			t.Errorf("%s: must be Secure when X-Forwarded-Proto=https from loopback", c.Name)
		}
	}
}
