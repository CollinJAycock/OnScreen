package v1

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/crewjam/saml"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// ── SP-keypair generation: smoke test ─────────────────────────────────────

// TestGenerateSPKeyPair_RoundTrip confirms the generated PEMs parse
// back into a valid TLS keypair — buildSAMLMiddleware does the same
// parse, so a busted generator would manifest as "saml: parse SP
// keypair" at first-login time, not at config-save time.
func TestGenerateSPKeyPair_RoundTrip(t *testing.T) {
	cert, key, err := GenerateSPKeyPair("onscreen-test")
	if err != nil {
		t.Fatalf("GenerateSPKeyPair: %v", err)
	}
	if !strings.Contains(cert, "BEGIN CERTIFICATE") {
		t.Errorf("cert PEM missing header: %s", cert[:60])
	}
	if !strings.Contains(key, "BEGIN PRIVATE KEY") {
		t.Errorf("key PEM missing header: %s", key[:60])
	}
}

// ── Profile extraction from a SAML assertion ──────────────────────────────

// TestBuildSAMLProfile_PullsAttributes covers the happy path —
// configured EmailAttribute / UsernameAttribute names map to the
// matching <saml:Attribute> entries in the assertion.
func TestBuildSAMLProfile_PullsAttributes(t *testing.T) {
	cfg := settings.SAMLConfig{
		EmailAttribute:    "mail",
		UsernameAttribute: "uid",
	}
	a := &saml.Assertion{
		Issuer:  saml.Issuer{Value: "https://idp.example.com/saml/metadata"},
		Subject: &saml.Subject{NameID: &saml.NameID{Value: "user-42"}},
		AttributeStatements: []saml.AttributeStatement{
			{Attributes: []saml.Attribute{
				{Name: "mail", Values: []saml.AttributeValue{{Value: "alice@example.com"}}},
				{Name: "uid", Values: []saml.AttributeValue{{Value: "alice"}}},
			}},
		},
	}
	p := buildSAMLProfile(a, cfg)
	if p.Issuer != "https://idp.example.com/saml/metadata" {
		t.Errorf("issuer = %q", p.Issuer)
	}
	if p.Subject != "user-42" {
		t.Errorf("subject = %q", p.Subject)
	}
	if p.Email != "alice@example.com" {
		t.Errorf("email = %q", p.Email)
	}
	if p.Username != "alice" {
		t.Errorf("username = %q", p.Username)
	}
}

// TestBuildSAMLProfile_EmailFromNameID covers the fallback for IdPs
// that emit only the Subject NameID — when it looks like an email
// the profile uses it for both fields.
func TestBuildSAMLProfile_EmailFromNameID(t *testing.T) {
	a := &saml.Assertion{
		Issuer:  saml.Issuer{Value: "idp"},
		Subject: &saml.Subject{NameID: &saml.NameID{Value: "bob@example.com"}},
	}
	p := buildSAMLProfile(a, settings.SAMLConfig{})
	if p.Email != "bob@example.com" {
		t.Errorf("email fallback = %q", p.Email)
	}
	if p.Username != "bob" {
		t.Errorf("username should be email-prefix, got %q", p.Username)
	}
}

// TestBuildSAMLProfile_AdminGroup verifies the admin-group sync flag.
// When GroupsAttribute + AdminGroup are configured, presence of the
// admin group in the assertion's groups attribute promotes the user.
func TestBuildSAMLProfile_AdminGroup(t *testing.T) {
	cfg := settings.SAMLConfig{
		GroupsAttribute: "groups",
		AdminGroup:      "onscreen-admins",
	}
	a := &saml.Assertion{
		Issuer:  saml.Issuer{Value: "idp"},
		Subject: &saml.Subject{NameID: &saml.NameID{Value: "carol@example.com"}},
		AttributeStatements: []saml.AttributeStatement{
			{Attributes: []saml.Attribute{
				{Name: "groups", Values: []saml.AttributeValue{
					{Value: "users"},
					{Value: "onscreen-admins"},
				}},
			}},
		},
	}
	p := buildSAMLProfile(a, cfg)
	if !p.GroupSync {
		t.Error("GroupSync should be true when both attribute + admin group are set")
	}
	if !p.IsAdmin {
		t.Error("IsAdmin should be true when admin group is present in assertion")
	}
}

