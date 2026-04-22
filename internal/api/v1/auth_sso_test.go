package v1

import (
	"context"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// fakeIssueTokens is a shared test stub for IssueTokenPairFn — used by the
// OIDC and LDAP service tests that don't want to spin up the real token maker.
func fakeIssueTokens(_ context.Context, user gen.User) (*TokenPair, error) {
	return &TokenPair{
		AccessToken:  "at-" + user.Username,
		RefreshToken: "rt-" + user.Username,
		UserID:       user.ID,
		Username:     user.Username,
	}, nil
}
