package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/email"
)

// ── mock PasswordResetDB ─────────────────────────────────────────────────────

type mockPasswordResetDB struct {
	// GetUserByEmail
	user    PRUser
	userErr error

	// CreateResetToken
	createTokenErr    error
	createTokenCalled bool

	// GetResetToken
	token    PRToken
	tokenErr error

	// MarkResetTokenUsed
	markUsedErr    error
	markUsedCalled bool

	// UpdatePassword
	updatePwErr    error
	updatePwCalled bool
	updatePwHash   string
	updatePwUserID uuid.UUID
}

func (m *mockPasswordResetDB) GetUserByEmail(_ context.Context, _ *string) (PRUser, error) {
	return m.user, m.userErr
}

func (m *mockPasswordResetDB) CreateResetToken(_ context.Context, _ uuid.UUID, _ string, _ time.Time) error {
	m.createTokenCalled = true
	return m.createTokenErr
}

func (m *mockPasswordResetDB) GetResetToken(_ context.Context, _ string) (PRToken, error) {
	return m.token, m.tokenErr
}

func (m *mockPasswordResetDB) MarkResetTokenUsed(_ context.Context, _ uuid.UUID) error {
	m.markUsedCalled = true
	return m.markUsedErr
}

func (m *mockPasswordResetDB) UpdatePassword(_ context.Context, userID uuid.UUID, hash string) error {
	m.updatePwCalled = true
	m.updatePwUserID = userID
	m.updatePwHash = hash
	return m.updatePwErr
}

// ── helpers ──────────────────────────────────────────────────────────────────

// dummySender creates a real *email.Sender with localhost config.
// It won't actually send (SMTP connect will fail) but is non-nil.
func dummySender() *email.Sender {
	return email.NewSender(email.Config{
		Host: "localhost",
		Port: 1025,
		From: "test@test.com",
	})
}

func newPRHandler(db PasswordResetDB, sender *email.Sender) *PasswordResetHandler {
	return NewPasswordResetHandler(db, sender, "http://localhost:3000", slog.Default())
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type dataEnvelope struct {
	Data map[string]any `json:"data"`
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	return env
}

func decodeData(t *testing.T, rec *httptest.ResponseRecorder) dataEnvelope {
	t.Helper()
	var env dataEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal data response: %v", err)
	}
	return env
}

// ── ForgotPassword ───────────────────────────────────────────────────────────

