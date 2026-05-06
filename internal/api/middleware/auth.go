package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/observability"
)

// ErrUserNotFound is the sentinel a SessionEpochReader must return when the
// user row is gone. The middleware treats this as authoritative revocation
// (fail closed); other errors fall back to fail-open so a transient DB blip
// doesn't log everybody out.
var ErrUserNotFound = errors.New("auth: user not found")

// Auth accepts two credential carriers:
//
//   1. `Authorization: Bearer <paseto>` — used by native API clients. Browsers
//      never attach this automatically on cross-origin requests, so it carries
//      no CSRF surface.
//   2. The httpOnly `onscreen_at` cookie set by setAuthCookies in the v1
//      package. This *does* carry CSRF surface, mitigated by SameSite=Lax on
//      the cookie itself (cross-origin POST/PATCH/PUT/DELETE never include the
//      cookie). The refresh cookie uses SameSite=Strict and is scoped to
//      /api/v1/auth, so it is not attached to non-auth endpoints at all.
//
// Two invariants protect the cookie path: every state-changing route must use
// a non-GET method (chi routers enforce per-method handlers), and no top-level
// GET endpoint may have side effects. Audit both before adding new routes — if
// either invariant breaks, a double-submit CSRF token layer becomes required.

type claimsKey struct{}

// SessionEpochReader looks up the current session_epoch for a user.
// Token epochs that don't match the DB row's epoch are rejected — this
// is what makes a stateless PASETO token revocable in seconds after
// admin demotion, delete, or force-logout.
//
// Interface is kept minimal so the auth package has no direct DB
// dependency. cmd/server supplies a gen.Queries-backed impl.
type SessionEpochReader interface {
	GetSessionEpoch(ctx context.Context, userID uuid.UUID) (int64, error)
}

// Authenticator validates Paseto access tokens.
type Authenticator struct {
	tokens *auth.TokenMaker
	epochs SessionEpochReader // optional; when nil the middleware skips the epoch check
}

// NewAuthenticator creates an Authenticator.
func NewAuthenticator(tokens *auth.TokenMaker) *Authenticator {
	return &Authenticator{tokens: tokens}
}

// WithEpochReader attaches the session-epoch lookup. Without it, PASETO
// tokens remain valid until TTL expiry (the pre-hardening behavior).
// Production wiring supplies one; tests that don't care about
// revocation can omit.
func (a *Authenticator) WithEpochReader(r SessionEpochReader) *Authenticator {
	a.epochs = r
	return a
}

// RequiredAllowQueryToken is `Required` plus a `?token=` query
// fallback. Designed for asset endpoints — `<img src=…>`, `<audio
// src=…>`, `<a download href=…>` — that can't carry an
// Authorization header and where cookies don't survive cross-
// origin (Tauri webview → remote OnScreen). DO NOT use on
// regular API routes: putting a long-lived bearer in a URL means
// it appears in server logs, browser history, and Referer
// headers, broadening the leak surface. Asset URLs are the
// trade-off — they're already private-cached and don't trigger
// external navigation.
//
// The query fallback only fires when both the Bearer header AND
// the cookie are absent, so browser builds (cookie auth) keep
// the existing path with no URL pollution.
func (a *Authenticator) RequiredAllowQueryToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := a.extractClaimsAllowQuery(r)
		if err != nil || claims == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !a.epochValid(r.Context(), claims) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey{}, claims)
		ctx = observability.ContextWithUserID(ctx, claims.UserID.String())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractClaimsAllowQuery layers a `?token=<paseto>` lookup on top
