package v1

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/safehttp"
)

// LDAPSettingsReader is the slice of settings.Service the LDAP handler needs.
type LDAPSettingsReader interface {
	LDAP(ctx context.Context) settings.LDAPConfig
}

// LDAPDialer abstracts the LDAP connection so tests can inject a fake.
type LDAPDialer interface {
	Dial(cfg settings.LDAPConfig) (LDAPConn, error)
}

// LDAPConn is the subset of *ldap.Conn we use.
type LDAPConn interface {
	Bind(username, password string) error
	Search(req *ldap.SearchRequest) (*ldap.SearchResult, error)
	Close() error
}

// LDAPAuthService logs in or provisions a user from LDAP.
type LDAPAuthService interface {
	LoginLDAP(ctx context.Context, username, password string) (*TokenPair, error)
}

// LDAPOAuthDB is the DB subset the LDAP auth service needs.
type LDAPOAuthDB interface {
	GetUserByLDAPDN(ctx context.Context, ldapDn *string) (gen.User, error)
	GetUserByEmail(ctx context.Context, email *string) (gen.User, error)
	GetUserByUsername(ctx context.Context, username string) (gen.User, error)
	LinkLDAPAccount(ctx context.Context, arg gen.LinkLDAPAccountParams) error
	CreateLDAPUser(ctx context.Context, arg gen.CreateLDAPUserParams) (gen.User, error)
	CountUsers(ctx context.Context) (int64, error)
	SetUserAdmin(ctx context.Context, arg gen.SetUserAdminParams) error
	// GrantAutoLibrariesToUser inserts library_access rows for every
	// library flagged auto_grant_new_users — called after auto-provisioning
	// an LDAP user so they don't land on a barren home page on all-private
	// installs.
	GrantAutoLibrariesToUser(ctx context.Context, userID uuid.UUID) error
}

// ── handler ─────────────────────────────────────────────────────────────────

// LDAPHandler exposes POST /api/v1/auth/ldap/login.
type LDAPHandler struct {
	cfgSrc LDAPSettingsReader
	svc    LDAPAuthService
	logger *slog.Logger
}

// NewLDAPHandler creates a handler.
func NewLDAPHandler(cfgSrc LDAPSettingsReader, svc LDAPAuthService, logger *slog.Logger) *LDAPHandler {
	return &LDAPHandler{cfgSrc: cfgSrc, svc: svc, logger: logger}
}

// Enabled reports whether LDAP login is configured. Drives the login form.
// GET /api/v1/auth/ldap/enabled
func (h *LDAPHandler) Enabled(w http.ResponseWriter, r *http.Request) {
	cfg := h.cfgSrc.LDAP(r.Context())
	enabled := cfg.Enabled && cfg.Host != "" && cfg.UserSearchBase != "" && cfg.UserFilter != ""
	name := cfg.DisplayName
	if name == "" {
		name = "LDAP"
	}
	respond.Success(w, r, map[string]any{
		"enabled":      enabled,
		"display_name": name,
	})
}

// Login handles POST /api/v1/auth/ldap/login.
// Body: { username, password }
func (h *LDAPHandler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" || body.Password == "" {
		respond.BadRequest(w, r, "username and password are required")
		return
	}
	pair, err := h.svc.LoginLDAP(r.Context(), body.Username, body.Password)
	if err != nil {
		if errors.Is(err, ErrLDAPDisabled) {
			respond.JSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": "ldap not configured"})
			return
		}
		if errors.Is(err, ErrLDAPInvalidCredentials) {
			respond.JSON(w, r, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
			return
		}
		if errors.Is(err, ErrSSOAccountConflict) {
			respond.JSON(w, r, http.StatusConflict, map[string]string{
				"error": "an existing local account uses this identifier; ask an admin to link the LDAP identity",
			})
			return
		}
		h.logger.ErrorContext(r.Context(), "ldap login", "err", err)
		respond.JSON(w, r, http.StatusBadGateway, map[string]string{"error": "ldap upstream error"})
		return
	}
	setAuthCookies(w, r, pair)
	respond.Success(w, r, pair)
}

// ── service ─────────────────────────────────────────────────────────────────

// ErrLDAPDisabled is returned when LDAP isn't configured.
var ErrLDAPDisabled = errors.New("ldap not enabled")

// ErrLDAPInvalidCredentials is returned for bad username/password (any LDAP failure
// during user search or user-bind that we map to "the user got it wrong").
var ErrLDAPInvalidCredentials = errors.New("invalid credentials")

type ldapAuthService struct {
	cfgSrc      LDAPSettingsReader
	dialer      LDAPDialer
	db          LDAPOAuthDB
	issueTokens IssueTokenPairFn
	logger      *slog.Logger
}

