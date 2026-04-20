package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/observability"
)

// Auth is Bearer-token only; we do not issue session cookies. Because every
// authenticated request must carry an explicit `Authorization: Bearer <paseto>`
// header (which browsers do not attach automatically on cross-origin requests),
// cross-site request forgery is not applicable and no CSRF middleware is
// required. If cookie-based auth is ever added, a CSRF token layer must be
// added alongside it.

type claimsKey struct{}

// Authenticator validates Paseto access tokens.
type Authenticator struct {
	tokens *auth.TokenMaker
}

// NewAuthenticator creates an Authenticator.
func NewAuthenticator(tokens *auth.TokenMaker) *Authenticator {
	return &Authenticator{tokens: tokens}
}

// Required rejects unauthenticated requests with 401.
func (a *Authenticator) Required(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := a.extractClaims(r)
		if err != nil || claims == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey{}, claims)
		ctx = observability.ContextWithUserID(ctx, claims.UserID.String())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Optional parses the token if present but does not reject unauthenticated requests.
func (a *Authenticator) Optional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := a.extractClaims(r)
		if claims != nil {
			ctx := context.WithValue(r.Context(), claimsKey{}, claims)
			ctx = observability.ContextWithUserID(ctx, claims.UserID.String())
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// AdminRequired rejects non-admin users with 403.
func (a *Authenticator) AdminRequired(next http.Handler) http.Handler {
	return a.Required(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil || !claims.IsAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// ClaimsFromContext returns the auth claims stored in ctx, or nil if not present.
func ClaimsFromContext(ctx context.Context) *auth.Claims {
	v, _ := ctx.Value(claimsKey{}).(*auth.Claims)
	return v
}

// WithClaims returns a copy of ctx with the given claims attached.
// Used by tests and server-to-server handlers that construct contexts directly.
func WithClaims(ctx context.Context, claims *auth.Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, claims)
}

func (a *Authenticator) extractClaims(r *http.Request) (*auth.Claims, error) {
	// Native API: Authorization: Bearer <paseto>
	if bearer := r.Header.Get("Authorization"); strings.HasPrefix(bearer, "Bearer ") {
		token := strings.TrimPrefix(bearer, "Bearer ")
		return a.tokens.ValidateAccessToken(token)
	}

	// Browser cookie fallback: httpOnly access-token cookie set by auth handlers.
	if c, err := r.Cookie("onscreen_at"); err == nil && c.Value != "" {
		return a.tokens.ValidateAccessToken(c.Value)
	}

	return nil, nil
}

// userIDFromContext retrieves the user ID string from context (set by auth middleware).
func userIDFromContext(ctx context.Context) string {
	return observability.UserIDFromContext(ctx)
}