// of the standard Bearer/cookie extraction. Used only by
// RequiredAllowQueryToken — broadcasting it via plain
// extractClaims would let a leaked artwork URL grant general
// API access, which is exactly the trade-off this variant exists
// to scope.
//
// Stream-purpose tokens are also enforced to match the request's
// {id} URL param against the token's FileID claim. The route
// pattern uses chi {id} for /media/stream/{id} and {fileId} for
// /media/subtitles/{fileId}/{streamIndex}; either match counts
// since both name the same file UUID. Tokens without purpose=stream
// (the standard 1 h access token reused as a `?token=` carrier on
// older clients) skip the file-id check — those still behave as
// before.
func (a *Authenticator) extractClaimsAllowQuery(r *http.Request) (*auth.Claims, error) {
	if claims, err := a.extractClaims(r); err != nil || claims != nil {
		return claims, err
	}
	tok := r.URL.Query().Get("token")
	if tok == "" {
		return nil, nil
	}
	claims, err := a.tokens.ValidateAccessToken(tok)
	if err != nil || claims == nil {
		return claims, err
	}
	if claims.Purpose == "stream" {
		// Bind enforcement: the token's file_id must match the
		// {id} / {fileId} path param. If neither chi param is
		// present (the artwork / trickplay routes), the token is
		// out of scope here — stream tokens shouldn't be honoured
		// on non-stream asset routes either.
		want := chi.URLParam(r, "id")
		if want == "" {
			want = chi.URLParam(r, "fileId")
		}
		if want == "" {
			return nil, fmt.Errorf("validate token: stream token presented on non-file-scoped route")
		}
		if claims.FileID == nil || claims.FileID.String() != want {
			return nil, fmt.Errorf("validate token: stream token file_id mismatch")
		}
	}
	return claims, nil
}

// Required rejects unauthenticated requests with 401. When an epoch
// reader is configured, tokens whose session_epoch doesn't match the
// DB's current value are also rejected — this is how admin demotion
// or user delete take effect before the token's 1h TTL.
func (a *Authenticator) Required(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := a.extractClaims(r)
		if err != nil || claims == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !a.epochValid(r.Context(), claims) {
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
		if claims != nil && a.epochValid(r.Context(), claims) {
			ctx := context.WithValue(r.Context(), claimsKey{}, claims)
			ctx = observability.ContextWithUserID(ctx, claims.UserID.String())
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// epochValid compares the token's session_epoch against the DB's
// current value. Missing reader → skip (legacy test setup); DB error
// → fail-open (we don't want a DB blip to log everybody out) but log
// as a concern; mismatch → fail-closed.
//
// Zero-epoch tokens minted before the field existed match any row —
// they age out within 1h via TTL, and after a single demote/delete
// the DB row's epoch diverges so they're rejected anyway.
func (a *Authenticator) epochValid(ctx context.Context, claims *auth.Claims) bool {
	if a.epochs == nil {
		return true
	}
	current, err := a.epochs.GetSessionEpoch(ctx, claims.UserID)
	if err != nil {
		// Fail closed when the user row is gone — a deleted user's PASETO
		// token must stop working immediately, not ride out the 1h TTL.
		if errors.Is(err, ErrUserNotFound) {
			return false
		}
		// Fail open on other errors: a DB hiccup shouldn't log everybody
		// out. A real revocation (epoch bump) requires the DB write to
		// succeed anyway, so this failure mode doesn't compromise the
		// security property.
		return true
	}
	if claims.SessionEpoch == 0 {
		// Token predates the session_epoch field. Accept but treat the
		// next demote/delete as authoritative.
		return current == 0
	}
	return claims.SessionEpoch == current
}

// ensure uuid package stays imported even when only used inside the
// interface declaration above.
var _ uuid.UUID

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

// ImpersonationLookup loads the fields the view-as middleware needs to
// synthesize a target user's claims. Returns ErrUserNotFound when the
// row is gone — the middleware maps that to 404 so admins can't probe
// for live user IDs.
type ImpersonationLookup interface {
	GetUserForImpersonation(ctx context.Context, userID uuid.UUID) (ImpersonatedUser, error)
}

// ImpersonatedUser carries the subset of users.* the view-as
// middleware substitutes into the request claims.
type ImpersonatedUser struct {
	ID               uuid.UUID
	Username         string
	IsAdmin          bool
	MaxContentRating string
}

// ViewAs returns a middleware that, when an admin appends
// `?view_as=<uuid>` to a GET request, swaps the auth claims for the
// target user's so every downstream handler executes the same DB
// queries with the same content-rating ceiling and library access
// the target would see. Verifies the policy stack end-to-end without
// having to PIN-switch.
//
// Read-only by design: any non-GET request that carries the param
// returns 403, so an admin can't accidentally write while
// impersonating (and clients that auto-append the param to every
// fetch don't break write paths). The original claims are *not*
// preserved on the context — handlers see exactly what the target
// would see, including admin handlers refusing because the target
// lacks the role.
//
// When the param is absent the middleware is a pass-through; safe to
// mount unconditionally on any router that already runs Required.
//
// auditor may be nil; when set, every successful impersonation emits
// a single audit.ActionImpersonateBegin record with the *real* admin's
// UserID as actor and the target user's ID in the target column. The
// downstream request log line otherwise attributes the GETs to the
// target user (since the synthetic claims carry their identity), so
// without this trail an admin can browse another user's home page,
// history, libraries, etc. with no forensic record.
func (a *Authenticator) ViewAs(lookup ImpersonationLookup, auditor ViewAsAuditor) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.URL.Query().Get("view_as")
			if raw == "" {
				next.ServeHTTP(w, r)
				return
			}
			// Block on non-GET so a client that adds the param to every
			// request can't accidentally mutate state as the target.
			if r.Method != http.MethodGet {
				http.Error(w, "view_as is only supported on GET requests", http.StatusForbidden)
				return
			}
			caller := ClaimsFromContext(r.Context())
			if caller == nil || !caller.IsAdmin {
				http.Error(w, "view_as requires admin", http.StatusForbidden)
				return
			}
			targetID, err := uuid.Parse(raw)
			if err != nil {
				http.Error(w, "view_as: invalid uuid", http.StatusBadRequest)
				return
			}
			target, err := lookup.GetUserForImpersonation(r.Context(), targetID)
			if err != nil {
				if errors.Is(err, ErrUserNotFound) {
					http.NotFound(w, r)
					return
				}
				http.Error(w, "view_as: lookup failed", http.StatusInternalServerError)
				return
			}
			// Stamp the audit trail before context swap so the actor is
			// the *real* admin, not the synthetic target. The auditor
			// gets the raw request and can pull whatever client-IP /
			// header context it needs (the audit package already has
			// ClientIP; importing it here would couple middleware to
			// audit's deps).
			if auditor != nil {
				adminID := caller.UserID
				auditor.LogImpersonate(r, &adminID, target.ID)
			}
			synth := *caller // copy so we don't mutate the cached claims
			synth.UserID = target.ID
			synth.Username = target.Username
			synth.IsAdmin = target.IsAdmin
			synth.MaxContentRating = target.MaxContentRating
			next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), &synth)))
		})
	}
}

