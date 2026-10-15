package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/auth"
)

// Personal API keys (`osk_…`, migration 00050) authenticate scripts and
// integrations — Home Assistant, Kometa, cron jobs — that can't run the
// interactive sign-in. They are accepted on the same routes as an access
// token, in either header:
//
//	Authorization: Bearer osk_…
//	X-Api-Key: osk_…
//
// and never in a query string: unlike the short-lived, purpose-scoped asset
// and stream tokens, a key is a long-lived general credential, so it must not
// end up in access logs, browser history or a Referer header.
//
// A key carries no policy of its own. Every request re-reads the owner's row
// (one indexed lookup on the primary, the same cost as the epoch check a
// PASETO token pays), so library access, the rating ceiling and the label
// restriction apply exactly as they stand now, a deleted key stops working on
// the very next request on every instance, and a deleted user's keys die with
// the row. There is no cache to make "revoked" lag.
//
// Keys are refused (RefuseAPIKey) on the endpoints that could turn a leaked
// key into a takeover of the account: signing in and refreshing, password and
// 2FA changes, PIN and profile management, signed-in device and API key
// management, device pairing, user administration, server settings, backups.

// APIKeyIdentity is what a key resolves to: the key, and its owner's policy
// as it is now.
type APIKeyIdentity struct {
	KeyID            uuid.UUID
	UserID           uuid.UUID
	Scope            string
	Username         string
	IsAdmin          bool
	MaxContentRating string
	BlockedLabels    []string
	AllowedLabels    []string
	SessionEpoch     int64
}

// ErrAPIKeyNotFound is what an APIKeyResolver returns for a key that doesn't
// exist or has expired.
var ErrAPIKeyNotFound = errors.New("auth: api key not found")

// errAPIKeyReadOnly rejects a non-GET request made with a read_only key.
var errAPIKeyReadOnly = errors.New("auth: read-only api key")

// APIKeyResolver is the key store the middleware needs. cmd/server supplies
// a gen.Queries-backed implementation on the PRIMARY pool.
type APIKeyResolver interface {
	// ResolveAPIKey looks a presented key up by its hash. ErrAPIKeyNotFound
	// for an unknown or expired key.
	ResolveAPIKey(ctx context.Context, keyHash string) (APIKeyIdentity, error)
	// APIKeyState re-checks a credential minted under a key: the owner's
	// current epoch and whether the key still exists. ErrUserNotFound when
	// the user is gone.
	APIKeyState(ctx context.Context, keyID, userID uuid.UUID) (epoch int64, live bool, err error)
	// TouchAPIKey records that the key was used. Called at most once per
	// apiKeyTouchInterval per key by this process.
	TouchAPIKey(ctx context.Context, keyID uuid.UUID) error
}

// apiKeyTouchInterval bounds how often one process writes a key's
// last_used_at. The query repeats the bound, so several instances still
// write each row at most about this often.
const apiKeyTouchInterval = 5 * time.Minute

// presentedAPIKey returns the personal API key the request carries, if any.
// A Bearer value that isn't key-shaped is a PASETO token and wins over an
// X-Api-Key header; an X-Api-Key value is always treated as a key attempt
// (a malformed one then fails as an unknown key).
func presentedAPIKey(r *http.Request) (string, bool) {
	if bearer := r.Header.Get("Authorization"); strings.HasPrefix(bearer, "Bearer ") {
		tok := strings.TrimSpace(strings.TrimPrefix(bearer, "Bearer "))
		if strings.HasPrefix(tok, auth.APIKeyPrefix) {
			return tok, true
		}
		return "", false
	}
	if k := strings.TrimSpace(r.Header.Get("X-Api-Key")); k != "" {
		return k, true
	}
	return "", false
}

// admitAPIKey resolves a presented key into claims for its owner, built from
// the owner's current row. A read_only key is refused on anything but a safe
// method with errAPIKeyReadOnly.
func (a *Authenticator) admitAPIKey(r *http.Request, key string) (*auth.Claims, error) {
	if a.keys == nil || !auth.LooksLikeAPIKey(key) {
		return nil, ErrAPIKeyNotFound
	}
	ctx := r.Context()
	id, err := a.keys.ResolveAPIKey(ctx, auth.HashAPIKey(key))
	if err != nil {
		// Unlike the epoch check this fails CLOSED on a DB error: there is no
		// earlier admission to fall back on, only a hash we couldn't look up.
		return nil, err
	}
	if id.Scope == auth.APIKeyScopeReadOnly && !isReadMethod(r.Method) {
		return nil, errAPIKeyReadOnly
	}
	a.touchAPIKey(id.KeyID)
	now := time.Now()
	return &auth.Claims{
		UserID:           id.UserID,
		Username:         id.Username,
		IsAdmin:          id.IsAdmin,
		MaxContentRating: id.MaxContentRating,
		BlockedLabels:    id.BlockedLabels,
		AllowedLabels:    id.AllowedLabels,
		SessionEpoch:     id.SessionEpoch,
		IssuedAt:         now,
		ExpiresAt:        now,
		APIKeyID:         id.KeyID,
		APIKeyScope:      id.Scope,
	}, nil
}

// apiKeyStillValid is epochValid for claims tied to a key: the stream tokens
// a key-authenticated request minted, and SessionStillValid's periodic
// re-check of a long-lived response. Fails open on a DB error like the epoch
// check (see epochValid's ACCEPTED RISK note); fails closed when the key or
// the user is gone, or the epoch moved.
func (a *Authenticator) apiKeyStillValid(ctx context.Context, claims *auth.Claims) bool {
	if a.keys == nil {
		return false
	}
	epoch, live, err := a.keys.APIKeyState(ctx, claims.APIKeyID, claims.UserID)
	if err != nil {
		return !errors.Is(err, ErrUserNotFound)
	}
	return live && epoch == claims.SessionEpoch
}

// touchAPIKey records the key's use, at most once per apiKeyTouchInterval in
// this process, off the request path.
func (a *Authenticator) touchAPIKey(keyID uuid.UUID) {
	now := time.Now()
	if last, ok := a.touched.Load(keyID); ok && now.Sub(last.(time.Time)) < apiKeyTouchInterval {
		return
	}
	a.touched.Store(keyID, now)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.keys.TouchAPIKey(ctx, keyID)
	}()
}

// isReadMethod is the set a read_only key may use.
func isReadMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// RefuseAPIKey answers 403 API_KEY_NOT_ALLOWED to a request authenticated
// with a personal API key, or one presenting a key on an unauthenticated
// route (sign-in, refresh). Mount it on every credential and account-security
// endpoint so a leaked key can't be parlayed into a password change, a new
// key, a paired device, a sign-in session or another user's account.
func RefuseAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, presented := presentedAPIKey(r)
		if presented || ClaimsFromContext(r.Context()).IsAPIKey() {
			respond.Error(w, r, http.StatusForbidden, "API_KEY_NOT_ALLOWED",
				"API keys can't be used here; sign in to do this")
			return
		}
		next.ServeHTTP(w, r)
	})
}
