package v1

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// GoogleAuthService defines the domain interface for Google SSO.
type GoogleAuthService interface {
	LoginOrCreateGoogleUser(ctx context.Context, googleID, email, name, avatarURL string) (*TokenPair, error)
}

// GoogleOAuthDB defines the database queries needed for Google SSO.
type GoogleOAuthDB interface {
	GetUserByGoogleID(ctx context.Context, googleID *string) (gen.User, error)
	GetUserByEmail(ctx context.Context, email *string) (gen.User, error)
	LinkGoogleAccount(ctx context.Context, arg gen.LinkGoogleAccountParams) error
	CreateGoogleUser(ctx context.Context, arg gen.CreateGoogleUserParams) (gen.User, error)
	CountUsers(ctx context.Context) (int64, error)
}

// GoogleOAuthHandler handles Google OAuth2 SSO.
type GoogleOAuthHandler struct {
	oauth2Cfg *oauth2.Config
	clientID  string
	svc       GoogleAuthService
	logger    *slog.Logger
}

// NewGoogleOAuthHandler creates a GoogleOAuthHandler.
func NewGoogleOAuthHandler(clientID, clientSecret, baseURL string, svc GoogleAuthService, logger *slog.Logger) *GoogleOAuthHandler {
	return &GoogleOAuthHandler{
		oauth2Cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     google.Endpoint,
			RedirectURL:  baseURL + "/api/v1/auth/google/callback",
			Scopes:       []string{"openid", "email", "profile"},
		},
		clientID: clientID,
		svc:      svc,
		logger:   logger,
	}
}

const oauthStateCookie = "onscreen_oauth_state"

// Redirect initiates the Google OAuth2 flow.
// GET /api/v1/auth/google
func (h *GoogleOAuthHandler) Redirect(w http.ResponseWriter, r *http.Request) {
	state, err := randomState()
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/api/v1/auth/google",
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300, // 5 minutes
	})
	url := h.oauth2Cfg.AuthCodeURL(state, oauth2.AccessTypeOffline)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// Callback handles the Google OAuth2 redirect.
// GET /api/v1/auth/google/callback
func (h *GoogleOAuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	// Validate state.
	stateCookie, err := r.Cookie(oauthStateCookie)
	if err != nil || stateCookie.Value == "" {
		respond.BadRequest(w, r, "missing OAuth state")
		return
	}
	// Clear state cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    "",
		Path:     "/api/v1/auth/google",
		HttpOnly: true,
		MaxAge:   -1,
	})
	if r.URL.Query().Get("state") != stateCookie.Value {
		respond.BadRequest(w, r, "invalid OAuth state")
		return
	}

	// Check for error from Google.
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.logger.Warn("google oauth error", "error", errParam)
		http.Redirect(w, r, "/login?error=google_denied", http.StatusTemporaryRedirect)
		return
	}

	// Exchange code for token.
	code := r.URL.Query().Get("code")
	if code == "" {
		respond.BadRequest(w, r, "missing authorization code")
		return
	}
	token, err := h.oauth2Cfg.Exchange(r.Context(), code)
	if err != nil {
		h.logger.Error("google oauth exchange failed", "err", err)
		http.Redirect(w, r, "/login?error=google_failed", http.StatusTemporaryRedirect)
		return
	}

	// Extract user info from ID token.
	idTokenStr, ok := token.Extra("id_token").(string)
	if !ok {
		h.logger.Error("google oauth: no id_token in response")
		http.Redirect(w, r, "/login?error=google_failed", http.StatusTemporaryRedirect)
		return
	}
	claims, err := parseIDTokenClaims(idTokenStr)
	if err != nil {
		h.logger.Error("google oauth: parse id_token", "err", err)
		http.Redirect(w, r, "/login?error=google_failed", http.StatusTemporaryRedirect)
		return
	}

	googleID, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	name, _ := claims["name"].(string)
	picture, _ := claims["picture"].(string)
	emailVerified, _ := claims["email_verified"].(bool)

	if googleID == "" || email == "" {
		h.logger.Error("google oauth: missing sub or email in id_token")
		http.Redirect(w, r, "/login?error=google_failed", http.StatusTemporaryRedirect)
		return
	}
	if !emailVerified {
		h.logger.Warn("google oauth: unverified email", "email", email)
		http.Redirect(w, r, "/login?error=email_unverified", http.StatusTemporaryRedirect)
		return
	}

	// Login or create user.
	pair, err := h.svc.LoginOrCreateGoogleUser(r.Context(), googleID, email, name, picture)
	if err != nil {
		h.logger.Error("google oauth: login/create user", "err", err)
		http.Redirect(w, r, "/login?error=google_failed", http.StatusTemporaryRedirect)
		return
	}

	// Set auth cookies and redirect to home.
	setAuthCookies(w, r, pair)
	http.Redirect(w, r, "/?google_auth=1", http.StatusTemporaryRedirect)
}

