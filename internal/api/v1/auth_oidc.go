package v1

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// OIDCSettingsReader is the slice of settings.Service the OIDC handler needs.
// Defined locally so tests can pass a fake without importing the full service.
type OIDCSettingsReader interface {
	OIDC(ctx context.Context) settings.OIDCConfig
}

// OIDCAuthService logs in or provisions a user from an OIDC ID token.
type OIDCAuthService interface {
	LoginOrCreateOIDCUser(ctx context.Context, profile OIDCProfile) (*TokenPair, error)
}

// OIDCProfile is the subset of ID-token claims the auth service needs.
type OIDCProfile struct {
	Issuer        string
	Subject       string
	Email         string // empty if email_verified was not strictly true
	EmailVerified bool   // copied from the email_verified claim
	Username      string // already-derived username (preferred_username, email-prefix, or sub)
	IsAdmin       bool   // true if the user is in the configured admin group
	GroupSync     bool   // whether AdminGroup was configured (controls is_admin overwrite)
}

// OIDCOAuthDB is the DB subset needed by the OIDC auth service.
type OIDCOAuthDB interface {
	GetUserByOIDCSubject(ctx context.Context, arg gen.GetUserByOIDCSubjectParams) (gen.User, error)
	GetUserByEmail(ctx context.Context, email *string) (gen.User, error)
	LinkOIDCAccount(ctx context.Context, arg gen.LinkOIDCAccountParams) error
	CreateOIDCUser(ctx context.Context, arg gen.CreateOIDCUserParams) (gen.User, error)
	CountUsers(ctx context.Context) (int64, error)
	SetUserAdmin(ctx context.Context, arg gen.SetUserAdminParams) error
}

// ── handler ─────────────────────────────────────────────────────────────────

// OIDCHandler exposes the OIDC SSO endpoints. The IdP config lives in DB
// settings — the handler lazy-loads it on each request and rebuilds its
// verifier/oauth2.Config when the issuer/client identity changes (no restart
// needed when an admin enables or reconfigures the IdP).
type OIDCHandler struct {
	cfgSrc  OIDCSettingsReader
	svc     OIDCAuthService
	baseURL string
	logger  *slog.Logger

	mu       sync.RWMutex
	cached   settings.OIDCConfig // last-built config (for cache-key comparison)
	provider *oidc.Provider
	oauth2   *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// NewOIDCHandler creates an OIDCHandler. baseURL is the public URL used to
// construct the redirect URI (e.g. "https://onscreen.example.com").
func NewOIDCHandler(cfgSrc OIDCSettingsReader, svc OIDCAuthService, baseURL string, logger *slog.Logger) *OIDCHandler {
	return &OIDCHandler{cfgSrc: cfgSrc, svc: svc, baseURL: baseURL, logger: logger}
}

const oidcStateCookie = "onscreen_oidc_state"

// oidcFlowState is the per-request data the callback needs to verify the
// response: the CSRF state token, the nonce we put in the auth request, and
// the PKCE code verifier. Persisted in a single httpOnly cookie scoped to the
// callback path so a callback in a different browser/session can't reuse it.
type oidcFlowState struct {
	State        string `json:"s"`
	Nonce        string `json:"n"`
	CodeVerifier string `json:"v"`
}

// configKey returns the fields whose change invalidates the cached provider.
// Display-name / username-claim / admin-group changes don't require rebuilding
// the discovery client, so they're excluded.
func configKey(c settings.OIDCConfig) string {
	return c.IssuerURL + "|" + c.ClientID + "|" + c.ClientSecret
}

// resolve loads the current OIDC config and (re)builds the OAuth2 + verifier
// pair if anything that affects discovery has changed. Returns ErrOIDCDisabled
// if the integration is turned off or misconfigured.
func (h *OIDCHandler) resolve(ctx context.Context) (settings.OIDCConfig, *oauth2.Config, *oidc.IDTokenVerifier, error) {
	cfg := h.cfgSrc.OIDC(ctx)
	if !cfg.Enabled || cfg.IssuerURL == "" || cfg.ClientID == "" {
		return cfg, nil, nil, ErrOIDCDisabled
	}

	h.mu.RLock()
	if h.oauth2 != nil && configKey(h.cached) == configKey(cfg) {
		oauth, ver := h.oauth2, h.verifier
		h.mu.RUnlock()
		return cfg, oauth, ver, nil
	}
	h.mu.RUnlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	// Re-check inside the write lock — another goroutine may have built it.
	if h.oauth2 != nil && configKey(h.cached) == configKey(cfg) {
		return cfg, h.oauth2, h.verifier, nil
	}

	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return cfg, nil, nil, fmt.Errorf("oidc discovery: %w", err)
	}
	scopes := strings.Fields(cfg.Scopes)
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	oauth := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  h.baseURL + "/api/v1/auth/oidc/callback",
		Scopes:       scopes,
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	h.cached = cfg
	h.provider = provider
	h.oauth2 = oauth
	h.verifier = verifier
	return cfg, oauth, verifier, nil
}

