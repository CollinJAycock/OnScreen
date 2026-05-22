package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
)

type mockTOTPService struct {
	setupURL, setupSecret string
	setupErr              error
	activateCodes        []string
	activateErr          error
	disableErr           error
	verifyResult         *TokenPair
	verifyErr            error
	statusEnabled        bool
	statusRemaining      int
	statusErr            error
}

func (m *mockTOTPService) SetupTOTP(_ context.Context, _ uuid.UUID, _ string) (string, string, error) {
	return m.setupURL, m.setupSecret, m.setupErr
}
func (m *mockTOTPService) ActivateTOTP(_ context.Context, _ uuid.UUID, _ string) ([]string, error) {
	return m.activateCodes, m.activateErr
}
func (m *mockTOTPService) DisableTOTP(_ context.Context, _ uuid.UUID, _ string) error {
	return m.disableErr
}
func (m *mockTOTPService) VerifyTOTPLogin(_ context.Context, _, _ string) (*TokenPair, error) {
	if m.verifyErr != nil {
		return nil, m.verifyErr
	}
	return m.verifyResult, nil
}
func (m *mockTOTPService) TOTPStatus(_ context.Context, _ uuid.UUID) (bool, int, error) {
	return m.statusEnabled, m.statusRemaining, m.statusErr
}

func totpReq(method, path, body string, withClaims bool) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if withClaims {
		claims := &auth.Claims{UserID: uuid.New(), Username: "alice"}
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	}
	return req
}

func newTOTPHandler(svc TOTPService) *TOTPHandler {
	return NewTOTPHandler(svc, slog.Default())
}

func TestTOTP_Setup_AlreadyEnabled409(t *testing.T) {
	h := newTOTPHandler(&mockTOTPService{setupErr: ErrTOTPAlreadyEnabled})
	rec := httptest.NewRecorder()
	h.Setup(rec, totpReq("POST", "/auth/totp/setup", "{}", true))
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestTOTP_Setup_Success(t *testing.T) {
	h := newTOTPHandler(&mockTOTPService{setupURL: "otpauth://totp/x", setupSecret: "SECRET"})
	rec := httptest.NewRecorder()
	h.Setup(rec, totpReq("POST", "/auth/totp/setup", "{}", true))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Data struct {
			OtpauthURL string `json:"otpauth_url"`
			Secret     string `json:"secret"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data.OtpauthURL != "otpauth://totp/x" || resp.Data.Secret != "SECRET" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestTOTP_Setup_Unauthenticated401(t *testing.T) {
	h := newTOTPHandler(&mockTOTPService{})
	rec := httptest.NewRecorder()
	h.Setup(rec, totpReq("POST", "/auth/totp/setup", "{}", false)) // no claims
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestTOTP_Activate_BadCode400(t *testing.T) {
	h := newTOTPHandler(&mockTOTPService{activateErr: ErrBadTOTPCode})
	rec := httptest.NewRecorder()
	h.Activate(rec, totpReq("POST", "/auth/totp/activate", `{"code":"000000"}`, true))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestTOTP_Activate_Success_ReturnsRecoveryCodes(t *testing.T) {
	h := newTOTPHandler(&mockTOTPService{activateCodes: []string{"AAAAA-BBBBB", "CCCCC-DDDDD"}})
	rec := httptest.NewRecorder()
	h.Activate(rec, totpReq("POST", "/auth/totp/activate", `{"code":"123456"}`, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "AAAAA-BBBBB") {
		t.Errorf("recovery codes missing from body: %s", rec.Body.String())
	}
}

func TestTOTP_Verify_BadCode401(t *testing.T) {
	h := newTOTPHandler(&mockTOTPService{verifyErr: ErrBadTOTPCode})
	rec := httptest.NewRecorder()
	h.Verify(rec, totpReq("POST", "/auth/totp/verify", `{"login_challenge_token":"t","code":"000000"}`, false))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestTOTP_Verify_InvalidChallenge401(t *testing.T) {
	h := newTOTPHandler(&mockTOTPService{verifyErr: ErrInvalidTOTPChallenge})
	rec := httptest.NewRecorder()
	h.Verify(rec, totpReq("POST", "/auth/totp/verify", `{"login_challenge_token":"bad","code":"123456"}`, false))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestTOTP_Verify_Success_SetsCookieAndReturnsPair(t *testing.T) {
	h := newTOTPHandler(&mockTOTPService{verifyResult: &TokenPair{
		AccessToken: "at", RefreshToken: "rt", UserID: uuid.New(), Username: "alice",
	}})
	rec := httptest.NewRecorder()
	h.Verify(rec, totpReq("POST", "/auth/totp/verify", `{"login_challenge_token":"t","code":"123456"}`, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Error("expected auth cookies to be set on successful verify")
	}
	if !strings.Contains(rec.Body.String(), `"access_token":"at"`) {
		t.Errorf("token pair missing from body: %s", rec.Body.String())
	}
}

func TestTOTP_Disable_BadCode400(t *testing.T) {
	h := newTOTPHandler(&mockTOTPService{disableErr: ErrBadTOTPCode})
	rec := httptest.NewRecorder()
	h.Disable(rec, totpReq("POST", "/auth/totp/disable", `{"code":"000000"}`, true))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestTOTP_Status_ReportsEnabledAndRemaining(t *testing.T) {
	h := newTOTPHandler(&mockTOTPService{statusEnabled: true, statusRemaining: 7})
	rec := httptest.NewRecorder()
	h.Status(rec, totpReq("GET", "/auth/totp/status", "", true))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"enabled":true`) || !strings.Contains(body, `"recovery_codes_remaining":7`) {
		t.Errorf("unexpected status body: %s", body)
	}
}
