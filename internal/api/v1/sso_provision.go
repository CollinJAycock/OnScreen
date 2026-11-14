package v1

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// ssoProvisionDB is what the shared OIDC / SAML sign-in flow needs beyond the
// protocol's own identity lookups. OIDCOAuthDB and SAMLOAuthDB both satisfy it.
type ssoProvisionDB interface {
	ssoAdminSyncDB
	GetUserByEmail(ctx context.Context, email *string) (gen.User, error)
	CountUsers(ctx context.Context) (int64, error)
	GrantAutoLibrariesToUser(ctx context.Context, userID uuid.UUID) error
}

// ssoLogin is one federated sign-in for loginOrCreateSSOUser: what the identity
// provider asserted, and the protocol's three identity operations.
type ssoLogin struct {
	// protocol is "oidc" or "saml": the prefix of errors and log lines, and
	// syncAdminFromIdP's label.
	protocol  string
	email     string // the IdP's (verified) email; "" = none
	isAdmin   bool   // the IdP says the user is in the admin group
	groupSync bool   // admin follows the IdP's groups (see syncAdminFromIdP)

	// findLinked returns the account already linked to this identity
	// (issuer + subject), pgx.ErrNoRows when none is.
	findLinked func(ctx context.Context) (gen.User, error)
	// link attaches the identity to the stub account userID.
	link func(ctx context.Context, userID uuid.UUID, email *string) error
	// create provisions a new account for the identity.
	create func(ctx context.Context, isAdmin bool, email *string, autoMovies, autoTV bool) (gen.User, error)
}

// loginOrCreateSSOUser is the sign-in both OIDC and SAML complete with, in
// three steps:
//
//  1. an account already linked to the identity signs in (its admin flag
//     reconciled with the IdP's groups when group sync is on);
//  2. otherwise an email match links the identity, but only into a stub row
//     (isStubUser: no password, no other SSO link, not a managed profile) —
//     a credentialed account with that email is ErrSSOAccountConflict, since
//     trusting the match would let an IdP user take it over;
//  3. otherwise a new account is created. The first account on the server is
//     an admin (parity with local registration); a new non-admin gets the
//     libraries flagged for new users.
//
// The two protocols ran byte-for-byte copies of this; one function means a
// fix to one can't miss the other.
func loginOrCreateSSOUser(ctx context.Context, db ssoProvisionDB, logger *slog.Logger,
	issueTokens IssueTokenPairFn, reqDefaults RequestDefaultsReader, l ssoLogin) (*TokenPair, error) {
	// 1. Already linked.
	user, err := l.findLinked(ctx)
	if err == nil {
		user = syncAdminFromIdP(ctx, db, logger, user, l.isAdmin, l.groupSync, l.protocol)
		return issueTokens(ctx, user)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get user by %s: %w", l.protocol, err)
	}

	// 2. Email-based linking — only when present AND only into a stub row.
	if l.email != "" {
		emailPtr := l.email
		user, err = db.GetUserByEmail(ctx, &emailPtr)
		if err == nil {
			if !isStubUser(user) {
				return nil, ErrSSOAccountConflict
			}
			if linkErr := l.link(ctx, user.ID, &emailPtr); linkErr != nil {
				logger.WarnContext(ctx, l.protocol+": link existing account", "user_id", user.ID, "err", linkErr)
			}
			user = syncAdminFromIdP(ctx, db, logger, user, l.isAdmin, l.groupSync, l.protocol)
			return issueTokens(ctx, user)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get user by email: %w", err)
		}
	}

	// 3. Create the account. First-ever user is admin.
	count, err := db.CountUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	isAdmin := l.isAdmin || count == 0
	var emailPtr *string
	if l.email != "" {
		e := l.email
		emailPtr = &e
	}
	autoMovies, autoTV := NewAccountRequestDefaults(ctx, reqDefaults)
	user, err = l.create(ctx, isAdmin, emailPtr, autoMovies, autoTV)
	if err != nil {
		return nil, fmt.Errorf("create %s user: %w", l.protocol, err)
	}
	if !isAdmin {
		// Non-fatal: a freshly-provisioned non-admin should default into the
		// admin-chosen library set so first sign-in isn't a barren home page.
		if err := db.GrantAutoLibrariesToUser(ctx, user.ID); err != nil {
			logger.WarnContext(ctx, l.protocol+": auto-grant libraries", "user_id", user.ID, "err", err)
		}
	}
	return issueTokens(ctx, user)
}