// TestBuildSAMLProfile_AdminGroupAbsent demotes the user when the
// admin group is configured but not in the assertion. With GroupSync
// on, this drives the maybeSyncAdmin path to flip is_admin off.
func TestBuildSAMLProfile_AdminGroupAbsent(t *testing.T) {
	cfg := settings.SAMLConfig{
		GroupsAttribute: "groups",
		AdminGroup:      "onscreen-admins",
	}
	a := &saml.Assertion{
		Issuer:  saml.Issuer{Value: "idp"},
		Subject: &saml.Subject{NameID: &saml.NameID{Value: "dave@example.com"}},
		AttributeStatements: []saml.AttributeStatement{
			{Attributes: []saml.Attribute{
				{Name: "groups", Values: []saml.AttributeValue{{Value: "users"}}},
			}},
		},
	}
	p := buildSAMLProfile(a, cfg)
	if !p.GroupSync || p.IsAdmin {
		t.Errorf("expected GroupSync=true, IsAdmin=false; got %+v", p)
	}
}

// ── Auth service: provisioning paths ──────────────────────────────────────

type fakeSAMLDB struct {
	users          map[uuid.UUID]gen.User
	bySAML         map[string]gen.User // key: issuer|subject
	byEmail        map[string]gen.User
	createdAdminID uuid.UUID
	linked         []gen.LinkSAMLAccountParams
}

func newFakeSAMLDB() *fakeSAMLDB {
	return &fakeSAMLDB{
		users:   map[uuid.UUID]gen.User{},
		bySAML:  map[string]gen.User{},
		byEmail: map[string]gen.User{},
	}
}

func (f *fakeSAMLDB) GetUserBySAMLSubject(_ context.Context, p gen.GetUserBySAMLSubjectParams) (gen.User, error) {
	key := strPtrVal(p.SamlIssuer) + "|" + strPtrVal(p.SamlSubject)
	if u, ok := f.bySAML[key]; ok {
		return u, nil
	}
	return gen.User{}, pgx.ErrNoRows
}

func (f *fakeSAMLDB) GetUserByEmail(_ context.Context, email *string) (gen.User, error) {
	if email == nil {
		return gen.User{}, pgx.ErrNoRows
	}
	if u, ok := f.byEmail[*email]; ok {
		return u, nil
	}
	return gen.User{}, pgx.ErrNoRows
}

func (f *fakeSAMLDB) LinkSAMLAccount(_ context.Context, p gen.LinkSAMLAccountParams) error {
	f.linked = append(f.linked, p)
	u := f.users[p.ID]
	u.SamlIssuer = p.SamlIssuer
	u.SamlSubject = p.SamlSubject
	f.users[p.ID] = u
	f.bySAML[strPtrVal(p.SamlIssuer)+"|"+strPtrVal(p.SamlSubject)] = u
	return nil
}

func (f *fakeSAMLDB) CreateSAMLUser(_ context.Context, p gen.CreateSAMLUserParams) (gen.User, error) {
	u := gen.User{
		ID:          uuid.New(),
		Username:    p.Username,
		Email:       p.Email,
		SamlIssuer:  p.SamlIssuer,
		SamlSubject: p.SamlSubject,
		IsAdmin:     p.IsAdmin,
	}
	f.users[u.ID] = u
	f.bySAML[strPtrVal(p.SamlIssuer)+"|"+strPtrVal(p.SamlSubject)] = u
	if p.Email != nil {
		f.byEmail[*p.Email] = u
	}
	if p.IsAdmin {
		f.createdAdminID = u.ID
	}
	return u, nil
}

func (f *fakeSAMLDB) CountUsers(_ context.Context) (int64, error) {
	return int64(len(f.users)), nil
}

func (f *fakeSAMLDB) SetUserAdmin(_ context.Context, p gen.SetUserAdminParams) error {
	u := f.users[p.ID]
	u.IsAdmin = p.IsAdmin
	f.users[p.ID] = u
	return nil
}

func fakeIssuer(_ context.Context, u gen.User) (*TokenPair, error) {
	return &TokenPair{
		AccessToken:  "token-" + u.ID.String(),
		RefreshToken: "refresh-" + u.ID.String(),
		ExpiresAt:    time.Now().Add(time.Hour),
		UserID:       u.ID,
		Username:     u.Username,
		IsAdmin:      u.IsAdmin,
	}, nil
}