// ViewAsAuditor is the narrow audit-write interface ViewAs depends on.
// Defined here (instead of importing internal/audit) so the middleware
// package stays free of the audit package's transitive deps. The
// adapter on the cmd/server side wraps audit.Logger.Log and pulls
// client IP via audit.ClientIP from the raw request.
type ViewAsAuditor interface {
	LogImpersonate(r *http.Request, adminID *uuid.UUID, targetID uuid.UUID)
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
		return a.validateGeneralPurposeToken(token)
	}

	// Browser cookie fallback: httpOnly access-token cookie set by auth handlers.
	if c, err := r.Cookie("onscreen_at"); err == nil && c.Value != "" {
		return a.validateGeneralPurposeToken(c.Value)
	}

	return nil, nil
}

// validateGeneralPurposeToken decrypts a token and rejects narrowly-
// scoped variants (purpose=stream) so they can't be presented as a
// Bearer to a non-asset API route. Stream tokens flow only through
// extractClaimsAllowQuery's `?token=` path on /media/stream and
// /media/subtitles, which additionally enforces the file_id binding.
func (a *Authenticator) validateGeneralPurposeToken(token string) (*auth.Claims, error) {
	claims, err := a.tokens.ValidateAccessToken(token)
	if err != nil {
		return nil, err
	}
	if claims != nil && claims.Purpose != "" {
		// Any token with a non-empty purpose is scoped to a specific
		// asset route (today: stream). Reject on the general path so
		// a leaked stream URL can't grant settings / item-mutation
		// access via Bearer header.
		return nil, fmt.Errorf("validate token: purpose=%q not accepted on general route", claims.Purpose)
	}
	return claims, nil
}

// userIDFromContext retrieves the user ID string from context (set by auth middleware).
func userIDFromContext(ctx context.Context) string {
	return observability.UserIDFromContext(ctx)
}
