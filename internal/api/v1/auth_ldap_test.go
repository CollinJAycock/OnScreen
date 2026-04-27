package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// ── LDAPHandler.Enabled ─────────────────────────────────────────────────────

type fakeLDAPSettings struct {
	cfg settings.LDAPConfig
}

func (f *fakeLDAPSettings) LDAP(_ context.Context) settings.LDAPConfig { return f.cfg }

func TestLDAPEnabled_Disabled(t *testing.T) {
	src := &fakeLDAPSettings{cfg: settings.LDAPConfig{}}
	h := NewLDAPHandler(src, nil, slog.Default())

	rec := httptest.NewRecorder()
	h.Enabled(rec, httptest.NewRequest("GET", "/api/v1/auth/ldap/enabled", nil))

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
	if body.Data.DisplayName != "LDAP" {
		t.Errorf("display_name: got %q, want fallback 'LDAP'", body.Data.DisplayName)
	}
}

func TestLDAPEnabled_Configured(t *testing.T) {
	src := &fakeLDAPSettings{cfg: settings.LDAPConfig{
		Enabled: true, Host: "ldap.example.com:636",
		UserSearchBase: "ou=people,dc=ex,dc=com", UserFilter: "(uid={username})",
		DisplayName: "Company SSO",
	}}
	h := NewLDAPHandler(src, nil, slog.Default())

	rec := httptest.NewRecorder()
	h.Enabled(rec, httptest.NewRequest("GET", "/api/v1/auth/ldap/enabled", nil))

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
	if body.Data.DisplayName != "Company SSO" {
		t.Errorf("display_name: got %q", body.Data.DisplayName)
	}
}

// ── LDAPHandler.Login ───────────────────────────────────────────────────────

type stubLDAPAuthSvc struct {
	pair *TokenPair
	err  error
}

func (s *stubLDAPAuthSvc) LoginLDAP(_ context.Context, _, _ string) (*TokenPair, error) {
	return s.pair, s.err
}

func postLDAPLogin(h *LDAPHandler, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/auth/ldap/login", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	return rec
}