// TestLoginOrCreateSAMLUser_FirstUserBecomesAdmin mirrors the OIDC
// behavior — the very first user provisioned (any auth source) is
// admin, regardless of whether the IdP claims they're in the admin
// group, so a fresh install never locks itself out.
func TestLoginOrCreateSAMLUser_FirstUserBecomesAdmin(t *testing.T) {
	db := newFakeSAMLDB()
	svc := NewSAMLAuthService(db, fakeIssuer, slog.Default())

	tokens, err := svc.LoginOrCreateSAMLUser(context.Background(), SAMLProfile{
		Issuer: "idp", Subject: "user1", Email: "first@example.com", Username: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !tokens.IsAdmin {
		t.Error("first user must be admin")
	}
}

// TestLoginOrCreateSAMLUser_ExistingLinkedUser short-circuits — when
// the (issuer, subject) pair already maps to a user, we mint tokens
// without trying email-link or JIT-create.
func TestLoginOrCreateSAMLUser_ExistingLinkedUser(t *testing.T) {
	db := newFakeSAMLDB()
	svc := NewSAMLAuthService(db, fakeIssuer, slog.Default())
	// Seed: one existing linked user.
	issuer, subject := "idp", "user1"
	uid := uuid.New()
	db.users[uid] = gen.User{ID: uid, Username: "alice", IsAdmin: false}
	db.bySAML["idp|user1"] = db.users[uid]

	tokens, err := svc.LoginOrCreateSAMLUser(context.Background(), SAMLProfile{
		Issuer: issuer, Subject: subject, Email: "alice@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.UserID != uid {
		t.Errorf("expected existing user %s, got %s", uid, tokens.UserID)
	}
	if len(db.linked) != 0 {
		t.Errorf("must not re-link an already-linked user")
	}
}

// TestLoginOrCreateSAMLUser_LinksStubByEmail mirrors the OIDC test
// — invite-provisioned stub users (no password, no OIDC link) get
// upgraded to a SAML link when the email matches.
func TestLoginOrCreateSAMLUser_LinksStubByEmail(t *testing.T) {
	db := newFakeSAMLDB()
	svc := NewSAMLAuthService(db, fakeIssuer, slog.Default())
	stubID := uuid.New()
	emailPtr := "stub@example.com"
	db.users[stubID] = gen.User{ID: stubID, Username: "stub", Email: &emailPtr}
	db.byEmail[emailPtr] = db.users[stubID]

	tokens, err := svc.LoginOrCreateSAMLUser(context.Background(), SAMLProfile{
		Issuer: "idp", Subject: "stubsub", Email: "stub@example.com", Username: "stub",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.UserID != stubID {
		t.Errorf("expected stub %s linked, got new user %s", stubID, tokens.UserID)
	}
	if len(db.linked) != 1 {
		t.Errorf("expected one link op, got %d", len(db.linked))
	}
}

// TestLoginOrCreateSAMLUser_RejectsEmailMatchOnCredentialedUser is
// the security-critical cell — an existing user with a password
// must NOT be hijacked just because their email shows up in a SAML
// assertion. Returns the SSO_ACCOUNT_CONFLICT sentinel.
func TestLoginOrCreateSAMLUser_RejectsEmailMatchOnCredentialedUser(t *testing.T) {
	db := newFakeSAMLDB()
	svc := NewSAMLAuthService(db, fakeIssuer, slog.Default())
	emailPtr := "fullaccount@example.com"
	pwHash := "bcrypt-hash"
	uid := uuid.New()
	db.users[uid] = gen.User{
		ID: uid, Username: "fullaccount", Email: &emailPtr,
		PasswordHash: &pwHash, // credentialed — not a stub
	}
	db.byEmail[emailPtr] = db.users[uid]

	_, err := svc.LoginOrCreateSAMLUser(context.Background(), SAMLProfile{
		Issuer: "idp", Subject: "newsub", Email: emailPtr, Username: "fullaccount",
	})
	if !errors.Is(err, ErrSSOAccountConflict) {
		t.Errorf("expected ErrSSOAccountConflict, got %v", err)
	}
}

func strPtrVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
