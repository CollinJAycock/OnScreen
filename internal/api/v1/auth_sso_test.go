package v1

import (
	"context"
	"testing"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// TestIsStubUser_CredentialTypes guards the SSO auto-link gate: a row counts as
// an un-owned stub ONLY when it carries no credential of ANY type. The SAML case
// is the regression: SAML-only accounts (no password, only saml_subject) must
// NOT be treated as stubs, or an OIDC/LDAP login with a matching email could
// silently take them over.
func TestIsStubUser_CredentialTypes(t *testing.T) {
	s := func(v string) *string { return &v }
	cases := []struct {
		name string
		user gen.User
		want bool
	}{
		{"no credentials", gen.User{}, true},
		{"password", gen.User{PasswordHash: s("h")}, false},
		{"oidc", gen.User{OidcSubject: s("sub")}, false},
		{"ldap", gen.User{LdapDn: s("dn")}, false},
		{"saml", gen.User{SamlSubject: s("nameid")}, false}, // regression: was true
		{"google", gen.User{GoogleID: s("g")}, false},
		{"github", gen.User{GithubID: s("gh")}, false},
		{"discord", gen.User{DiscordID: s("d")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStubUser(tc.user); got != tc.want {
				t.Errorf("isStubUser(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

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