// ErrOIDCDisabled is returned when OIDC is not enabled in settings.
var ErrOIDCDisabled = errors.New("oidc not enabled")

// Enabled returns whether OIDC SSO is configured. Drives the login page button.
// GET /api/v1/auth/oidc/enabled
func (h *OIDCHandler) Enabled(w http.ResponseWriter, r *http.Request) {
	cfg := h.cfgSrc.OIDC(r.Context())
	enabled := cfg.Enabled && cfg.IssuerURL != "" && cfg.ClientID != ""
	name := cfg.DisplayName
	if name == "" {
		name = "SSO"
	}
	respond.Success(w, r, map[string]any{
		"enabled":      enabled,
		"display_name": name,
	})
}

// Redirect kicks off the OIDC flow.
// GET /api/v1/auth/oidc
func (h *OIDCHandler) Redirect(w http.ResponseWriter, r *http.Request) {
	_, oauth, _, err := h.resolve(r.Context())
	if err != nil {
		http.Redirect(w, r, "/login?error=oidc_disabled", http.StatusTemporaryRedirect)
		return
	}
	state, err := randomState()
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	nonce, err := randomState()
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	verifier := oauth2.GenerateVerifier()

	flow := oidcFlowState{State: state, Nonce: nonce, CodeVerifier: verifier}
	cookieVal, err := encodeOIDCFlow(flow)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    cookieVal,
		Path:     "/api/v1/auth/oidc",
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300,
	})
	authURL := oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// Callback handles the IdP redirect.
// GET /api/v1/auth/oidc/callback
func (h *OIDCHandler) Callback(w http.ResponseWriter, r *http.Request) {
	cfg, oauth, verifier, err := h.resolve(r.Context())
	if err != nil {
		http.Redirect(w, r, "/login?error=oidc_disabled", http.StatusTemporaryRedirect)
		return
	}

	stateCookie, err := r.Cookie(oidcStateCookie)
	if err != nil || stateCookie.Value == "" {
		respond.BadRequest(w, r, "invalid OIDC state")
		return
	}
	flow, err := decodeOIDCFlow(stateCookie.Value)
	if err != nil || flow.State == "" || r.URL.Query().Get("state") != flow.State {
		respond.BadRequest(w, r, "invalid OIDC state")
		return
	}
	// Clear state cookie — single use.
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    "",
		Path:     "/api/v1/auth/oidc",
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.logger.WarnContext(r.Context(), "oidc: idp returned error", "error", errParam)
		http.Redirect(w, r, "/login?error=oidc_denied", http.StatusTemporaryRedirect)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		respond.BadRequest(w, r, "missing authorization code")
		return
	}

	token, err := oauth.Exchange(r.Context(), code, oauth2.VerifierOption(flow.CodeVerifier))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "oidc: token exchange", "err", err)
		http.Redirect(w, r, "/login?error=oidc_failed", http.StatusTemporaryRedirect)
		return
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		h.logger.ErrorContext(r.Context(), "oidc: no id_token in response")
		http.Redirect(w, r, "/login?error=oidc_failed", http.StatusTemporaryRedirect)
		return
	}
	idToken, err := verifier.Verify(r.Context(), rawID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "oidc: verify id_token", "err", err)
		http.Redirect(w, r, "/login?error=oidc_failed", http.StatusTemporaryRedirect)
		return
	}
	if idToken.Nonce != flow.Nonce {
		h.logger.ErrorContext(r.Context(), "oidc: nonce mismatch")
		http.Redirect(w, r, "/login?error=oidc_failed", http.StatusTemporaryRedirect)
		return
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		h.logger.ErrorContext(r.Context(), "oidc: parse claims", "err", err)
		http.Redirect(w, r, "/login?error=oidc_failed", http.StatusTemporaryRedirect)
		return
	}

	profile, err := buildProfile(cfg, idToken.Subject, idToken.Issuer, claims)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "oidc: build profile", "err", err)
		http.Redirect(w, r, "/login?error=oidc_failed", http.StatusTemporaryRedirect)
		return
	}

	pair, err := h.svc.LoginOrCreateOIDCUser(r.Context(), profile)
	if err != nil {
		if errors.Is(err, ErrSSOAccountConflict) {
			h.logger.WarnContext(r.Context(), "oidc: account conflict on auto-link", "username", profile.Username)
			http.Redirect(w, r, "/login?error=account_exists", http.StatusTemporaryRedirect)
			return
		}
		h.logger.ErrorContext(r.Context(), "oidc: login/create", "err", err)
		http.Redirect(w, r, "/login?error=oidc_failed", http.StatusTemporaryRedirect)
		return
	}
	setAuthCookies(w, r, pair)
	http.Redirect(w, r, "/?oidc_auth=1", http.StatusTemporaryRedirect)
}