// Enabled returns whether Google SSO is configured.
// GET /api/v1/auth/google/enabled
func (h *GoogleOAuthHandler) Enabled(w http.ResponseWriter, r *http.Request) {
	respond.Success(w, r, map[string]interface{}{
		"enabled":   true,
		"client_id": h.clientID,
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// parseIDTokenClaims decodes the JWT payload without signature verification.
// This is safe because the token was received directly from Google's token
// endpoint over TLS — it cannot be tampered with.
func parseIDTokenClaims(idToken string) (map[string]interface{}, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid id_token: expected 3 parts, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode id_token payload: %w", err)
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal id_token claims: %w", err)
	}
	return claims, nil
}

// ── googleAuthService implements GoogleAuthService ──────────────────────────

type googleAuthService struct {
	db          GoogleOAuthDB
	issueTokens func(ctx context.Context, user gen.User) (*TokenPair, error)
	logger      *slog.Logger
}

// NewGoogleAuthService creates a GoogleAuthService backed by the given DB and token issuer.
func NewGoogleAuthService(db GoogleOAuthDB, issueTokens func(ctx context.Context, user gen.User) (*TokenPair, error), logger *slog.Logger) GoogleAuthService {
	return &googleAuthService{db: db, issueTokens: issueTokens, logger: logger}
}

var sanitizeUsername = regexp.MustCompile(`[^a-zA-Z0-9_]`)

func (s *googleAuthService) LoginOrCreateGoogleUser(ctx context.Context, googleID, email, name, avatarURL string) (*TokenPair, error) {
	// 1. Fast path: user already linked by Google ID.
	user, err := s.db.GetUserByGoogleID(ctx, &googleID)
	if err == nil {
		return s.issueTokens(ctx, user)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get user by google_id: %w", err)
	}

	// 2. Try to find by email and link.
	user, err = s.db.GetUserByEmail(ctx, &email)
	if err == nil {
		var av *string
		if avatarURL != "" {
			av = &avatarURL
		}
		if linkErr := s.db.LinkGoogleAccount(ctx, gen.LinkGoogleAccountParams{
			ID:              user.ID,
			GoogleID:        &googleID,
			GoogleAvatarUrl: av,
			Email:           &email,
		}); linkErr != nil {
			s.logger.Warn("failed to link google account", "user_id", user.ID, "err", linkErr)
		}
		return s.issueTokens(ctx, user)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get user by email: %w", err)
	}

	// 3. Create new user.
	username := deriveUsername(name, email)
	count, err := s.db.CountUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	isAdmin := count == 0 // First user becomes admin.

	var av *string
	if avatarURL != "" {
		av = &avatarURL
	}
	user, err = s.db.CreateGoogleUser(ctx, gen.CreateGoogleUserParams{
		Username:        username,
		Email:           &email,
		GoogleID:        &googleID,
		GoogleAvatarUrl: av,
		IsAdmin:         isAdmin,
	})
	if err != nil {
		return nil, fmt.Errorf("create google user: %w", err)
	}
	return s.issueTokens(ctx, user)
}

// deriveUsername creates a valid username from the Google profile name or email.
func deriveUsername(name, email string) string {
	candidate := name
	if candidate == "" {
		// Use email prefix.
		candidate = strings.SplitN(email, "@", 2)[0]
	}
	// Sanitize: remove invalid characters, truncate.
	candidate = sanitizeUsername.ReplaceAllString(candidate, "_")
	candidate = strings.Trim(candidate, "_")
	if len(candidate) < 2 {
		candidate = "user_" + candidate
	}
	if len(candidate) > 32 {
		candidate = candidate[:32]
	}
	// Add a short random suffix to avoid collisions.
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	candidate = candidate + "_" + fmt.Sprintf("%x", suffix)
	if len(candidate) > 32 {
		candidate = candidate[:32]
	}
	return candidate
}

// ── placeholder for when Google is not configured ───────────────────────────

// GoogleDisabledHandler returns a handler that reports Google SSO as disabled.
func GoogleDisabledHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respond.Success(w, r, map[string]interface{}{
			"enabled": false,
		})
	}
}

// SetAuthCookiesFromPair is an exported wrapper so the google handler can set cookies.
// This just calls the package-level setAuthCookies.
func SetAuthCookiesFromPair(w http.ResponseWriter, r *http.Request, pair *TokenPair) {
	setAuthCookies(w, r, pair)
}

// IssueTokenPairFn is the function signature for issuing token pairs.
type IssueTokenPairFn func(ctx context.Context, user gen.User) (*TokenPair, error)

// We need access to the unexported issueTokenPair. Instead of exporting it,
// we pass it as a function when constructing the service. See NewGoogleAuthService.
// The existing `setAuthCookies` in auth.go is already accessible within the package.
// And `isSecure` is also accessible. So the handler can use them directly.
