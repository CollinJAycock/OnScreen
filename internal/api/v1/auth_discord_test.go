package v1

import (
	"context"
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

// ── DiscordDisabledHandler ───────────────────────────────────────────────────

func TestDiscordDisabledHandler(t *testing.T) {
	h := DiscordDisabledHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/auth/discord/enabled", nil)
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

// ── mock DiscordOAuthDB ──────────────────────────────────────────────────────

type mockDiscordOAuthDB struct {
	// GetUserByDiscordID
	discordIDUser gen.User
	discordIDErr  error

	// GetUserByEmail
	emailUser gen.User
	emailErr  error

	// LinkDiscordAccount
	linkErr    error
	linkCalled bool

	// CreateDiscordUser
	createUser gen.User
	createErr  error

	// CountUsers
	count    int64
	countErr error
}

func (m *mockDiscordOAuthDB) GetUserByDiscordID(_ context.Context, _ *string) (gen.User, error) {
	return m.discordIDUser, m.discordIDErr
}
func (m *mockDiscordOAuthDB) GetUserByEmail(_ context.Context, _ *string) (gen.User, error) {
	return m.emailUser, m.emailErr
}
func (m *mockDiscordOAuthDB) LinkDiscordAccount(_ context.Context, _ gen.LinkDiscordAccountParams) error {
	m.linkCalled = true
	return m.linkErr
}
func (m *mockDiscordOAuthDB) CreateDiscordUser(_ context.Context, _ gen.CreateDiscordUserParams) (gen.User, error) {
	return m.createUser, m.createErr
}
func (m *mockDiscordOAuthDB) CountUsers(_ context.Context) (int64, error) {
	return m.count, m.countErr
}

// ── discordAuthService.loginOrCreate ─────────────────────────────────────────

func TestDiscordLoginOrCreate_ExistingDiscordUser(t *testing.T) {
	uid := uuid.New()
	db := &mockDiscordOAuthDB{
		discordIDUser: gen.User{ID: uid, Username: "alice"},
		discordIDErr:  nil,
	}
	svc := &discordAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	pair, err := svc.loginOrCreate(context.Background(), &discordUserInfo{
		ID: "123456", Username: "alice_dc", GlobalName: "Alice", Email: "alice@example.com", Verified: true,
	})
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

func TestDiscordLoginOrCreate_LinkExistingEmail(t *testing.T) {
	uid := uuid.New()
	db := &mockDiscordOAuthDB{
		discordIDErr: pgx.ErrNoRows,
		emailUser:    gen.User{ID: uid, Username: "bob"},
		emailErr:     nil,
	}
	svc := &discordAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	pair, err := svc.loginOrCreate(context.Background(), &discordUserInfo{
		ID: "789012", Username: "bob_dc", GlobalName: "Bob", Email: "bob@example.com", Verified: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.Username != "bob" {
		t.Errorf("username: got %q, want %q", pair.Username, "bob")
	}
	if !db.linkCalled {
		t.Error("expected LinkDiscordAccount to be called")
	}
}

func TestDiscordLoginOrCreate_CreateNewUser(t *testing.T) {
	uid := uuid.New()
	db := &mockDiscordOAuthDB{
		discordIDErr: pgx.ErrNoRows,
		emailErr:     pgx.ErrNoRows,
		count:        0, // First user => admin
		createUser:   gen.User{ID: uid, Username: "newuser_abc123", IsAdmin: true},
	}
	svc := &discordAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	pair, err := svc.loginOrCreate(context.Background(), &discordUserInfo{
		ID: "999999", Username: "newuser_dc", GlobalName: "New User", Email: "new@example.com", Verified: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.UserID != uid {
		t.Error("user_id mismatch")
	}
}

func TestDiscordLoginOrCreate_CreateNewUser_NotAdmin(t *testing.T) {
	uid := uuid.New()
	db := &mockDiscordOAuthDB{
		discordIDErr: pgx.ErrNoRows,
		emailErr:     pgx.ErrNoRows,
		count:        5, // Not first user => not admin
		createUser:   gen.User{ID: uid, Username: "seconduser_abc", IsAdmin: false},
	}
	svc := &discordAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	pair, err := svc.loginOrCreate(context.Background(), &discordUserInfo{
		ID: "111111", Username: "second_dc", GlobalName: "Second", Email: "second@example.com", Verified: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.UserID != uid {
		t.Error("user_id mismatch")
	}
}

func TestDiscordLoginOrCreate_UsesUsernameWhenGlobalNameEmpty(t *testing.T) {
	uid := uuid.New()
	db := &mockDiscordOAuthDB{
		discordIDErr: pgx.ErrNoRows,
		emailErr:     pgx.ErrNoRows,
		count:        0,
		createUser:   gen.User{ID: uid, Username: "fallback_user"},
	}
	svc := &discordAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	// GlobalName is empty, so Username should be used for deriveUsername.
	pair, err := svc.loginOrCreate(context.Background(), &discordUserInfo{
		ID: "222222", Username: "fallback_dc", GlobalName: "", Email: "fb@example.com", Verified: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.UserID != uid {
		t.Error("user_id mismatch")
	}
}

// ── Verified flag behavior ───────────────────────────────────────────────────

func TestDiscordLoginOrCreate_UnverifiedEmail_SkipsEmailLink(t *testing.T) {
	uid := uuid.New()
	db := &mockDiscordOAuthDB{
		discordIDErr: pgx.ErrNoRows,
		// Email lookup should be skipped because Verified=false.
		emailUser:  gen.User{ID: uuid.New(), Username: "should-not-match"},
		emailErr:   nil,
		count:      0,
		createUser: gen.User{ID: uid, Username: "unverified_user"},
	}
	svc := &discordAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	pair, err := svc.loginOrCreate(context.Background(), &discordUserInfo{
		ID: "333333", Username: "unverified_dc", GlobalName: "Unverified", Email: "unverified@example.com", Verified: false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have created a new user, not matched by email.
	if pair.UserID != uid {
		t.Error("user_id mismatch: expected new user, not email match")
	}
	if db.linkCalled {
		t.Error("LinkDiscordAccount should not be called when email is unverified")
	}
}

func TestDiscordLoginOrCreate_EmptyEmail_SkipsEmailLink(t *testing.T) {
	uid := uuid.New()
	db := &mockDiscordOAuthDB{
		discordIDErr: pgx.ErrNoRows,
		emailUser:    gen.User{ID: uuid.New(), Username: "should-not-match"},
		emailErr:     nil,
		count:        0,
		createUser:   gen.User{ID: uid, Username: "noemail_user"},
	}
	svc := &discordAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	pair, err := svc.loginOrCreate(context.Background(), &discordUserInfo{
		ID: "444444", Username: "noemail_dc", GlobalName: "No Email", Email: "", Verified: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.UserID != uid {
		t.Error("user_id mismatch: expected new user, not email match")
	}
}

// ── Error cases ──────────────────────────────────────────────────────────────

func TestDiscordLoginOrCreate_DiscordIDLookupError(t *testing.T) {
	db := &mockDiscordOAuthDB{
		discordIDErr: errors.New("db connection failed"),
	}
	svc := &discordAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	_, err := svc.loginOrCreate(context.Background(), &discordUserInfo{
		ID: "1", Username: "x", Email: "a@b.com", Verified: true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "get user by discord_id") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDiscordLoginOrCreate_EmailLookupError(t *testing.T) {
	db := &mockDiscordOAuthDB{
		discordIDErr: pgx.ErrNoRows,
		emailErr:     errors.New("db timeout"),
	}
	svc := &discordAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	_, err := svc.loginOrCreate(context.Background(), &discordUserInfo{
		ID: "1", Username: "x", Email: "a@b.com", Verified: true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "get user by email") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDiscordLoginOrCreate_CountError(t *testing.T) {
	db := &mockDiscordOAuthDB{
		discordIDErr: pgx.ErrNoRows,
		emailErr:     pgx.ErrNoRows,
		countErr:     errors.New("count failed"),
	}
	svc := &discordAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	_, err := svc.loginOrCreate(context.Background(), &discordUserInfo{
		ID: "1", Username: "x", Email: "a@b.com", Verified: true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "count users") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDiscordLoginOrCreate_CreateError(t *testing.T) {
	db := &mockDiscordOAuthDB{
		discordIDErr: pgx.ErrNoRows,
		emailErr:     pgx.ErrNoRows,
		count:        0,
		createErr:    fmt.Errorf("unique constraint"),
	}
	svc := &discordAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	_, err := svc.loginOrCreate(context.Background(), &discordUserInfo{
		ID: "1", Username: "x", Email: "a@b.com", Verified: true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "create discord user") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDiscordLoginOrCreate_LinkError_StillSucceeds(t *testing.T) {
	uid := uuid.New()
	db := &mockDiscordOAuthDB{
		discordIDErr: pgx.ErrNoRows,
		emailUser:    gen.User{ID: uid, Username: "dave"},
		emailErr:     nil,
		linkErr:      errors.New("link failed"),
	}
	svc := &discordAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	// Link error is logged but login still succeeds.
	pair, err := svc.loginOrCreate(context.Background(), &discordUserInfo{
		ID: "1", Username: "dave_dc", GlobalName: "Dave", Email: "dave@example.com", Verified: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.Username != "dave" {
		t.Errorf("username: got %q, want %q", pair.Username, "dave")
	}
}