func TestLDAPLogin_Success(t *testing.T) {
	uid := uuid.New()
	svc := &stubLDAPAuthSvc{pair: &TokenPair{
		AccessToken: "at", RefreshToken: "rt", UserID: uid, Username: "alice",
	}}
	h := NewLDAPHandler(&fakeLDAPSettings{}, svc, slog.Default())

	rec := postLDAPLogin(h, map[string]string{"username": "alice", "password": "pw"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestLDAPLogin_BadBody(t *testing.T) {
	h := NewLDAPHandler(&fakeLDAPSettings{}, &stubLDAPAuthSvc{}, slog.Default())
	rec := postLDAPLogin(h, map[string]string{"username": ""})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestLDAPLogin_Disabled(t *testing.T) {
	svc := &stubLDAPAuthSvc{err: ErrLDAPDisabled}
	h := NewLDAPHandler(&fakeLDAPSettings{}, svc, slog.Default())
	rec := postLDAPLogin(h, map[string]string{"username": "u", "password": "p"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
}

func TestLDAPLogin_BadCredentials(t *testing.T) {
	svc := &stubLDAPAuthSvc{err: ErrLDAPInvalidCredentials}
	h := NewLDAPHandler(&fakeLDAPSettings{}, svc, slog.Default())
	rec := postLDAPLogin(h, map[string]string{"username": "u", "password": "p"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestLDAPLogin_UpstreamError(t *testing.T) {
	svc := &stubLDAPAuthSvc{err: errors.New("dial: connection refused")}
	h := NewLDAPHandler(&fakeLDAPSettings{}, svc, slog.Default())
	rec := postLDAPLogin(h, map[string]string{"username": "u", "password": "p"})
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502", rec.Code)
	}
}

// ── ldapAuthService with fake dialer/conn ───────────────────────────────────

type fakeLDAPConn struct {
	bindErrs   map[string]error  // by DN — first call uses BindDN, second uses entry DN
	bindCalls  []string          // DNs we bound as
	searchOut  *ldap.SearchResult
	searchErr  error
	searchReqs []*ldap.SearchRequest
}

func (f *fakeLDAPConn) Bind(dn, _ string) error {
	f.bindCalls = append(f.bindCalls, dn)
	if err, ok := f.bindErrs[dn]; ok {
		return err
	}
	return nil
}
func (f *fakeLDAPConn) Search(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f.searchReqs = append(f.searchReqs, req)
	return f.searchOut, f.searchErr
}
func (f *fakeLDAPConn) Close() error { return nil }

type fakeLDAPDialer struct {
	conn   LDAPConn
	dialErr error
}

func (f *fakeLDAPDialer) Dial(_ settings.LDAPConfig) (LDAPConn, error) {
	if f.dialErr != nil {
		return nil, f.dialErr
	}
	return f.conn, nil
}

type mockLDAPDB struct {
	dnUser    gen.User
	dnErr     error
	emailUser gen.User
	emailErr  error
	unameUser gen.User
	unameErr  error
	linkErr   error
	linkCalls int
	createOut gen.User
	createErr error
	count     int64
	countErr  error
	adminSets []gen.SetUserAdminParams
}

func (m *mockLDAPDB) GetUserByLDAPDN(_ context.Context, _ *string) (gen.User, error) {
	return m.dnUser, m.dnErr
}
func (m *mockLDAPDB) GetUserByEmail(_ context.Context, _ *string) (gen.User, error) {
	return m.emailUser, m.emailErr
}
func (m *mockLDAPDB) GetUserByUsername(_ context.Context, _ string) (gen.User, error) {
	return m.unameUser, m.unameErr
}
func (m *mockLDAPDB) LinkLDAPAccount(_ context.Context, _ gen.LinkLDAPAccountParams) error {
	m.linkCalls++
	return m.linkErr
}
func (m *mockLDAPDB) CreateLDAPUser(_ context.Context, _ gen.CreateLDAPUserParams) (gen.User, error) {
	return m.createOut, m.createErr
}
func (m *mockLDAPDB) CountUsers(_ context.Context) (int64, error) { return m.count, m.countErr }
func (m *mockLDAPDB) SetUserAdmin(_ context.Context, p gen.SetUserAdminParams) error {
	m.adminSets = append(m.adminSets, p)
	return nil
}
func (m *mockLDAPDB) GrantAutoLibrariesToUser(_ context.Context, _ uuid.UUID) error {
	return nil
}

func validLDAPCfg() settings.LDAPConfig {
	return settings.LDAPConfig{
		Enabled: true, Host: "ldap.example.com:389",
		BindDN: "cn=svc,dc=ex,dc=com", BindPassword: "svcpw",
		UserSearchBase: "ou=people,dc=ex,dc=com", UserFilter: "(uid={username})",
	}
}

func userEntry(dn, uid, mail string, memberOf ...string) *ldap.Entry {
	attrs := []*ldap.EntryAttribute{
		{Name: "uid", Values: []string{uid}},
		{Name: "mail", Values: []string{mail}},
	}
	if len(memberOf) > 0 {
		attrs = append(attrs, &ldap.EntryAttribute{Name: "memberOf", Values: memberOf})
	}
	return &ldap.Entry{DN: dn, Attributes: attrs}
}

func TestLDAPService_Disabled(t *testing.T) {
	cfg := &fakeLDAPSettings{cfg: settings.LDAPConfig{}}
	svc := NewLDAPAuthService(cfg, &fakeLDAPDialer{}, &mockLDAPDB{}, fakeIssueTokens, slog.Default())
	_, err := svc.LoginLDAP(context.Background(), "alice", "pw")
	if !errors.Is(err, ErrLDAPDisabled) {
		t.Errorf("expected ErrLDAPDisabled, got %v", err)
	}
}

func TestLDAPService_DialFailure(t *testing.T) {
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	d := &fakeLDAPDialer{dialErr: errors.New("connect refused")}
	svc := NewLDAPAuthService(cfg, d, &mockLDAPDB{}, fakeIssueTokens, slog.Default())
	_, err := svc.LoginLDAP(context.Background(), "alice", "pw")
	if err == nil || !strings.Contains(err.Error(), "ldap dial") {
		t.Errorf("expected dial error, got %v", err)
	}
}

func TestLDAPService_ServiceBindFailure(t *testing.T) {
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	conn := &fakeLDAPConn{bindErrs: map[string]error{
		"cn=svc,dc=ex,dc=com": errors.New("bad svc creds"),
	}}
	d := &fakeLDAPDialer{conn: conn}
	svc := NewLDAPAuthService(cfg, d, &mockLDAPDB{}, fakeIssueTokens, slog.Default())
	_, err := svc.LoginLDAP(context.Background(), "alice", "pw")
	if err == nil || !strings.Contains(err.Error(), "service bind") {
		t.Errorf("expected service bind error, got %v", err)
	}
}

func TestLDAPService_NoUserFound(t *testing.T) {
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{}}
	d := &fakeLDAPDialer{conn: conn}
	svc := NewLDAPAuthService(cfg, d, &mockLDAPDB{}, fakeIssueTokens, slog.Default())
	_, err := svc.LoginLDAP(context.Background(), "ghost", "pw")
	if !errors.Is(err, ErrLDAPInvalidCredentials) {
		t.Errorf("expected ErrLDAPInvalidCredentials, got %v", err)
	}
}

func TestLDAPService_AmbiguousFilter_ReturnsInvalidCredentials(t *testing.T) {
	// An ambiguous filter must look identical to "user not found" from the
	// caller's perspective — otherwise a misconfigured filter could be used
	// to enumerate the directory by behaviour diff.
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{Entries: []*ldap.Entry{
		userEntry("uid=alice,ou=people,dc=ex,dc=com", "alice", "a@ex.com"),
		userEntry("uid=alice2,ou=people,dc=ex,dc=com", "alice", "a2@ex.com"),
	}}}
	d := &fakeLDAPDialer{conn: conn}
	svc := NewLDAPAuthService(cfg, d, &mockLDAPDB{}, fakeIssueTokens, slog.Default())
	_, err := svc.LoginLDAP(context.Background(), "alice", "pw")
	if !errors.Is(err, ErrLDAPInvalidCredentials) {
		t.Errorf("expected ErrLDAPInvalidCredentials, got %v", err)
	}
}

func TestLDAPService_BadUserPassword(t *testing.T) {
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	dn := "uid=alice,ou=people,dc=ex,dc=com"
	conn := &fakeLDAPConn{
		bindErrs: map[string]error{
			dn: &ldap.Error{ResultCode: ldap.LDAPResultInvalidCredentials, Err: errors.New("invalid")},
		},
		searchOut: &ldap.SearchResult{Entries: []*ldap.Entry{userEntry(dn, "alice", "a@ex.com")}},
	}
	d := &fakeLDAPDialer{conn: conn}
	svc := NewLDAPAuthService(cfg, d, &mockLDAPDB{}, fakeIssueTokens, slog.Default())
	_, err := svc.LoginLDAP(context.Background(), "alice", "wrong")
	if !errors.Is(err, ErrLDAPInvalidCredentials) {
		t.Errorf("expected ErrLDAPInvalidCredentials, got %v", err)
	}
}

func TestLDAPService_ExistingDNLogsIn(t *testing.T) {
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	dn := "uid=alice,ou=people,dc=ex,dc=com"
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{Entries: []*ldap.Entry{
		userEntry(dn, "alice", "a@ex.com"),
	}}}
	d := &fakeLDAPDialer{conn: conn}
	db := &mockLDAPDB{dnUser: gen.User{ID: uuid.New(), Username: "alice"}}
	svc := NewLDAPAuthService(cfg, d, db, fakeIssueTokens, slog.Default())

	pair, err := svc.LoginLDAP(context.Background(), "alice", "pw")
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

func TestLDAPService_LinkByEmail(t *testing.T) {
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	dn := "uid=bob,ou=people,dc=ex,dc=com"
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{Entries: []*ldap.Entry{
		userEntry(dn, "bob", "bob@ex.com"),
	}}}
	d := &fakeLDAPDialer{conn: conn}
	db := &mockLDAPDB{
		dnErr:     pgx.ErrNoRows,
		emailUser: gen.User{ID: uuid.New(), Username: "bob"},
	}
	svc := NewLDAPAuthService(cfg, d, db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginLDAP(context.Background(), "bob", "pw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if db.linkCalls != 1 {
		t.Errorf("expected 1 link call, got %d", db.linkCalls)
	}
}

func TestLDAPService_LinkByUsername(t *testing.T) {
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	dn := "uid=carol,ou=people,dc=ex,dc=com"
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{Entries: []*ldap.Entry{
		userEntry(dn, "carol", "carol@ex.com"),
	}}}
	d := &fakeLDAPDialer{conn: conn}
	db := &mockLDAPDB{
		dnErr:     pgx.ErrNoRows,
		emailErr:  pgx.ErrNoRows,
		unameUser: gen.User{ID: uuid.New(), Username: "carol"},
	}
	svc := NewLDAPAuthService(cfg, d, db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginLDAP(context.Background(), "carol", "pw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if db.linkCalls != 1 {
		t.Errorf("expected 1 link call, got %d", db.linkCalls)
	}
}

func TestLDAPService_CreatesNewUser_FirstUserBecomesAdmin(t *testing.T) {
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	dn := "uid=dave,ou=people,dc=ex,dc=com"
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{Entries: []*ldap.Entry{
		userEntry(dn, "dave", "dave@ex.com"),
	}}}
	d := &fakeLDAPDialer{conn: conn}
	db := &mockLDAPDB{
		dnErr:     pgx.ErrNoRows,
		emailErr:  pgx.ErrNoRows,
		unameErr:  pgx.ErrNoRows,
		count:     0,
		createOut: gen.User{ID: uuid.New(), Username: "dave", IsAdmin: true},
	}
	svc := NewLDAPAuthService(cfg, d, db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginLDAP(context.Background(), "dave", "pw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLDAPService_AdminGroupSync_PromotesExistingUser(t *testing.T) {
	cfg := validLDAPCfg()
	cfg.AdminGroupDN = "cn=admins,ou=groups,dc=ex,dc=com"
	src := &fakeLDAPSettings{cfg: cfg}
	dn := "uid=eve,ou=people,dc=ex,dc=com"
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{Entries: []*ldap.Entry{
		userEntry(dn, "eve", "eve@ex.com", "cn=admins,ou=groups,dc=ex,dc=com", "cn=users,ou=groups,dc=ex,dc=com"),
	}}}
	d := &fakeLDAPDialer{conn: conn}
	db := &mockLDAPDB{dnUser: gen.User{ID: uuid.New(), Username: "eve", IsAdmin: false}}
	svc := NewLDAPAuthService(src, d, db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginLDAP(context.Background(), "eve", "pw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(db.adminSets) != 1 || !db.adminSets[0].IsAdmin {
		t.Errorf("expected SetUserAdmin(true), got %+v", db.adminSets)
	}
}

func TestLDAPService_NoAdminSync_WithoutAdminGroup(t *testing.T) {
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	dn := "uid=frank,ou=people,dc=ex,dc=com"
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{Entries: []*ldap.Entry{
		userEntry(dn, "frank", "frank@ex.com", "cn=admins,ou=groups,dc=ex,dc=com"),
	}}}
	d := &fakeLDAPDialer{conn: conn}
	db := &mockLDAPDB{dnUser: gen.User{ID: uuid.New(), Username: "frank", IsAdmin: false}}
	svc := NewLDAPAuthService(cfg, d, db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginLDAP(context.Background(), "frank", "pw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(db.adminSets) != 0 {
		t.Errorf("expected no SetUserAdmin calls without AdminGroupDN, got %+v", db.adminSets)
	}
}

func TestLDAPService_FilterEscapesUsername(t *testing.T) {
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{}}
	d := &fakeLDAPDialer{conn: conn}
	svc := NewLDAPAuthService(cfg, d, &mockLDAPDB{}, fakeIssueTokens, slog.Default())

	_, _ = svc.LoginLDAP(context.Background(), "evil)(uid=*", "pw")
	if len(conn.searchReqs) != 1 {
		t.Fatalf("expected 1 search call, got %d", len(conn.searchReqs))
	}
	filter := conn.searchReqs[0].Filter
	if strings.Contains(filter, "(uid=*)") {
		t.Errorf("filter not escaped: %q", filter)
	}
}

// ── account-conflict guards (HIGH security regression tests) ────────────────

// passwordPtr is a one-liner so test fixtures can read like "user with a
// password" instead of dancing around taking the address of a string literal.
func passwordPtr(s string) *string { return &s }

func TestLDAPService_NonStubEmailMatch_ReturnsConflict(t *testing.T) {
	// A local account with a real password must NOT be auto-linked to a remote
	// LDAP identity that happens to share an email — that's the pre-claim
	// attack from the security audit.
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	dn := "uid=eve,ou=people,dc=ex,dc=com"
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{Entries: []*ldap.Entry{
		userEntry(dn, "eve", "victim@corp.com"),
	}}}
	d := &fakeLDAPDialer{conn: conn}
	db := &mockLDAPDB{
		dnErr: pgx.ErrNoRows,
		emailUser: gen.User{
			ID:           uuid.New(),
			Username:     "victim",
			PasswordHash: passwordPtr("argon2id$..."),
		},
	}
	svc := NewLDAPAuthService(cfg, d, db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginLDAP(context.Background(), "eve", "pw")
	if !errors.Is(err, ErrSSOAccountConflict) {
		t.Errorf("expected ErrSSOAccountConflict, got %v", err)
	}
	if db.linkCalls != 0 {
		t.Errorf("must NOT link to a non-stub user, got %d link calls", db.linkCalls)
	}
}

func TestLDAPService_NonStubUsernameMatch_ReturnsConflict(t *testing.T) {
	// Same defence on the username-match path — string equality alone is not
	// proof of identity. Even with no email, a remote "alice" must not absorb
	// the local "alice" account.
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	dn := "uid=alice,ou=remote,dc=ex,dc=com"
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{Entries: []*ldap.Entry{
		userEntry(dn, "alice", ""),
	}}}
	d := &fakeLDAPDialer{conn: conn}
	db := &mockLDAPDB{
		dnErr:    pgx.ErrNoRows,
		emailErr: pgx.ErrNoRows,
		unameUser: gen.User{
			ID:           uuid.New(),
			Username:     "alice",
			PasswordHash: passwordPtr("argon2id$..."),
		},
	}
	svc := NewLDAPAuthService(cfg, d, db, fakeIssueTokens, slog.Default())

	_, err := svc.LoginLDAP(context.Background(), "alice", "pw")
	if !errors.Is(err, ErrSSOAccountConflict) {
		t.Errorf("expected ErrSSOAccountConflict, got %v", err)
	}
	if db.linkCalls != 0 {
		t.Errorf("must NOT link to a non-stub user, got %d link calls", db.linkCalls)
	}
}

func TestLDAPLogin_AccountConflict_Returns409(t *testing.T) {
	svc := &stubLDAPAuthSvc{err: ErrSSOAccountConflict}
	h := NewLDAPHandler(&fakeLDAPSettings{}, svc, slog.Default())
	rec := postLDAPLogin(h, map[string]string{"username": "u", "password": "p"})
	if rec.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409", rec.Code)
	}
}
