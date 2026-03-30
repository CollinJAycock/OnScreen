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

// ── GitHubDisabledHandler ────────────────────────────────────────────────────

func TestGitHubDisabledHandler(t *testing.T) {
	h := GitHubDisabledHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/auth/github/enabled", nil)
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

// ── mock GitHubOAuthDB ───────────────────────────────────────────────────────

type mockGitHubOAuthDB struct {
	// GetUserByGitHubID
	githubIDUser gen.User
	githubIDErr  error

	// GetUserByEmail
	emailUser gen.User
	emailErr  error

	// LinkGitHubAccount
	linkErr    error
	linkCalled bool

	// CreateGitHubUser
	createUser gen.User
	createErr  error

	// CountUsers
	count    int64
	countErr error
}

func (m *mockGitHubOAuthDB) GetUserByGitHubID(_ context.Context, _ *string) (gen.User, error) {
	return m.githubIDUser, m.githubIDErr
}
func (m *mockGitHubOAuthDB) GetUserByEmail(_ context.Context, _ *string) (gen.User, error) {
	return m.emailUser, m.emailErr
}
func (m *mockGitHubOAuthDB) LinkGitHubAccount(_ context.Context, _ gen.LinkGitHubAccountParams) error {
	m.linkCalled = true
	return m.linkErr
}
func (m *mockGitHubOAuthDB) CreateGitHubUser(_ context.Context, _ gen.CreateGitHubUserParams) (gen.User, error) {
	return m.createUser, m.createErr
}
func (m *mockGitHubOAuthDB) CountUsers(_ context.Context) (int64, error) {
	return m.count, m.countErr
}

// ── githubAuthService.loginOrCreate ──────────────────────────────────────────

func TestGitHubLoginOrCreate_ExistingGitHubUser(t *testing.T) {
	uid := uuid.New()
	db := &mockGitHubOAuthDB{
		githubIDUser: gen.User{ID: uid, Username: "alice"},
		githubIDErr:  nil,
	}
	svc := &githubAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	pair, err := svc.loginOrCreate(context.Background(), &githubUserInfo{
		ID: 12345, Login: "alice-gh", Name: "Alice", Email: "alice@example.com",
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

func TestGitHubLoginOrCreate_LinkExistingEmail(t *testing.T) {
	uid := uuid.New()
	db := &mockGitHubOAuthDB{
		githubIDErr: pgx.ErrNoRows,
		emailUser:   gen.User{ID: uid, Username: "bob"},
		emailErr:    nil,
	}
	svc := &githubAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	pair, err := svc.loginOrCreate(context.Background(), &githubUserInfo{
		ID: 67890, Login: "bob-gh", Name: "Bob", Email: "bob@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.Username != "bob" {
		t.Errorf("username: got %q, want %q", pair.Username, "bob")
	}
	if !db.linkCalled {
		t.Error("expected LinkGitHubAccount to be called")
	}
}

func TestGitHubLoginOrCreate_CreateNewUser(t *testing.T) {
	uid := uuid.New()
	db := &mockGitHubOAuthDB{
		githubIDErr: pgx.ErrNoRows,
		emailErr:    pgx.ErrNoRows,
		count:       0, // First user => admin
		createUser:  gen.User{ID: uid, Username: "newuser_abc123", IsAdmin: true},
	}
	svc := &githubAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	pair, err := svc.loginOrCreate(context.Background(), &githubUserInfo{
		ID: 99999, Login: "newuser-gh", Name: "New User", Email: "new@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.UserID != uid {
		t.Error("user_id mismatch")
	}
}

func TestGitHubLoginOrCreate_CreateNewUser_NotAdmin(t *testing.T) {
	uid := uuid.New()
	db := &mockGitHubOAuthDB{
		githubIDErr: pgx.ErrNoRows,
		emailErr:    pgx.ErrNoRows,
		count:       5, // Not first user => not admin
		createUser:  gen.User{ID: uid, Username: "seconduser_abc", IsAdmin: false},
	}
	svc := &githubAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	pair, err := svc.loginOrCreate(context.Background(), &githubUserInfo{
		ID: 11111, Login: "second-gh", Name: "Second", Email: "second@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.UserID != uid {
		t.Error("user_id mismatch")
	}
}

func TestGitHubLoginOrCreate_UsesLoginWhenNameEmpty(t *testing.T) {
	uid := uuid.New()
	db := &mockGitHubOAuthDB{
		githubIDErr: pgx.ErrNoRows,
		emailErr:    pgx.ErrNoRows,
		count:       0,
		createUser:  gen.User{ID: uid, Username: "fallback_login"},
	}
	svc := &githubAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	// Name is empty, so Login should be used for deriveUsername.
	pair, err := svc.loginOrCreate(context.Background(), &githubUserInfo{
		ID: 22222, Login: "fallback-login", Name: "", Email: "fb@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.UserID != uid {
		t.Error("user_id mismatch")
	}
}

func TestGitHubLoginOrCreate_NoEmailSkipsEmailLink(t *testing.T) {
	uid := uuid.New()
	db := &mockGitHubOAuthDB{
		githubIDErr: pgx.ErrNoRows,
		// emailErr is never checked because email is empty
		emailErr:   nil,
		emailUser:  gen.User{ID: uuid.New(), Username: "should-not-match"},
		count:      0,
		createUser: gen.User{ID: uid, Username: "noemail_user"},
	}
	svc := &githubAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	pair, err := svc.loginOrCreate(context.Background(), &githubUserInfo{
		ID: 33333, Login: "noemail", Name: "No Email", Email: "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have created a new user, not matched by email.
	if pair.UserID != uid {
		t.Error("user_id mismatch: expected new user, not email match")
	}
}

// ── Error cases ──────────────────────────────────────────────────────────────

func TestGitHubLoginOrCreate_GitHubIDLookupError(t *testing.T) {
	db := &mockGitHubOAuthDB{
		githubIDErr: errors.New("db connection failed"),
	}
	svc := &githubAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	_, err := svc.loginOrCreate(context.Background(), &githubUserInfo{
		ID: 1, Login: "x", Email: "a@b.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "get user by github_id") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGitHubLoginOrCreate_EmailLookupError(t *testing.T) {
	db := &mockGitHubOAuthDB{
		githubIDErr: pgx.ErrNoRows,
		emailErr:    errors.New("db timeout"),
	}
	svc := &githubAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	_, err := svc.loginOrCreate(context.Background(), &githubUserInfo{
		ID: 1, Login: "x", Email: "a@b.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "get user by email") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGitHubLoginOrCreate_CountError(t *testing.T) {
	db := &mockGitHubOAuthDB{
		githubIDErr: pgx.ErrNoRows,
		emailErr:    pgx.ErrNoRows,
		countErr:    errors.New("count failed"),
	}
	svc := &githubAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	_, err := svc.loginOrCreate(context.Background(), &githubUserInfo{
		ID: 1, Login: "x", Email: "a@b.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "count users") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGitHubLoginOrCreate_CreateError(t *testing.T) {
	db := &mockGitHubOAuthDB{
		githubIDErr: pgx.ErrNoRows,
		emailErr:    pgx.ErrNoRows,
		count:       0,
		createErr:   fmt.Errorf("unique constraint"),
	}
	svc := &githubAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	_, err := svc.loginOrCreate(context.Background(), &githubUserInfo{
		ID: 1, Login: "x", Email: "a@b.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "create github user") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGitHubLoginOrCreate_LinkError_StillSucceeds(t *testing.T) {
	uid := uuid.New()
	db := &mockGitHubOAuthDB{
		githubIDErr: pgx.ErrNoRows,
		emailUser:   gen.User{ID: uid, Username: "dave"},
		emailErr:    nil,
		linkErr:     errors.New("link failed"),
	}
	svc := &githubAuthService{db: db, issueTokens: fakeIssueTokens, logger: slog.Default()}

	// Link error is logged but login still succeeds.
	pair, err := svc.loginOrCreate(context.Background(), &githubUserInfo{
		ID: 1, Login: "dave-gh", Name: "Dave", Email: "dave@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.Username != "dave" {
		t.Errorf("username: got %q, want %q", pair.Username, "dave")
	}
}