// NewLDAPAuthService creates an LDAP auth service. The dialer can be the
// default *defaultLDAPDialer or a fake for tests.
func NewLDAPAuthService(cfgSrc LDAPSettingsReader, dialer LDAPDialer, db LDAPOAuthDB, issueTokens IssueTokenPairFn, logger *slog.Logger) LDAPAuthService {
	return &ldapAuthService{cfgSrc: cfgSrc, dialer: dialer, db: db, issueTokens: issueTokens, logger: logger}
}

func (s *ldapAuthService) LoginLDAP(ctx context.Context, username, password string) (*TokenPair, error) {
	cfg := s.cfgSrc.LDAP(ctx)
	if !cfg.Enabled || cfg.Host == "" || cfg.UserSearchBase == "" || cfg.UserFilter == "" {
		return nil, ErrLDAPDisabled
	}

	conn, err := s.dialer.Dial(cfg)
	if err != nil {
		return nil, fmt.Errorf("ldap dial: %w", err)
	}
	defer conn.Close()

	if cfg.BindDN != "" {
		if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("ldap service bind: %w", err)
		}
	}

	usernameAttr := cfg.UsernameAttr
	if usernameAttr == "" {
		usernameAttr = "uid"
	}
	emailAttr := cfg.EmailAttr
	if emailAttr == "" {
		emailAttr = "mail"
	}
	filter := strings.ReplaceAll(cfg.UserFilter, "{username}", ldap.EscapeFilter(username))
	attrs := []string{"dn", usernameAttr, emailAttr, "memberOf"}
	res, err := conn.Search(ldap.NewSearchRequest(
		cfg.UserSearchBase,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		2, // Size limit: catch ambiguous filters early.
		0, false,
		filter, attrs, nil,
	))
	if err != nil {
		return nil, fmt.Errorf("ldap search: %w", err)
	}
	if len(res.Entries) == 0 {
		return nil, ErrLDAPInvalidCredentials
	}
	if len(res.Entries) > 1 {
		// Ambiguous filter — log server-side, but return invalid-credentials to
		// the client so a misconfigured filter can't be used to enumerate the
		// directory via behaviour diff vs. "user not found".
		s.logger.Warn("ldap: ambiguous filter — refusing login", "matches", len(res.Entries), "filter", filter)
		return nil, ErrLDAPInvalidCredentials
	}
	entry := res.Entries[0]

	// Re-bind as the user with the supplied password — this is the actual auth step.
	if err := conn.Bind(entry.DN, password); err != nil {
		var lerr *ldap.Error
		if errors.As(err, &lerr) && lerr.ResultCode == ldap.LDAPResultInvalidCredentials {
			return nil, ErrLDAPInvalidCredentials
		}
		return nil, fmt.Errorf("ldap user bind: %w", err)
	}

	resolvedUsername := entry.GetAttributeValue(usernameAttr)
	if resolvedUsername == "" {
		resolvedUsername = username
	}
	resolvedUsername = sanitizeUsername.ReplaceAllString(resolvedUsername, "_")
	if len(resolvedUsername) > 32 {
		resolvedUsername = resolvedUsername[:32]
	}
	email := entry.GetAttributeValue(emailAttr)
	isAdmin := false
	groupSync := cfg.AdminGroupDN != ""
	if groupSync {
		for _, g := range entry.GetAttributeValues("memberOf") {
			if strings.EqualFold(g, cfg.AdminGroupDN) {
				isAdmin = true
				break
			}
		}
	}

	return s.loginOrCreate(ctx, entry.DN, resolvedUsername, email, isAdmin, groupSync)
}

