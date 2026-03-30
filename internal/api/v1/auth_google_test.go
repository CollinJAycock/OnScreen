package v1

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── deriveUsername ────────────────────────────────────────────────────────────

func TestDeriveUsername_FromName(t *testing.T) {
	u := deriveUsername("Alice Smith", "alice@example.com")
	if !strings.HasPrefix(u, "Alice_Smith_") {
		t.Errorf("expected prefix 'Alice_Smith_', got %q", u)
	}
	if len(u) > 32 {
		t.Errorf("username too long: %d chars", len(u))
	}
}

func TestDeriveUsername_FromEmail(t *testing.T) {
	u := deriveUsername("", "bob@example.com")
	if !strings.HasPrefix(u, "bob_") {
		t.Errorf("expected prefix 'bob_', got %q", u)
	}
}

func TestDeriveUsername_EmptyBoth(t *testing.T) {
	u := deriveUsername("", "")
	// Empty email prefix => "", sanitized => "", trimmed => "", len<2 => "user_"
	if !strings.HasPrefix(u, "user__") {
		t.Errorf("expected prefix 'user__', got %q", u)
	}
	if len(u) > 32 {
		t.Errorf("username too long: %d chars", len(u))
	}
}

func TestDeriveUsername_LongName(t *testing.T) {
	long := strings.Repeat("a", 100)
	u := deriveUsername(long, "x@example.com")
	if len(u) > 32 {
		t.Errorf("username too long: %d chars", len(u))
	}
}

func TestDeriveUsername_SpecialChars(t *testing.T) {
	u := deriveUsername("O'Brien-Jones!", "ob@example.com")
	// Special chars replaced with _
	if strings.ContainsAny(u, "'!-") {
		t.Errorf("username contains special chars: %q", u)
	}
	if len(u) > 32 {
		t.Errorf("username too long: %d chars", len(u))
	}
}

func TestDeriveUsername_ShortName(t *testing.T) {
	u := deriveUsername("A", "a@example.com")
	// "A" is 1 char after sanitize => "user_A" prefix
	if !strings.HasPrefix(u, "user_A_") {
		t.Errorf("expected prefix 'user_A_', got %q", u)
	}
}

func TestDeriveUsername_Uniqueness(t *testing.T) {
	u1 := deriveUsername("alice", "alice@example.com")
	u2 := deriveUsername("alice", "alice@example.com")
	if u1 == u2 {
		t.Error("expected different usernames due to random suffix")
	}
}

// ── parseIDTokenClaims ───────────────────────────────────────────────────────

func makeJWT(payload string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString([]byte("fakesig"))
	return header + "." + body + "." + sig
}

