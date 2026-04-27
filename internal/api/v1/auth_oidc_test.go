package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// ── buildProfile ────────────────────────────────────────────────────────────

func TestBuildProfile_PrefersConfiguredUsernameClaim(t *testing.T) {
	cfg := settings.OIDCConfig{UsernameClaim: "nickname"}
	p, err := buildProfile(cfg, "sub-1", "https://idp", map[string]any{
		"nickname":           "alice_dev",
		"preferred_username": "alice@idp",
		"email":              "alice@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Username != "alice_dev" {
		t.Errorf("username: got %q, want %q", p.Username, "alice_dev")
	}
}

func TestBuildProfile_FallsBackToPreferredUsername(t *testing.T) {
	cfg := settings.OIDCConfig{}
	p, err := buildProfile(cfg, "sub-2", "https://idp", map[string]any{
		"preferred_username": "bob",
		"email":              "bob@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Username != "bob" {
		t.Errorf("username: got %q, want %q", p.Username, "bob")
	}
}

func TestBuildProfile_FallsBackToEmailPrefix(t *testing.T) {
	cfg := settings.OIDCConfig{}
	p, _ := buildProfile(cfg, "sub-3", "https://idp", map[string]any{
		"email": "carol@example.com",
	})
	if p.Username != "carol" {
		t.Errorf("username: got %q, want %q", p.Username, "carol")
	}
}

func TestBuildProfile_FallsBackToSubject(t *testing.T) {
	cfg := settings.OIDCConfig{}
	p, _ := buildProfile(cfg, "abc123", "https://idp", map[string]any{})
	if !strings.HasPrefix(p.Username, "user_abc123") {
		t.Errorf("username: got %q, want prefix user_abc123", p.Username)
	}
}

func TestBuildProfile_SanitizesUsername(t *testing.T) {
	cfg := settings.OIDCConfig{}
	p, _ := buildProfile(cfg, "sub-4", "https://idp", map[string]any{
		"preferred_username": "weird/name@idp!",
	})
	if strings.ContainsAny(p.Username, "/@!") {
		t.Errorf("username not sanitized: %q", p.Username)
	}
}

func TestBuildProfile_TruncatesLongUsername(t *testing.T) {
	cfg := settings.OIDCConfig{}
	long := strings.Repeat("a", 100)
	p, _ := buildProfile(cfg, "sub-5", "https://idp", map[string]any{
		"preferred_username": long,
	})
	if len(p.Username) > 32 {
		t.Errorf("username too long: %d", len(p.Username))
	}
}

func TestBuildProfile_MissingSubject(t *testing.T) {
	_, err := buildProfile(settings.OIDCConfig{}, "", "https://idp", map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing sub")
	}
}

func TestBuildProfile_GroupsClaim_AdminMatch(t *testing.T) {
	cfg := settings.OIDCConfig{AdminGroup: "onscreen-admins"}
	p, _ := buildProfile(cfg, "sub-6", "https://idp", map[string]any{
		"preferred_username": "dave",
		"groups":             []any{"users", "onscreen-admins"},
	})
	if !p.GroupSync {
		t.Error("expected GroupSync=true when AdminGroup is set")
	}
	if !p.IsAdmin {
		t.Error("expected IsAdmin=true when in admin group")
	}
}

func TestBuildProfile_GroupsClaim_NoMatch(t *testing.T) {
	cfg := settings.OIDCConfig{AdminGroup: "admins"}
	p, _ := buildProfile(cfg, "sub-7", "https://idp", map[string]any{
		"preferred_username": "eve",
		"groups":             []any{"users"},
	})
	if !p.GroupSync {
		t.Error("expected GroupSync=true when AdminGroup is set")
	}
	if p.IsAdmin {
		t.Error("expected IsAdmin=false when not in admin group")
	}
}

func TestBuildProfile_NoAdminGroup_NoGroupSync(t *testing.T) {
	cfg := settings.OIDCConfig{}
	p, _ := buildProfile(cfg, "sub-8", "https://idp", map[string]any{
		"preferred_username": "frank",
		"groups":             []any{"admins"},
	})
	if p.GroupSync {
		t.Error("expected GroupSync=false when AdminGroup is empty")
	}
	if p.IsAdmin {
		t.Error("expected IsAdmin=false when no AdminGroup configured")
	}
}

func TestBuildProfile_CustomGroupsClaim(t *testing.T) {
	cfg := settings.OIDCConfig{AdminGroup: "ops", GroupsClaim: "roles"}
	p, _ := buildProfile(cfg, "sub-9", "https://idp", map[string]any{
		"preferred_username": "grace",
		"roles":              []any{"ops", "viewer"},
	})
	if !p.IsAdmin {
		t.Error("expected IsAdmin=true via custom groups claim")
	}
}

// ── OIDCHandler.Enabled ─────────────────────────────────────────────────────

type fakeOIDCSettings struct {
	cfg settings.OIDCConfig
}

func (f *fakeOIDCSettings) OIDC(_ context.Context) settings.OIDCConfig { return f.cfg }

func TestOIDCEnabled_Disabled(t *testing.T) {
	src := &fakeOIDCSettings{cfg: settings.OIDCConfig{}}
	h := NewOIDCHandler(src, nil, "http://localhost", slog.Default())
	rec := httptest.NewRecorder()
	h.Enabled(rec, httptest.NewRequest("GET", "/api/v1/auth/oidc/enabled", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	var body struct {
		Data struct {
			Enabled     bool   `json:"enabled"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Data.Enabled {
		t.Error("expected enabled=false")
	}
	if body.Data.DisplayName != "SSO" {
		t.Errorf("display_name: got %q, want fallback 'SSO'", body.Data.DisplayName)
	}
}

func TestOIDCEnabled_Configured(t *testing.T) {
	src := &fakeOIDCSettings{cfg: settings.OIDCConfig{
		Enabled: true, IssuerURL: "https://idp", ClientID: "cid", DisplayName: "Authentik",
	}}
	h := NewOIDCHandler(src, nil, "http://localhost", slog.Default())
	rec := httptest.NewRecorder()
	h.Enabled(rec, httptest.NewRequest("GET", "/api/v1/auth/oidc/enabled", nil))

	var body struct {
		Data struct {
			Enabled     bool   `json:"enabled"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !body.Data.Enabled {
		t.Error("expected enabled=true")
	}
	if body.Data.DisplayName != "Authentik" {
		t.Errorf("display_name: got %q", body.Data.DisplayName)
	}
}

// ── oidcAuthService ─────────────────────────────────────────────────────────

type mockOIDCDB struct {
	subjUser  gen.User
	subjErr   error
	emailUser gen.User
	emailErr  error
	linkErr   error
	linkCalls int
	createOut gen.User
	createErr error
	count     int64
	countErr  error
	adminSets []gen.SetUserAdminParams
}

func (m *mockOIDCDB) GetUserByOIDCSubject(_ context.Context, _ gen.GetUserByOIDCSubjectParams) (gen.User, error) {
	return m.subjUser, m.subjErr
}
func (m *mockOIDCDB) GetUserByEmail(_ context.Context, _ *string) (gen.User, error) {
	return m.emailUser, m.emailErr
}
func (m *mockOIDCDB) LinkOIDCAccount(_ context.Context, _ gen.LinkOIDCAccountParams) error {
	m.linkCalls++
	return m.linkErr
}
func (m *mockOIDCDB) CreateOIDCUser(_ context.Context, _ gen.CreateOIDCUserParams) (gen.User, error) {
	return m.createOut, m.createErr
}
func (m *mockOIDCDB) CountUsers(_ context.Context) (int64, error) { return m.count, m.countErr }
func (m *mockOIDCDB) SetUserAdmin(_ context.Context, p gen.SetUserAdminParams) error {
	m.adminSets = append(m.adminSets, p)
	return nil
}
func (m *mockOIDCDB) GrantAutoLibrariesToUser(_ context.Context, _ uuid.UUID) error {
	return nil
}

func TestOIDCService_ExistingSubjectLogsIn(t *testing.T) {
	uid := uuid.New()
	db := &mockOIDCDB{subjUser: gen.User{ID: uid, Username: "alice"}}
	svc := NewOIDCAuthService(db, fakeIssueTokens, slog.Default())

	pair, err := svc.LoginOrCreateOIDCUser(context.Background(), OIDCProfile{
		Issuer: "https://idp", Subject: "abc", Username: "alice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.Username != "alice" {
		t.Errorf("username: got %q", pair.Username)
	}
	if db.linkCalls != 0 {
		t.Errorf("expected no link calls, got %d", db.linkCalls)
	}
}

func TestOIDCService_LinksByEmail(t *testing.T) {
	uid := uuid.New()
	db := &mockOIDCDB{
		subjErr:   pgx.ErrNoRows,
		emailUser: gen.User{ID: uid, Username: "bob"},
	}
	svc := NewOIDCAuthService(db, fakeIssueTokens, slog.Default())

	pair, err := svc.LoginOrCreateOIDCUser(context.Background(), OIDCProfile{
		Issuer: "https://idp", Subject: "xyz", Email: "bob@example.com", Username: "bob",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.Username != "bob" {
		t.Errorf("username: got %q", pair.Username)
	}
	if db.linkCalls != 1 {
		t.Errorf("expected 1 link call, got %d", db.linkCalls)
	}
}

func TestOIDCService_CreatesNewUser_FirstUserBecomesAdmin(t *testing.T) {
	uid := uuid.New()
	db := &mockOIDCDB{
		subjErr:   pgx.ErrNoRows,
		emailErr:  pgx.ErrNoRows,
		count:     0,
		createOut: gen.User{ID: uid, Username: "carol", IsAdmin: true},
	}
	svc := NewOIDCAuthService(db, fakeIssueTokens, slog.Default())

	pair, err := svc.LoginOrCreateOIDCUser(context.Background(), OIDCProfile{
		Issuer: "https://idp", Subject: "first-sub", Email: "carol@example.com", Username: "carol",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.Username != "carol" {
		t.Errorf("username: got %q", pair.Username)
	}
}

func TestOIDCService_CreatesNewUser_NoEmail(t *testing.T) {
	uid := uuid.New()
	db := &mockOIDCDB{
		subjErr:   pgx.ErrNoRows,
		count:     5,
		createOut: gen.User{ID: uid, Username: "dave"},
	}
	svc := NewOIDCAuthService(db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginOrCreateOIDCUser(context.Background(), OIDCProfile{
		Issuer: "https://idp", Subject: "no-email-sub", Username: "dave",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOIDCService_PropagatesSubjectLookupError(t *testing.T) {
	db := &mockOIDCDB{subjErr: errors.New("db down")}
	svc := NewOIDCAuthService(db, fakeIssueTokens, slog.Default())
	_, err := svc.LoginOrCreateOIDCUser(context.Background(), OIDCProfile{
		Issuer: "i", Subject: "s",
	})
	if err == nil || !strings.Contains(err.Error(), "get user by oidc") {
		t.Fatalf("expected lookup error, got %v", err)
	}
}

func TestOIDCService_AdminSync_OnExisting(t *testing.T) {
	uid := uuid.New()
	db := &mockOIDCDB{subjUser: gen.User{ID: uid, Username: "eve", IsAdmin: false}}
	svc := NewOIDCAuthService(db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginOrCreateOIDCUser(context.Background(), OIDCProfile{
		Issuer: "i", Subject: "s", Username: "eve", IsAdmin: true, GroupSync: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(db.adminSets) != 1 || !db.adminSets[0].IsAdmin {
		t.Errorf("expected SetUserAdmin(true), got %+v", db.adminSets)
	}
}

func TestOIDCService_NoAdminSync_WithoutGroupSync(t *testing.T) {
	uid := uuid.New()
	db := &mockOIDCDB{subjUser: gen.User{ID: uid, Username: "frank", IsAdmin: false}}
	svc := NewOIDCAuthService(db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginOrCreateOIDCUser(context.Background(), OIDCProfile{
		Issuer: "i", Subject: "s", Username: "frank", IsAdmin: true, GroupSync: false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(db.adminSets) != 0 {
		t.Errorf("expected no SetUserAdmin calls, got %+v", db.adminSets)
	}
}

// ── email_verified gate (HIGH security regression tests) ───────────────────

func TestBuildProfile_DropsUnverifiedEmail(t *testing.T) {
	// IdPs let users add unverified emails (Google, Auth0, Keycloak free tier).
	// Carrying that email forward into the profile would let an attacker
	// pre-claim a victim's local account by adding the victim's email to their
	// own IdP user. The profile must drop the email when not verified.
	cfg := settings.OIDCConfig{}
	p, err := buildProfile(cfg, "sub-attacker", "https://idp", map[string]any{
		"preferred_username": "attacker",
		"email":              "victim@corp.com",
		"email_verified":     false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Email != "" {
		t.Errorf("expected dropped email, got %q", p.Email)
	}
	if p.EmailVerified {
		t.Error("expected EmailVerified=false")
	}
}

func TestBuildProfile_KeepsVerifiedEmail(t *testing.T) {
	cfg := settings.OIDCConfig{}
	p, err := buildProfile(cfg, "sub-real", "https://idp", map[string]any{
		"preferred_username": "alice",
		"email":              "alice@corp.com",
		"email_verified":     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Email != "alice@corp.com" {
		t.Errorf("expected kept email, got %q", p.Email)
	}
	if !p.EmailVerified {
		t.Error("expected EmailVerified=true")
	}
}

func TestBuildProfile_MissingEmailVerifiedDropsEmail(t *testing.T) {
	// IdPs that omit the claim entirely are treated as not-verified — fail
	// closed.
	cfg := settings.OIDCConfig{}
	p, _ := buildProfile(cfg, "sub-x", "https://idp", map[string]any{
		"preferred_username": "x",
		"email":              "x@corp.com",
	})
	if p.Email != "" {
		t.Errorf("expected dropped email when claim absent, got %q", p.Email)
	}
}

// ── stub-row gate ──────────────────────────────────────────────────────────

func TestOIDCService_NonStubEmailMatch_ReturnsConflict(t *testing.T) {
	// The OIDC pre-claim defence: a local row with a real password (or any
	// other identity) must NOT be auto-linked to a remote OIDC subject just
	// because the verified email matches.
	pw := "argon2id$..."
	db := &mockOIDCDB{
		subjErr: pgx.ErrNoRows,
		emailUser: gen.User{
			ID:           uuid.New(),
			Username:     "victim",
			PasswordHash: &pw,
		},
	}
	svc := NewOIDCAuthService(db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginOrCreateOIDCUser(context.Background(), OIDCProfile{
		Issuer: "https://idp", Subject: "attacker-sub",
		Email: "victim@corp.com", EmailVerified: true,
		Username: "attacker",
	})
	if !errors.Is(err, ErrSSOAccountConflict) {
		t.Errorf("expected ErrSSOAccountConflict, got %v", err)
	}
	if db.linkCalls != 0 {
		t.Errorf("must NOT link to a non-stub user, got %d link calls", db.linkCalls)
	}
}

// ── flow-state cookie roundtrip (nonce + PKCE plumbing) ────────────────────

func TestOIDCFlowState_RoundTrips(t *testing.T) {
	in := oidcFlowState{
		State:        "state-token",
		Nonce:        "nonce-token",
		CodeVerifier: "verifier-token",
	}
	enc, err := encodeOIDCFlow(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := decodeOIDCFlow(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out != in {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", out, in)
	}
}

func TestOIDCFlowState_Decode_Garbage(t *testing.T) {
	if _, err := decodeOIDCFlow("not-base64-and-not-json!!!"); err == nil {
		t.Error("expected decode error on garbage")
	}
}

// ── configKey cache invalidation ────────────────────────────────────────────

func TestConfigKey_DiffersOnCriticalFields(t *testing.T) {
	a := settings.OIDCConfig{IssuerURL: "https://a", ClientID: "x", ClientSecret: "s"}
	b := settings.OIDCConfig{IssuerURL: "https://b", ClientID: "x", ClientSecret: "s"}
	if configKey(a) == configKey(b) {
		t.Error("issuer change should change key")
	}
	c := settings.OIDCConfig{IssuerURL: "https://a", ClientID: "y", ClientSecret: "s"}
	if configKey(a) == configKey(c) {
		t.Error("client_id change should change key")
	}
}

func TestConfigKey_StableForCosmeticChanges(t *testing.T) {
	a := settings.OIDCConfig{IssuerURL: "https://a", ClientID: "x", ClientSecret: "s"}
	b := settings.OIDCConfig{
		IssuerURL: "https://a", ClientID: "x", ClientSecret: "s",
		DisplayName: "Different", AdminGroup: "ops",
	}
	if configKey(a) != configKey(b) {
		t.Error("display-name/admin-group changes should NOT invalidate key")
	}
}