// loginOrCreate handles the lookup → link → create flow. Mirrors the OIDC
// service: dn first, then email, finally a fresh row.
//
// Both the email and username link paths require the matched local row to be
// a stub (no password, no other identity binding). Without that gate any
// remote LDAP user could hijack a local account that happens to share an
// email or username — see auth_sso.go:isStubUser for the full rationale.
func (s *ldapAuthService) loginOrCreate(ctx context.Context, dn, username, email string, isAdmin, groupSync bool) (*TokenPair, error) {
	dnPtr := dn
	user, err := s.db.GetUserByLDAPDN(ctx, &dnPtr)
	if err == nil {
		s.maybeSyncAdmin(ctx, user, isAdmin, groupSync)
		return s.issueTokens(ctx, user)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get user by ldap dn: %w", err)
	}

	// Try linking by email — only into a stub row.
	if email != "" {
		emailPtr := email
		user, err = s.db.GetUserByEmail(ctx, &emailPtr)
		if err == nil {
			if !isStubUser(user) {
				return nil, ErrSSOAccountConflict
			}
			if linkErr := s.db.LinkLDAPAccount(ctx, gen.LinkLDAPAccountParams{
				ID: user.ID, LdapDn: &dnPtr, Email: &emailPtr,
			}); linkErr != nil {
				s.logger.Warn("ldap: link existing account", "user_id", user.ID, "err", linkErr)
			}
			s.maybeSyncAdmin(ctx, user, isAdmin, groupSync)
			return s.issueTokens(ctx, user)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get user by email: %w", err)
		}
	}

	// Try linking by exact username — only into a stub the admin pre-created.
	// A non-stub username match is treated as a conflict, not a free takeover.
	user, err = s.db.GetUserByUsername(ctx, username)
	if err == nil {
		if !isStubUser(user) {
			return nil, ErrSSOAccountConflict
		}
		var emailPtr *string
		if email != "" {
			e := email
			emailPtr = &e
		}
		if linkErr := s.db.LinkLDAPAccount(ctx, gen.LinkLDAPAccountParams{
			ID: user.ID, LdapDn: &dnPtr, Email: emailPtr,
		}); linkErr != nil {
			s.logger.Warn("ldap: link by username", "user_id", user.ID, "err", linkErr)
		}
		s.maybeSyncAdmin(ctx, user, isAdmin, groupSync)
		return s.issueTokens(ctx, user)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get user by username: %w", err)
	}

	// Create a fresh user.
	count, err := s.db.CountUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	createAdmin := isAdmin || count == 0
	var emailPtr *string
	if email != "" {
		e := email
		emailPtr = &e
	}
	user, err = s.db.CreateLDAPUser(ctx, gen.CreateLDAPUserParams{
		Username: username,
		Email:    emailPtr,
		LdapDn:   &dnPtr,
		IsAdmin:  createAdmin,
	})
	if err != nil {
		return nil, fmt.Errorf("create ldap user: %w", err)
	}
	if !createAdmin {
		// Non-fatal: a freshly-provisioned non-admin should default into the
		// admin-chosen library set so first sign-in isn't a barren home page.
		if err := s.db.GrantAutoLibrariesToUser(ctx, user.ID); err != nil {
			s.logger.Warn("ldap: auto-grant libraries", "user_id", user.ID, "err", err)
		}
	}
	return s.issueTokens(ctx, user)
}

func (s *ldapAuthService) maybeSyncAdmin(ctx context.Context, user gen.User, isAdmin, groupSync bool) {
	if !groupSync || user.IsAdmin == isAdmin {
		return
	}
	if err := s.db.SetUserAdmin(ctx, gen.SetUserAdminParams{ID: user.ID, IsAdmin: isAdmin}); err != nil {
		s.logger.Warn("ldap: sync admin", "user_id", user.ID, "err", err)
	}
}

// ── default dialer ──────────────────────────────────────────────────────────

// DefaultLDAPDialer dials a real LDAP server using the go-ldap library.
type DefaultLDAPDialer struct{}

// Dial implements LDAPDialer.
func (DefaultLDAPDialer) Dial(cfg settings.LDAPConfig) (LDAPConn, error) {
	scheme := "ldap"
	if cfg.UseLDAPS {
		scheme = "ldaps"
	}
	url := scheme + "://" + cfg.Host

	// SSRF guard: LDAP is a typical corporate-LAN service, so we
	// permit RFC1918 + loopback (that's where real LDAP servers live).
	// Link-local is NOT allowed — the canonical link-local address an
	// attacker would target is 169.254.169.254, the AWS/GCP/Azure
	// metadata endpoint that returns IAM credentials. Real LDAP
	// servers don't live on link-local; if an operator has one that
	// does, they can run a reverse proxy in front. 0.0.0.0/multicast
	// stay blocked by the default policy.
	var opts []ldap.DialOpt
	opts = append(opts, ldap.DialWithDialer(safehttp.NewDialer(safehttp.DialPolicy{
		AllowPrivate: true, AllowLoopback: true,
	})))
	if cfg.UseLDAPS || cfg.StartTLS {
		// #nosec G402 — admin opt-in for self-signed dev certs only.
		opts = append(opts, ldap.DialWithTLSConfig(&tls.Config{
			InsecureSkipVerify: cfg.SkipTLSVerify,
		}))
	}
	conn, err := ldap.DialURL(url, opts...)
	if err != nil {
		return nil, err
	}
	if cfg.StartTLS && !cfg.UseLDAPS {
		// #nosec G402 — admin opt-in.
		if err := conn.StartTLS(&tls.Config{InsecureSkipVerify: cfg.SkipTLSVerify}); err != nil {
			conn.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	}
	return conn, nil
}