func TestParseIDTokenClaims_Valid(t *testing.T) {
	token := makeJWT(`{"sub":"123","email":"a@b.com","name":"Alice"}`)
	claims, err := parseIDTokenClaims(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claims["sub"] != "123" {
		t.Errorf("sub: got %v, want 123", claims["sub"])
	}
	if claims["email"] != "a@b.com" {
		t.Errorf("email: got %v, want a@b.com", claims["email"])
	}
}

func TestParseIDTokenClaims_InvalidParts(t *testing.T) {
	_, err := parseIDTokenClaims("only.two")
	if err == nil {
		t.Fatal("expected error for 2-part token")
	}
	if !strings.Contains(err.Error(), "expected 3 parts") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseIDTokenClaims_MalformedBase64(t *testing.T) {
	_, err := parseIDTokenClaims("a.!!!invalid!!!.c")
	if err == nil {
		t.Fatal("expected error for malformed base64")
	}
}

func TestParseIDTokenClaims_InvalidJSON(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(`not json`))
	sig := base64.RawURLEncoding.EncodeToString([]byte(`sig`))
	_, err := parseIDTokenClaims(header + "." + body + "." + sig)
	if err == nil {
		t.Fatal("expected error for invalid JSON payload")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseIDTokenClaims_EmptyToken(t *testing.T) {
	_, err := parseIDTokenClaims("")
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

// ── GoogleDisabledHandler ────────────────────────────────────────────────────

func TestGoogleDisabledHandler(t *testing.T) {
	h := GoogleDisabledHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/auth/google/enabled", nil)
	h.ServeHTTP(rec, req)

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
		t.Error("expected enabled=false")
	}
}

// ── GoogleOAuthHandler.Enabled ───────────────────────────────────────────────

func TestGoogleEnabledHandler(t *testing.T) {
	h := NewGoogleOAuthHandler("my-client-id", "secret", "http://localhost", &mockGoogleAuthService{}, slog.Default())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/auth/google/enabled", nil)
	h.Enabled(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data struct {
			Enabled  bool   `json:"enabled"`
			ClientID string `json:"client_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Data.Enabled {
		t.Error("expected enabled=true")
	}
	if resp.Data.ClientID != "my-client-id" {
		t.Errorf("client_id: got %q, want %q", resp.Data.ClientID, "my-client-id")
	}
}

// ── mock GoogleAuthService ───────────────────────────────────────────────────

type mockGoogleAuthService struct {
	result *TokenPair
	err    error
}

func (m *mockGoogleAuthService) LoginOrCreateGoogleUser(_ context.Context, _, _, _, _ string) (*TokenPair, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

// ── mock GoogleOAuthDB ───────────────────────────────────────────────────────

type mockGoogleOAuthDB struct {
	// GetUserByGoogleID
	googleIDUser gen.User
	googleIDErr  error

	// GetUserByEmail
	emailUser gen.User
	emailErr  error

	// LinkGoogleAccount
	linkErr    error
	linkCalled bool

	// CreateGoogleUser
	createUser gen.User
	createErr  error

	// CountUsers
	count    int64
	countErr error
}

func (m *mockGoogleOAuthDB) GetUserByGoogleID(_ context.Context, _ *string) (gen.User, error) {
	return m.googleIDUser, m.googleIDErr
}
func (m *mockGoogleOAuthDB) GetUserByEmail(_ context.Context, _ *string) (gen.User, error) {
	return m.emailUser, m.emailErr
}
func (m *mockGoogleOAuthDB) LinkGoogleAccount(_ context.Context, _ gen.LinkGoogleAccountParams) error {
	m.linkCalled = true
	return m.linkErr
}
func (m *mockGoogleOAuthDB) CreateGoogleUser(_ context.Context, _ gen.CreateGoogleUserParams) (gen.User, error) {
	return m.createUser, m.createErr
}
func (m *mockGoogleOAuthDB) CountUsers(_ context.Context) (int64, error) {
	return m.count, m.countErr
}

// ── LoginOrCreateGoogleUser (service layer) ──────────────────────────────────

func fakeIssueTokens(_ context.Context, user gen.User) (*TokenPair, error) {
	return &TokenPair{
		AccessToken:  "at-" + user.Username,
		RefreshToken: "rt-" + user.Username,
		UserID:       user.ID,
		Username:     user.Username,
	}, nil
}

func TestLoginOrCreateGoogleUser_ExistingGoogleUser(t *testing.T) {
	uid := uuid.New()
	db := &mockGoogleOAuthDB{
		googleIDUser: gen.User{ID: uid, Username: "alice"},
		googleIDErr:  nil,
	}
	svc := NewGoogleAuthService(db, fakeIssueTokens, slog.Default())

	pair, err := svc.LoginOrCreateGoogleUser(context.Background(), "gid-123", "alice@example.com", "Alice", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.Username != "alice" {
		t.Errorf("username: got %q, want %q", pair.Username, "alice")
	}
	if pair.UserID != uid {
		t.Errorf("user_id mismatch")
	}
}

func TestLoginOrCreateGoogleUser_LinkExistingEmail(t *testing.T) {
	uid := uuid.New()
	db := &mockGoogleOAuthDB{
		googleIDErr: pgx.ErrNoRows,
		emailUser:   gen.User{ID: uid, Username: "bob"},
		emailErr:    nil,
	}
	svc := NewGoogleAuthService(db, fakeIssueTokens, slog.Default())

	pair, err := svc.LoginOrCreateGoogleUser(context.Background(), "gid-456", "bob@example.com", "Bob", "http://avatar.url")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.Username != "bob" {
		t.Errorf("username: got %q, want %q", pair.Username, "bob")
	}
	if !db.linkCalled {
		t.Error("expected LinkGoogleAccount to be called")
	}
}

func TestLoginOrCreateGoogleUser_LinkExistingEmail_NoAvatar(t *testing.T) {
	uid := uuid.New()
	db := &mockGoogleOAuthDB{
		googleIDErr: pgx.ErrNoRows,
		emailUser:   gen.User{ID: uid, Username: "carol"},
		emailErr:    nil,
	}
	svc := NewGoogleAuthService(db, fakeIssueTokens, slog.Default())

	pair, err := svc.LoginOrCreateGoogleUser(context.Background(), "gid-789", "carol@example.com", "Carol", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.Username != "carol" {
		t.Errorf("username: got %q, want %q", pair.Username, "carol")
	}
}

func TestLoginOrCreateGoogleUser_CreateNewUser(t *testing.T) {
	uid := uuid.New()
	db := &mockGoogleOAuthDB{
		googleIDErr: pgx.ErrNoRows,
		emailErr:    pgx.ErrNoRows,
		count:       0, // First user => admin
		createUser:  gen.User{ID: uid, Username: "newuser_abc123", IsAdmin: true},
	}
	svc := NewGoogleAuthService(db, fakeIssueTokens, slog.Default())

	pair, err := svc.LoginOrCreateGoogleUser(context.Background(), "gid-new", "new@example.com", "New User", "http://pic.url")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.UserID != uid {
		t.Error("user_id mismatch")
	}
}

func TestLoginOrCreateGoogleUser_CreateNewUser_NotAdmin(t *testing.T) {
	uid := uuid.New()
	db := &mockGoogleOAuthDB{
		googleIDErr: pgx.ErrNoRows,
		emailErr:    pgx.ErrNoRows,
		count:       5, // Not first user => not admin
		createUser:  gen.User{ID: uid, Username: "seconduser_abc", IsAdmin: false},
	}
	svc := NewGoogleAuthService(db, fakeIssueTokens, slog.Default())

	pair, err := svc.LoginOrCreateGoogleUser(context.Background(), "gid-second", "second@example.com", "Second", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.UserID != uid {
		t.Error("user_id mismatch")
	}
}

func TestLoginOrCreateGoogleUser_GoogleIDLookupError(t *testing.T) {
	db := &mockGoogleOAuthDB{
		googleIDErr: errors.New("db connection failed"),
	}
	svc := NewGoogleAuthService(db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginOrCreateGoogleUser(context.Background(), "gid", "a@b.com", "A", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "get user by google_id") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoginOrCreateGoogleUser_EmailLookupError(t *testing.T) {
	db := &mockGoogleOAuthDB{
		googleIDErr: pgx.ErrNoRows,
		emailErr:    errors.New("db timeout"),
	}
	svc := NewGoogleAuthService(db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginOrCreateGoogleUser(context.Background(), "gid", "a@b.com", "A", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "get user by email") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoginOrCreateGoogleUser_CountError(t *testing.T) {
	db := &mockGoogleOAuthDB{
		googleIDErr: pgx.ErrNoRows,
		emailErr:    pgx.ErrNoRows,
		countErr:    errors.New("count failed"),
	}
	svc := NewGoogleAuthService(db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginOrCreateGoogleUser(context.Background(), "gid", "a@b.com", "A", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "count users") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoginOrCreateGoogleUser_CreateError(t *testing.T) {
	db := &mockGoogleOAuthDB{
		googleIDErr: pgx.ErrNoRows,
		emailErr:    pgx.ErrNoRows,
		count:       0,
		createErr:   fmt.Errorf("unique constraint"),
	}
	svc := NewGoogleAuthService(db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginOrCreateGoogleUser(context.Background(), "gid", "a@b.com", "A", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "create google user") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoginOrCreateGoogleUser_LinkError_StillSucceeds(t *testing.T) {
	uid := uuid.New()
	db := &mockGoogleOAuthDB{
		googleIDErr: pgx.ErrNoRows,
		emailUser:   gen.User{ID: uid, Username: "dave"},
		emailErr:    nil,
		linkErr:     errors.New("link failed"),
	}
	svc := NewGoogleAuthService(db, fakeIssueTokens, slog.Default())

	// Link error is logged but login still succeeds.
	pair, err := svc.LoginOrCreateGoogleUser(context.Background(), "gid", "dave@example.com", "Dave", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.Username != "dave" {
		t.Errorf("username: got %q, want %q", pair.Username, "dave")
	}
}
