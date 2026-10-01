package v1

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ssoAdminSyncDB is the slice of queries syncAdminFromIdP needs. Each of the
// three SSO services (OIDC, SAML, LDAP) embeds these in its own DB interface.
type ssoAdminSyncDB interface {
	SetUserAdmin(ctx context.Context, arg gen.SetUserAdminParams) error
	BumpSessionEpoch(ctx context.Context, id uuid.UUID) error
	DeleteSessionsForUser(ctx context.Context, userID uuid.UUID) error
	GetUser(ctx context.Context, id uuid.UUID) (gen.User, error)
}

// ssoAuditKey is the context key withSSOAudit stores an ssoAudit under.
type ssoAuditKey struct{}

// ssoAudit is what syncAdminFromIdP needs to audit the session wipe: the
// sign-in handler's audit logger and the request's client IP.
type ssoAudit struct {
	log *audit.Logger
	ip  string
}

// withSSOAudit returns ctx carrying the sign-in handler's audit logger and the
// request's client IP for syncAdminFromIdP, or ctx unchanged when a is nil.
//
// Why the context: the sync runs inside the OIDC / SAML / LDAP services, which
// see neither the request nor the audit logger — both live on the handlers.
// Threading them through three service interfaces (and every fake of those)
// for one event was far more churn than a request-scoped value. It also puts
// the entry AT the wipe, so it is written even if the sign-in then fails to
// mint tokens: the user's other devices were signed out either way.
func withSSOAudit(ctx context.Context, a *audit.Logger, r *http.Request) context.Context {
	if a == nil {
		return ctx
	}
	return context.WithValue(ctx, ssoAuditKey{}, ssoAudit{log: a, ip: audit.ClientIP(r)})
}

// syncAdminFromIdP reconciles a user's is_admin flag with what the identity
// provider just asserted, and returns the user the caller should mint tokens
// from.
//
// Returning the user is the point. All three SSO services previously called
// SetUserAdmin and then issued tokens from the SAME struct they had read
// BEFORE the write — and issueTokenPair takes IsAdmin straight off that struct.
// So an IdP demotion wrote is_admin=false to the database and then handed the
// demoted user a token asserting IsAdmin=true, which AdminRequired honours with
// no DB read for the full access-token TTL. The login that was supposed to
// remove their admin rights was the thing that renewed them.
//
// It also revokes outstanding credentials on a change, which is the invariant
// the admin UI already enforces at PUT /users/{id}/admin: "a demoted admin
// can't keep using their session for up to an hour while the access token rides
// out its TTL". A demotion driven by the IdP is the same event and deserves the
// same treatment — otherwise every session the user opened before this login
// keeps full admin until it expires on its own.
//
// Ordering matters: bump the epoch BEFORE re-reading, so the fresh user carries
// the new epoch and the token we are about to mint validates, while every token
// minted under the old epoch is refused.
//
// The wipe signs the user out on every device, so it is also recorded as
// auth.sessions_revoked when the handler supplied an audit logger (see
// withSSOAudit) — otherwise an operator only had a server-log line to explain
// why every session suddenly ended.
//
// Failures are logged, not fatal, and fall back to the pre-sync user. A sign-in
// that succeeds against the IdP should not be turned into a hard error by a
// bookkeeping write — but note the fallback is the SAFE direction only for
// promotion. On a failed demotion the caller mints from a struct that still
// says IsAdmin=true; the DB write is what actually failed, so there is nothing
// better to fall back to, and the warning is the operator's signal.
func syncAdminFromIdP(
	ctx context.Context, db ssoAdminSyncDB, logger *slog.Logger,
	user gen.User, wantAdmin, groupSync bool, provider string,
) gen.User {
	if !groupSync || user.IsAdmin == wantAdmin {
		return user
	}
	if err := db.SetUserAdmin(ctx, gen.SetUserAdminParams{ID: user.ID, IsAdmin: wantAdmin}); err != nil {
		logger.Warn(provider+": sync admin", "user_id", user.ID, "err", err)
		return user
	}
	// Revoke credentials issued under the old role. PASETO access/asset tokens
	// die via session_epoch; refresh sessions via the sessions table.
	berr := db.BumpSessionEpoch(ctx, user.ID)
	if berr != nil {
		logger.Warn(provider+": bump session epoch after admin sync", "user_id", user.ID, "err", berr)
	}
	derr := db.DeleteSessionsForUser(ctx, user.ID)
	if derr != nil {
		logger.Warn(provider+": delete sessions after admin sync", "user_id", user.ID, "err", derr)
	}
	if a, ok := ctx.Value(ssoAuditKey{}).(ssoAudit); ok {
		// sessions_deleted / epoch_bumped: whether each half of the wipe
		// landed, so a failed one is not recorded as a revocation.
		uid := user.ID
		a.log.Log(ctx, &uid, audit.ActionSessionsRevoked, uid.String(), map[string]any{
			"reason":           "idp_admin_sync",
			"provider":         provider,
			"is_admin":         wantAdmin,
			"sessions_deleted": derr == nil,
			"epoch_bumped":     berr == nil,
		}, a.ip)
	}
	fresh, err := db.GetUser(ctx, user.ID)
	if err != nil {
		// Re-read failed: fall back to patching the fields we know changed, so
		// at minimum the token does not assert an admin role the DB just
		// revoked. The epoch may be stale, which costs this login a refresh,
		// not a privilege.
		logger.Warn(provider+": re-read user after admin sync", "user_id", user.ID, "err", err)
		user.IsAdmin = wantAdmin
		return user
	}
	logger.Info(provider+": admin role synced from identity provider",
		"user_id", user.ID, "is_admin", wantAdmin)
	return fresh
}
