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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/email"
)

// ── mock PasswordResetDB ─────────────────────────────────────────────────────

type mockPasswordResetDB struct {
	// invalidatedFor records InvalidateUserResetTokens calls.
	invalidatedFor []uuid.UUID

	// mu guards the *Called flags. ForgotPassword now does its token
	// creation + SMTP send on a detached goroutine (the synchronous version
	// leaked account existence through response latency), so the test
	// goroutine and the handler's goroutine touch these concurrently.
	mu sync.Mutex

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
	// markUsedNotWon=true simulates "another caller already claimed
	// this token" — the conditional UPDATE returned 0 rows. Default
	// (false) means this caller wins the race.
	markUsedNotWon bool

	// UpdateLocalPassword
	updatePwErr    error
	updatePwCalled bool
	updatePwHash   string
	updatePwUserID uuid.UUID
	// noLocalPassword makes UpdateLocalPassword report an account without a
	// local password (SSO / LDAP only, a managed profile): no row updated.
	noLocalPassword bool

	// DeleteAPIKeysForUser
	apiKeysDeletedFor []uuid.UUID
	apiKeysToDelete   int64
	deleteAPIKeysErr  error

	// BumpSessionEpoch + DeleteSessionsForUser — together revoke every
	// outstanding credential after a successful password reset.
	bumpEpochErr    error
	bumpEpochCalled bool
	bumpEpochUserID uuid.UUID

	deleteSessionsErr    error
	deleteSessionsCalled bool
	deleteSessionsUserID uuid.UUID
}

func (m *mockPasswordResetDB) GetUserByEmail(_ context.Context, _ *string) (PRUser, error) {
	return m.user, m.userErr
}

func (m *mockPasswordResetDB) CreateResetToken(_ context.Context, _ uuid.UUID, _ string, _ time.Time) error {
	m.mu.Lock()
	m.createTokenCalled = true
	m.mu.Unlock()
	return m.createTokenErr
}

func (m *mockPasswordResetDB) GetResetToken(_ context.Context, _ string) (PRToken, error) {
	return m.token, m.tokenErr
}

func (m *mockPasswordResetDB) InvalidateUserResetTokens(_ context.Context, uid uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invalidatedFor = append(m.invalidatedFor, uid)
	return nil
}

func (m *mockPasswordResetDB) MarkResetTokenUsed(_ context.Context, _ uuid.UUID) (bool, error) {
	m.markUsedCalled = true
	if m.markUsedErr != nil {
		return false, m.markUsedErr
	}
	// Default = won (common case). Tests exercising the race-loser
	// path set markUsedNotWon=true explicitly.
	return !m.markUsedNotWon, nil
}

func (m *mockPasswordResetDB) UpdateLocalPassword(_ context.Context, userID uuid.UUID, hash string) (bool, error) {
	m.updatePwCalled = true
	m.updatePwUserID = userID
	if m.updatePwErr != nil {
		return false, m.updatePwErr
	}
	if m.noLocalPassword {
		return false, nil
	}
	m.updatePwHash = hash
	return true, nil
}

func (m *mockPasswordResetDB) DeleteAPIKeysForUser(_ context.Context, userID uuid.UUID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apiKeysDeletedFor = append(m.apiKeysDeletedFor, userID)
	return m.apiKeysToDelete, m.deleteAPIKeysErr
}

func (m *mockPasswordResetDB) BumpSessionEpoch(_ context.Context, userID uuid.UUID) error {
	m.bumpEpochCalled = true
	m.bumpEpochUserID = userID
	return m.bumpEpochErr
}

func (m *mockPasswordResetDB) DeleteSessionsForUser(_ context.Context, userID uuid.UUID) error {
	m.deleteSessionsCalled = true
	m.deleteSessionsUserID = userID
	return m.deleteSessionsErr
}

// ── helpers ──────────────────────────────────────────────────────────────────

// dummySender returns a Sender whose ConfigFunc reports a complete + enabled
// SMTP config. Send won't actually deliver (localhost:1025 dial will fail),
// but Enabled(ctx) is true so handlers proceed past the gate.
func dummySender() *email.Sender {
	return email.NewSender(func(context.Context) email.Config {
		return email.Config{
			Enabled: true,
			Host:    "localhost",
			Port:    1025,
			From:    "test@test.com",
		}
	})
}

// disabledSender returns a Sender whose ConfigFunc reports SMTP off — used
// to exercise the "not configured" handler branch.
func disabledSender() *email.Sender {
	return email.NewSender(func(context.Context) email.Config { return email.Config{} })
}

func newPRHandler(db PasswordResetDB, sender *email.Sender) *PasswordResetHandler {
	if sender == nil {
		// Production wiring guarantees a non-nil sender; tests that pass nil
		// only exercise paths (e.g. ResetPassword) that don't touch SMTP.
		sender = disabledSender()
	}
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

// tokenCreated reads the flag under the lock.
func (m *mockPasswordResetDB) tokenCreated() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createTokenCalled
}