// encodeOIDCFlow serializes the per-request flow state into a cookie value.
// Compact JSON + base64url so the entire packet (state + nonce + verifier) fits
// in a single cookie ~150 bytes.
func encodeOIDCFlow(f oidcFlowState) (string, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeOIDCFlow(s string) (oidcFlowState, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return oidcFlowState{}, err
	}
	var f oidcFlowState
	if err := json.Unmarshal(b, &f); err != nil {
		return oidcFlowState{}, err
	}
	return f, nil
}

// buildProfile pulls the relevant fields out of the verified ID token claims.
// Username precedence: configured UsernameClaim → preferred_username → email-prefix → subject.
//
// Email is *only* propagated when the IdP asserts email_verified=true. Without
// that guard, an attacker can add an unverified email to their IdP account
// (Google, Auth0, Keycloak all allow this on free tiers) that matches a victim's
// local account, then have the SSO flow auto-link them. See LoginOrCreateOIDCUser
// for the second half of the defence.
func buildProfile(cfg settings.OIDCConfig, subject, issuer string, claims map[string]any) (OIDCProfile, error) {
	if subject == "" {
		return OIDCProfile{}, errors.New("missing sub claim")
	}
	rawEmail, _ := claims["email"].(string)
	emailVerified, _ := claims["email_verified"].(bool)

	// Determine username (this is allowed to use the raw email even if unverified —
	// it's only an account-name suggestion, not an identity claim).
	usernameClaim := cfg.UsernameClaim
	if usernameClaim == "" {
		usernameClaim = "preferred_username"
	}
	username, _ := claims[usernameClaim].(string)
	if username == "" {
		// Try preferred_username explicitly even if a custom claim was set but missing.
		username, _ = claims["preferred_username"].(string)
	}
	if username == "" && rawEmail != "" {
		username = strings.SplitN(rawEmail, "@", 2)[0]
	}
	if username == "" {
		username = "user_" + subject
	}
	username = sanitizeUsername.ReplaceAllString(username, "_")
	username = strings.Trim(username, "_")
	if len(username) > 32 {
		username = username[:32]
	}
	if len(username) < 2 {
		username = "user_" + subject
		if len(username) > 32 {
			username = username[:32]
		}
	}

	// Group → admin mapping.
	isAdmin, groupSync := false, cfg.AdminGroup != ""
	if groupSync {
		groupsClaim := cfg.GroupsClaim
		if groupsClaim == "" {
			groupsClaim = "groups"
		}
		switch groups := claims[groupsClaim].(type) {
		case []any:
			for _, g := range groups {
				if s, _ := g.(string); s == cfg.AdminGroup {
					isAdmin = true
					break
				}
			}
		case []string:
			for _, g := range groups {
				if g == cfg.AdminGroup {
					isAdmin = true
					break
				}
			}
		}
	}

	// Only carry the email forward when the IdP confirmed it. The unverified
	// value is dropped entirely — including it on the new user record would let
	// it be used by a future linker.
	email := ""
	if emailVerified {
		email = rawEmail
	}

	return OIDCProfile{
		Issuer:        issuer,
		Subject:       subject,
		Email:         email,
		EmailVerified: emailVerified,
		Username:      username,
		IsAdmin:       isAdmin,
		GroupSync:     groupSync,
	}, nil
}