func TestForgotPassword_Success(t *testing.T) {
	uid := uuid.New()
	e := "alice@example.com"
	db := &mockPasswordResetDB{
		user: PRUser{ID: uid, Username: "alice", Email: &e},
	}
	h := newPRHandler(db, dummySender())

	body := `{"email":"alice@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ForgotPassword(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	data := decodeData(t, rec)
	msg, _ := data.Data["message"].(string)
	if !strings.Contains(msg, "If an account") {
		t.Errorf("unexpected message: %q", msg)
	}
	if !db.createTokenCalled {
		t.Error("expected CreateResetToken to be called")
	}
}

func TestForgotPassword_UserNotFound(t *testing.T) {
	db := &mockPasswordResetDB{
		userErr: errors.New("no rows"),
	}
	h := newPRHandler(db, dummySender())

	body := `{"email":"nobody@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ForgotPassword(rec, req)

	// Must still return 200 to prevent email enumeration.
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	data := decodeData(t, rec)
	msg, _ := data.Data["message"].(string)
	if !strings.Contains(msg, "If an account") {
		t.Errorf("unexpected message: %q", msg)
	}
	if db.createTokenCalled {
		t.Error("CreateResetToken should not be called when user not found")
	}
}

func TestForgotPassword_SMTPNotConfigured(t *testing.T) {
	db := &mockPasswordResetDB{}
	h := newPRHandler(db, nil) // sender is nil

	body := `{"email":"alice@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ForgotPassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
	env := decodeError(t, rec)
	if !strings.Contains(env.Error.Message, "not configured") {
		t.Errorf("unexpected error message: %q", env.Error.Message)
	}
}

func TestForgotPassword_EmptyEmail(t *testing.T) {
	h := newPRHandler(&mockPasswordResetDB{}, dummySender())

	body := `{"email":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ForgotPassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
	env := decodeError(t, rec)
	if !strings.Contains(env.Error.Message, "email is required") {
		t.Errorf("unexpected error message: %q", env.Error.Message)
	}
}

// ── ResetPassword ────────────────────────────────────────────────────────────

func TestResetPassword_Success(t *testing.T) {
	uid := uuid.New()
	tokenID := uuid.New()
	rawToken := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	hash := sha256.Sum256([]byte(rawToken))
	_ = hex.EncodeToString(hash[:]) // the handler will hash it internally

	db := &mockPasswordResetDB{
		token: PRToken{ID: tokenID, UserID: uid},
	}
	h := newPRHandler(db, nil)

	body := `{"token":"` + rawToken + `","password":"newpassword123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ResetPassword(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	data := decodeData(t, rec)
	msg, _ := data.Data["message"].(string)
	if !strings.Contains(msg, "Password has been reset") {
		t.Errorf("unexpected message: %q", msg)
	}
	if !db.updatePwCalled {
		t.Error("expected UpdatePassword to be called")
	}
	if db.updatePwUserID != uid {
		t.Errorf("UpdatePassword userID: got %s, want %s", db.updatePwUserID, uid)
	}
	if !db.markUsedCalled {
		t.Error("expected MarkResetTokenUsed to be called")
	}
}

func TestResetPassword_InvalidToken(t *testing.T) {
	db := &mockPasswordResetDB{
		tokenErr: errors.New("no rows"),
	}
	h := newPRHandler(db, nil)

	body := `{"token":"invalidtoken","password":"newpassword123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ResetPassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
	env := decodeError(t, rec)
	if !strings.Contains(env.Error.Message, "Invalid or expired") {
		t.Errorf("unexpected error message: %q", env.Error.Message)
	}
	if db.updatePwCalled {
		t.Error("UpdatePassword should not be called for invalid token")
	}
}

func TestResetPassword_PasswordTooShort(t *testing.T) {
	h := newPRHandler(&mockPasswordResetDB{}, nil)

	body := `{"token":"sometoken","password":"short"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ResetPassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
	env := decodeError(t, rec)
	if !strings.Contains(env.Error.Message, "at least 12 characters") {
		t.Errorf("unexpected error message: %q", env.Error.Message)
	}
}

func TestResetPassword_MissingToken(t *testing.T) {
	h := newPRHandler(&mockPasswordResetDB{}, nil)

	body := `{"password":"newpassword123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ResetPassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
	env := decodeError(t, rec)
	if !strings.Contains(env.Error.Message, "token and password are required") {
		t.Errorf("unexpected error message: %q", env.Error.Message)
	}
}

func TestResetPassword_MissingPassword(t *testing.T) {
	h := newPRHandler(&mockPasswordResetDB{}, nil)

	body := `{"token":"sometoken"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ResetPassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
	env := decodeError(t, rec)
	if !strings.Contains(env.Error.Message, "token and password are required") {
		t.Errorf("unexpected error message: %q", env.Error.Message)
	}
}

func TestResetPassword_EmptyBody(t *testing.T) {
	h := newPRHandler(&mockPasswordResetDB{}, nil)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ResetPassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ── Enabled ──────────────────────────────────────────────────────────────────

func TestEnabled_WithSender(t *testing.T) {
	h := newPRHandler(&mockPasswordResetDB{}, dummySender())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/forgot-password/enabled", nil)
	rec := httptest.NewRecorder()

	h.Enabled(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data struct {
			Enabled bool `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Data.Enabled {
		t.Error("expected enabled=true when sender is configured")
	}
}

func TestEnabled_WithoutSender(t *testing.T) {
	h := newPRHandler(&mockPasswordResetDB{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/forgot-password/enabled", nil)
	rec := httptest.NewRecorder()

	h.Enabled(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data struct {
			Enabled bool `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Enabled {
		t.Error("expected enabled=false when sender is nil")
	}
}
