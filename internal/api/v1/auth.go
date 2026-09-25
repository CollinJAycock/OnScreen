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

// isLocalPeer reports whether the request's client address is on the local host
// or a private/link-local network.
//
// It reads RemoteAddr, which TrustedRealIP rewrites from a trusted proxy's
// X-Forwarded-For / X-Real-IP — but ONLY when the proxy actually sends one of
// those headers. The bundled docker/nginx.conf sample and cloudflared both do,
// so on a supported deployment a public client behind the proxy correctly shows
// its real public IP. The gate's soundness therefore depends on the fronting
// proxy forwarding the client IP: a same-host proxy that forwards WITHOUT
// setting XFF would leave RemoteAddr as its own loopback address and a remote
// client would read as local. That is a proxy misconfiguration, but operators
// exposing setup through a custom proxy should confirm it forwards the client
// IP (or complete setup over the LAN / set ALLOW_PUBLIC_SETUP deliberately).
func isLocalPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return true
	}
	// Tailscale / CGNAT (100.64.0.0/10): a tailnet peer is the operator's own
	// device, and the TV clients already treat this range as local.
	if cgnatRange.Contains(ip) {
		return true
	}
	// A dual-stack LAN hands clients GLOBAL IPv6 addresses, which IsPrivate
	// rejects — a browser on the same segment connecting over IPv6 would be
	// refused setup. Accept a peer inside one of this host's own on-link
	// prefixes: an internet client cannot source from our subnet.
	return onLinkPeer(ip)
}

var cgnatRange = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// onLinkPeer reports whether an IPv6 ip falls inside a /64-or-longer prefix
// assigned to one of this host's interfaces (skipping loopback, whose peers
// IsLoopback covers). IPv4 is deliberately excluded: a VPS's public IPv4
// subnet is routinely shared with other tenants, whereas an IPv6 /64 is a
// single site-owned segment.
func onLinkPeer(ip net.IP) bool {
	if ip.To4() != nil {
		return false
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() != nil {
				continue
			}
			if ones, bits := n.Mask.Size(); bits == 128 && ones >= 64 && n.Contains(ip) {
				return true
			}
		}
	}
	return false
}

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
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	// AssetToken is a 24 h purpose=asset PASETO bound to the user.
	// Cross-origin clients (Tauri, the TV fleet) embed it in `?token=`
	// on the read-only asset routes — artwork, trickplay, external
	// subtitles, SSE — where an Authorization header isn't reachable
	// and the auth cookie doesn't survive cross-origin. Browsers ignore
	// it (same-origin asset requests carry the httpOnly cookie). Empty
	// only on builds that predate the asset-token work; clients fall
	// back to omitting the param (same-origin) in that case.
	AssetToken string    `json:"asset_token,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
	UserID     uuid.UUID `json:"user_id"`
	Username   string    `json:"username"`
	IsAdmin    bool      `json:"is_admin"`
	// TOTPRequired is set when a TOTP-enabled local account clears the
	// password check but still owes a second factor. AccessToken /
	// RefreshToken / AssetToken are empty in that case; the client posts
	// the 6-digit code (or a recovery code) plus LoginChallengeToken to
	// /auth/totp/verify to finish login. Federated logins never set this
	// — they get 2FA at their IdP.
	TOTPRequired        bool   `json:"totp_required,omitempty"`
	LoginChallengeToken string `json:"login_challenge_token,omitempty"`
}

// UserInfo is a public-safe user representation.
type UserInfo struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
	IsAdmin  bool      `json:"is_admin"`
}

// AuthHandler handles auth endpoints.
type AuthHandler struct {
	svc              AuthService
	logger           *slog.Logger
	audit            *audit.Logger
	allowPublicSetup bool
}

// WithSetupPolicy sets whether first-run admin creation may come from a
// non-local client. Default (false) confines the empty-users-table window to
// loopback / private-network peers.
func (h *AuthHandler) WithSetupPolicy(allowPublic bool) *AuthHandler {
	h.allowPublicSetup = allowPublic
	return h
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
	if pair.TOTPRequired {
		// Password verified but 2FA owed — no session exists yet, so no
		// cookies and no success audit. The /auth/totp/verify step logs
		// the login once the second factor lands.
		respond.Success(w, r, pair)
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
	// Browser logout always takes the cookie path — SameSite=Strict on
	// the refresh cookie means it's only attached to first-party POST
	// /auth/logout calls, which is the intended use.
	var refreshToken string
	if c, err := r.Cookie(cookieRefreshToken); err == nil {
		refreshToken = c.Value
	}
	// JSON-body path is for native clients (Tauri / TV / Android) that don't
	// have cookies. It does NOT require a valid access token: sign-out is
	// exactly when a client's access token has most likely expired, and
	// gating on it made those sign-outs silently skip the revoke, leaving the
	// refresh token alive server-side for its full lifetime. Requiring claims
	// never protected anything either — whoever holds a refresh token can
	// already spend it on /auth/refresh, and reuse detection then ends the
	// victim's session family, so revoking it is strictly less power than
	// possessing it.
	if refreshToken == "" {
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.RefreshToken != "" {
			refreshToken = body.RefreshToken
		}
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
	} else if !h.allowPublicSetup && !isLocalPeer(r) {
		// First-run: while the users table is empty this call makes the caller
		// the admin. Confine that to a loopback / private-network client so a
		// fresh install briefly reachable on the internet can't be claimed by a
		// stranger before the operator finishes setup. See isLocalPeer for the
		// proxy-forwarding assumption this rests on.
		if h.logger != nil {
			h.logger.WarnContext(r.Context(), "first-run register refused: non-local client",
				"remote_addr", r.RemoteAddr)
		}
		respond.Error(w, r, http.StatusForbidden, "SETUP_LOCAL_ONLY",
			"initial setup must be completed from the local network; set ALLOW_PUBLIC_SETUP=true to override")
		return
	} else if !h.allowPublicSetup && !setupHostAllowed(r) {
		// A LAN peer is not enough on its own: DNS rebinding lets a web page on
		// any domain reach this box from a LAN browser as same-origin and read
		// the response. See setupHostAllowed.
		if h.logger != nil {
			h.logger.WarnContext(r.Context(), "first-run register refused: non-local Host",
				"host", r.Host)
		}
		respond.Error(w, r, http.StatusForbidden, "SETUP_LOCAL_HOST_ONLY",
			"initial setup must be opened via the server's IP address or a local hostname; set ALLOW_PUBLIC_SETUP=true to override")
		return
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

	if err := ValidatePassword(body.Password); err != nil {
		respond.BadRequest(w, r, err.Error())
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
// Delegates to middleware.IsSecure — there is exactly ONE implementation of
// this decision on purpose. This package used to carry a byte-identical copy,
// and when the middleware's notion of "trusted peer" was corrected (it now
// reads the trust decision TrustedRealIP recorded, rather than re-deriving it
// from an r.RemoteAddr that TrustedRealIP has already overwritten), the copy
// would have silently kept the old, wrong behaviour for the cookie Secure flag
// while HSTS got the right one.
func isSecure(r *http.Request) bool {
	return middleware.IsSecure(r)
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
