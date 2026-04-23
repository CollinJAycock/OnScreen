package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
)

// usernameRe matches valid usernames: 2-32 alphanumeric characters or underscores.
var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{2,32}$`)

// ErrUserExists is returned by AuthService.CreateUser when the username is taken.
var ErrUserExists = errors.New("user already exists")

// ErrNotFirstUser is returned by CreateFirstAdmin when the users table
// already has rows. Setup-wizard callers translate to a 409 "setup
// already complete" so the loser of a concurrent setup race gets a
// clear message instead of a misleading 500.
var ErrNotFirstUser = errors.New("users already exist")

// AuthService defines the domain interface for authentication.
type AuthService interface {
	LoginLocal(ctx context.Context, username, password string) (*TokenPair, error)
	Refresh(ctx context.Context, refreshToken string) (*TokenPair, error)
	Logout(ctx context.Context, refreshToken string) error
	CreateUser(ctx context.Context, username, email, password string, isAdmin bool) (*UserInfo, error)
	// CreateFirstAdmin is the atomic setup-wizard path. Inserts the user
	// as admin if and only if the users table is empty. Returns
	// ErrNotFirstUser if someone beat us to it — caller can fall back
	// to admin-authenticated CreateUser or surface a clearer error.
	CreateFirstAdmin(ctx context.Context, username, email, password string) (*UserInfo, error)
	UserCount(ctx context.Context) (int64, error)
}

// TokenPair holds the access token and refresh token returned on login.
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	UserID       uuid.UUID `json:"user_id"`
	Username     string    `json:"username"`
	IsAdmin      bool      `json:"is_admin"`
}

// UserInfo is a public-safe user representation.
type UserInfo struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
	IsAdmin  bool      `json:"is_admin"`
}

// AuthHandler handles auth endpoints.
type AuthHandler struct {
	svc    AuthService
	logger *slog.Logger
	audit  *audit.Logger
}

// NewAuthHandler creates an AuthHandler.
func NewAuthHandler(svc AuthService, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{svc: svc, logger: logger}
}

// WithAudit attaches an audit logger. Returns the handler for chaining.
func (h *AuthHandler) WithAudit(a *audit.Logger) *AuthHandler {
	h.audit = a
	return h
}

// Login handles POST /api/v1/auth/login.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}

	if strings.TrimSpace(body.Username) == "" || body.Password == "" {
		respond.BadRequest(w, r, "username and password are required")
		return
	}

	pair, err := h.svc.LoginLocal(r.Context(), body.Username, body.Password)
	if err != nil {
		if h.audit != nil {
			h.audit.Log(r.Context(), nil, audit.ActionLoginFailed, body.Username, nil, audit.ClientIP(r))
		}
		respond.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "invalid credentials")
		return
	}
	if h.audit != nil {
		h.audit.Log(r.Context(), &pair.UserID, audit.ActionLoginSuccess, pair.Username, nil, audit.ClientIP(r))
	}
	setAuthCookies(w, r, pair)
	respond.Success(w, r, pair)
}

// Refresh handles POST /api/v1/auth/refresh.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	// Read refresh token from JSON body (legacy/API clients) or httpOnly cookie.
	var refreshToken string
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.RefreshToken != "" {
		refreshToken = body.RefreshToken
	} else if c, err := r.Cookie(cookieRefreshToken); err == nil {
		refreshToken = c.Value
	}
	if refreshToken == "" {
		respond.Unauthorized(w, r)
		return
	}

	pair, err := h.svc.Refresh(r.Context(), refreshToken)
	if err != nil {
		clearAuthCookies(w, r)
		respond.Unauthorized(w, r)
		return
	}
	setAuthCookies(w, r, pair)
	respond.Success(w, r, pair)
}

// Logout handles POST /api/v1/auth/logout.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	// Read refresh token from JSON body (legacy/API clients) or httpOnly cookie.
	var refreshToken string
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.RefreshToken != "" {
		refreshToken = body.RefreshToken
	} else if c, err := r.Cookie(cookieRefreshToken); err == nil {
		refreshToken = c.Value
	}
	if refreshToken != "" {
		_ = h.svc.Logout(r.Context(), refreshToken)
	}
	clearAuthCookies(w, r)
	respond.NoContent(w)
}

// Register handles POST /api/v1/auth/register (setup wizard / admin).
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	// Allow registration only if no users exist yet (setup wizard), or if
	// the requester is an admin.
	count, err := h.svc.UserCount(r.Context())
	if err != nil {
		respond.InternalError(w, r)
		return
	}

	if count > 0 {
		// Not first user — require admin.
		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil || !claims.IsAdmin {
			respond.Forbidden(w, r)
			return
		}
	}

	var body struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}

	if strings.TrimSpace(body.Username) == "" || body.Password == "" {
		respond.BadRequest(w, r, "username and password are required")
		return
	}

	if !usernameRe.MatchString(body.Username) {
		respond.BadRequest(w, r, "username must be 2-32 characters, alphanumeric or underscores only")
		return
	}

	if len(body.Password) < 12 {
		respond.BadRequest(w, r, "password must be at least 12 characters")
		return
	}

	// First user is always admin — go through the atomic
	// CreateFirstAdmin path so two concurrent /auth/register calls when
	// count=0 can't both succeed as admins. The SQL uses
	// `INSERT ... WHERE NOT EXISTS` so only one of the racing goroutines
	// wins; the loser gets ErrNotFirstUser and we surface a helpful 409.
	var user *UserInfo
	if count == 0 {
		user, err = h.svc.CreateFirstAdmin(r.Context(), body.Username, body.Email, body.Password)
		if errors.Is(err, ErrNotFirstUser) {
			respond.Error(w, r, http.StatusConflict, "SETUP_COMPLETE",
				"setup already complete — an admin must create additional users")
			return
		}
	} else {
		user, err = h.svc.CreateUser(r.Context(), body.Username, body.Email, body.Password, body.IsAdmin)
	}
	if err != nil {
		if errors.Is(err, ErrUserExists) {
			respond.Error(w, r, http.StatusConflict, "CONFLICT", "username already taken")
			return
		}
		h.logger.ErrorContext(r.Context(), "create user", "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		actorID := user.ID
		// If an admin is creating the user, attribute to the admin.
		if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
			actorID = claims.UserID
		}
		h.audit.Log(r.Context(), &actorID, audit.ActionUserCreate, user.Username, nil, audit.ClientIP(r))
	}
	respond.Created(w, r, user)
}

// ── Cookie-based auth ────────────────────────────────────────────────────────

const (
	cookieAccessToken  = "onscreen_at"
	cookieRefreshToken = "onscreen_rt"
)

// isSecure returns true if the request arrived over HTTPS.
//
// We accept X-Forwarded-Proto only when the request reached us from a
// loopback or RFC1918 private address — i.e., a reverse proxy on the same
// host or the same private network. Internet-facing clients can't influence
// the cookie's Secure flag this way; if you front OnScreen with a proxy on a
// public IP, the proxy must terminate TLS itself or the auth cookies will be
// issued without Secure (which is the safer failure mode).
func isSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") == "https" && remoteAddrIsTrusted(r) {
		return true
	}
	return false
}

// remoteAddrIsTrusted reports whether the request's RemoteAddr is loopback or
// in an RFC1918 / unique-local address range — the proxies we trust to set
// X-Forwarded-Proto. Anything else (public IPs) is rejected.
func remoteAddrIsTrusted(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

// setAuthCookies writes httpOnly cookies for both tokens.
// The access token cookie is sent on all paths; the refresh token is scoped to /api/v1/auth.
func setAuthCookies(w http.ResponseWriter, r *http.Request, pair *TokenPair) {
	secure := isSecure(r)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieAccessToken,
		Value:    pair.AccessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   max(0, int(time.Until(pair.ExpiresAt).Seconds())),
	})
	// Refresh token: long-lived, scoped to auth endpoints only.
	// MaxAge must match auth.RefreshTokenTTL (30 days).
	http.SetCookie(w, &http.Cookie{
		Name:     cookieRefreshToken,
		Value:    pair.RefreshToken,
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   30 * 24 * 60 * 60, // 30 days — matches auth.RefreshTokenTTL
	})
}

// clearAuthCookies expires both auth cookies. Cookie attributes (Path,
// Secure, SameSite) must match what setAuthCookies wrote so browsers
// actually evict them — a Strict-on-write/Lax-on-delete cookie can survive.
func clearAuthCookies(w http.ResponseWriter, r *http.Request) {
	secure := isSecure(r)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieAccessToken,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     cookieRefreshToken,
		Value:    "",
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// SetupStatus handles GET /api/v1/setup/status — returns whether setup is required.
func (h *AuthHandler) SetupStatus(w http.ResponseWriter, r *http.Request) {
	count, err := h.svc.UserCount(r.Context())
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, map[string]bool{"setup_required": count == 0})
}