// ── service ─────────────────────────────────────────────────────────────────

type oidcAuthService struct {
	db          OIDCOAuthDB
	issueTokens IssueTokenPairFn
	logger      *slog.Logger
}

// NewOIDCAuthService creates an OIDCAuthService backed by the given DB and token issuer.
func NewOIDCAuthService(db OIDCOAuthDB, issueTokens IssueTokenPairFn, logger *slog.Logger) OIDCAuthService {
	return &oidcAuthService{db: db, issueTokens: issueTokens, logger: logger}
}

func (s *oidcAuthService) LoginOrCreateOIDCUser(ctx context.Context, p OIDCProfile) (*TokenPair, error) {
	issuer, subject := p.Issuer, p.Subject
	// 1. Already linked.
	user, err := s.db.GetUserByOIDCSubject(ctx, gen.GetUserByOIDCSubjectParams{
		OidcIssuer: &issuer, OidcSubject: &subject,
	})
	if err == nil {
		s.maybeSyncAdmin(ctx, user, p)
		return s.issueTokens(ctx, user)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get user by oidc: %w", err)
	}

	// 2. Email-based linking — only when verified AND only into a stub row.
	// A non-stub row already has a credential (password or another SSO link),
	// so trusting an email match would let an IdP user hijack that account.
	if p.Email != "" {
		emailPtr := p.Email
		user, err = s.db.GetUserByEmail(ctx, &emailPtr)
		if err == nil {
			if !isStubUser(user) {
				return nil, ErrSSOAccountConflict
			}
			if linkErr := s.db.LinkOIDCAccount(ctx, gen.LinkOIDCAccountParams{
				ID: user.ID, OidcIssuer: &issuer, OidcSubject: &subject, Email: &emailPtr,
			}); linkErr != nil {
				s.logger.Warn("oidc: link existing account", "user_id", user.ID, "err", linkErr)
			}
			s.maybeSyncAdmin(ctx, user, p)
			return s.issueTokens(ctx, user)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get user by email: %w", err)
		}
	}

	// 3. Create new user. First-ever user is admin (parity with local registration).
	count, err := s.db.CountUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	isAdmin := p.IsAdmin || count == 0
	var emailPtr *string
	if p.Email != "" {
		e := p.Email
		emailPtr = &e
	}
	user, err = s.db.CreateOIDCUser(ctx, gen.CreateOIDCUserParams{
		Username:    p.Username,
		Email:       emailPtr,
		OidcIssuer:  &issuer,
		OidcSubject: &subject,
		IsAdmin:     isAdmin,
	})
	if err != nil {
		return nil, fmt.Errorf("create oidc user: %w", err)
	}
	return s.issueTokens(ctx, user)
}

// maybeSyncAdmin updates is_admin to match the current group membership when
// an AdminGroup is configured. Without group sync we leave is_admin alone so
// admins can be promoted via the UI without an IdP edit.
func (s *oidcAuthService) maybeSyncAdmin(ctx context.Context, user gen.User, p OIDCProfile) {
	if !p.GroupSync || user.IsAdmin == p.IsAdmin {
		return
	}
	if err := s.db.SetUserAdmin(ctx, gen.SetUserAdminParams{ID: user.ID, IsAdmin: p.IsAdmin}); err != nil {
		s.logger.Warn("oidc: sync admin", "user_id", user.ID, "err", err)
	}
}