// waitTokenCreated polls for the detached send path to reach CreateResetToken.
// Returns false if it never does within the budget.
func (m *mockPasswordResetDB) waitTokenCreated(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if m.tokenCreated() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

func TestForgotPassword_Success(t *testing.T) {
	uid := uuid.New()
	e := "alice@example.com"
	db := &mockPasswordResetDB{
		user: PRUser{ID: uid, Username: "alice", Email: &e, HasLocalPassword: true},
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
	// Async now: the response is written before the work starts, so that the
	// reply latency is identical for a known and an unknown address.
	if !db.waitTokenCreated(2 * time.Second) {
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
	time.Sleep(50 * time.Millisecond) // let any detached send goroutine run
	if db.tokenCreated() {
		t.Error("CreateResetToken should not be called when user not found")
	}
}

func TestForgotPassword_SMTPNotConfigured(t *testing.T) {
	// Hardened response shape: SMTP-disabled servers must respond
	// identically to "user not found" so an unauthenticated probe
	// can't distinguish "this server has email enabled" from "this
	// account exists." (Was 400 with a leaky message; now 200 with
	// the same generic body the success / user-not-found paths
	// return.)
	db := &mockPasswordResetDB{}
	h := newPRHandler(db, disabledSender())

	body := `{"email":"alice@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ForgotPassword(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	time.Sleep(50 * time.Millisecond) // let any detached send goroutine run
	if db.tokenCreated() {
		t.Error("CreateResetToken should NOT be called when SMTP disabled")
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
	// Every OTHER outstanding reset link for the user must die with this reset.
	if len(db.invalidatedFor) != 1 || db.invalidatedFor[0] != uid {
		t.Errorf("expected InvalidateUserResetTokens(%s) once; got %v", uid, db.invalidatedFor)
	}
	// The recovery revokes personal API keys too: a key minted from a stolen
	// session must not outlive the reset.
	if len(db.apiKeysDeletedFor) != 1 || db.apiKeysDeletedFor[0] != uid {
		t.Errorf("expected DeleteAPIKeysForUser(%s) once; got %v", uid, db.apiKeysDeletedFor)
	}
}

// S3: an account with no local password (single sign-on / LDAP only, or a
// managed profile) gets no reset link — it would hand the account a password
// that skips the identity provider — and the response is the same as for any
// address.
func TestForgotPassword_NoLocalPasswordSendsNothing(t *testing.T) {
	e := "sso-user@example.com"
	db := &mockPasswordResetDB{
		user: PRUser{ID: uuid.New(), Username: "sso_user", Email: &e, HasLocalPassword: false},
	}
	h := newPRHandler(db, dummySender())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(`{"email":"sso-user@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ForgotPassword(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (same answer as any address)", rec.Code)
	}
	msg, _ := decodeData(t, rec).Data["message"].(string)
	if !strings.Contains(msg, "If an account") {
		t.Errorf("unexpected message: %q", msg)
	}
	if db.waitTokenCreated(100 * time.Millisecond) {
		t.Error("a reset token was created for an account with no local password")
	}
}

// S3, confirm time: a link minted before the rule (or before the account moved
// to single sign-on) must not set a password on an account that has none. The
// link is burned and the answer is the generic dead-link one; no credential is
// revoked, since nothing changed.
func TestResetPassword_RefusesAccountWithoutLocalPassword(t *testing.T) {
	uid := uuid.New()
	db := &mockPasswordResetDB{
		token:           PRToken{ID: uuid.New(), UserID: uid},
		noLocalPassword: true,
	}
	h := newPRHandler(db, nil)

	body := `{"token":"some-old-token","password":"newpassword123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ResetPassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
	if env := decodeError(t, rec); !strings.Contains(env.Error.Message, "Invalid or expired") {
		t.Errorf("message = %q, want the generic dead-link message", env.Error.Message)
	}
	if !db.markUsedCalled {
		t.Error("the link should be claimed (burned)")
	}
	if db.updatePwHash != "" {
		t.Error("a password hash was stored for an account with no local password")
	}
	if len(db.invalidatedFor) != 1 || db.invalidatedFor[0] != uid {
		t.Errorf("the account's other links should be burned too; got %v", db.invalidatedFor)
	}
	if db.bumpEpochCalled || db.deleteSessionsCalled || len(db.apiKeysDeletedFor) != 0 {
		t.Error("nothing changed, so nothing should be revoked")
	}
}

// A failed API-key revocation fails the reset loudly (500), like the other
// revocation steps: the caller must know a credential may still be live.
func TestResetPassword_APIKeyRevokeFailureIs500(t *testing.T) {
	db := &mockPasswordResetDB{
		token:            PRToken{ID: uuid.New(), UserID: uuid.New()},
		deleteAPIKeysErr: errors.New("db down"),
	}
	h := newPRHandler(db, nil)
	body := `{"token":"tok","password":"newpassword123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ResetPassword(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
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
