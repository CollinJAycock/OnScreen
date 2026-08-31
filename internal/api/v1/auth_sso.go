package v1

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// IssueTokenPairFn is the callback OIDC and LDAP services use to mint a token
// pair without depending on cmd/server. The implementation lives next to the
// auth service so it can call into unexported package state.
type IssueTokenPairFn func(ctx context.Context, user gen.User) (*TokenPair, error)

// ErrSSOAccountConflict is returned when an SSO login matches an existing
// local account that already has a credential (password, different SSO
// provider, etc.). We refuse to auto-link in that case — see auth_oidc.go and
// auth_ldap.go for the rationale (pre-claim attack via shared email/username).
var ErrSSOAccountConflict = errors.New("an existing local account uses this identifier; ask an admin to link the SSO identity")

// sanitizeUsername strips characters that aren't safe for a derived username
// (anything outside [A-Za-z0-9_]). Used by OIDC and LDAP when minting a local
// account name from upstream profile fields.
var sanitizeUsername = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// randomState returns a base64url-encoded 256-bit random string suitable for
// the OAuth state cookie / nonce parameter.
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// isStubUser reports whether a user row carries no authentication credential.
// "Stub" means the row exists (admin pre-created it for SSO linking, or it was
// orphaned by a removed identity) but no one can prove ownership of it via any
// auth method. Email-based auto-linking only happens to stubs; otherwise an
// attacker who controls a matching email at the IdP can hijack a real account.
//
// Why: Without this gate, the OIDC/LDAP "find by email" link path would let an
// attacker register an IdP account with `victim@corp.com` (unverified emails
// are allowed by most providers), sign in once, and silently take over the
// local account that owns that email. Same risk for LDAP when its tree is not
// fully trusted.
// A MANAGED PROFILE is never a stub, however empty its auth columns look.
// CreateManagedProfile inserts no password, no email and no SSO link, so every
// household profile satisfied the "nobody can prove ownership" test and was
// adoptable by a federated login. Two directions fell out of that: any
// authenticated non-admin could create a profile named after a colleague and
// capture their identity when they first signed in via LDAP, and a directory
// user whose sanitised username happened to match an existing profile
// ("guest", "kids", "mom") was logged straight into it — PIN never consulted,
// and handed a refresh token, which is strictly stronger than a legitimate
// PIN-switch. A profile is a household-owned identity, not an admin-created
// placeholder; parent_user_id is what tells them apart.
//
// The PIN is also a real bcrypt-verified authenticator, so its presence alone
// disproves "no one can prove ownership of this row". Both checks are here:
// the parent check is the one that closes the class, the PIN check keeps the
// stated invariant honest for any future caller.
func isStubUser(u gen.User) bool {
	return u.PasswordHash == nil &&
		u.OidcSubject == nil &&
		u.LdapDn == nil &&
		u.SamlSubject == nil &&
		u.GoogleID == nil &&
		u.GithubID == nil &&
		u.DiscordID == nil &&
		u.Pin == nil &&
		!u.ParentUserID.Valid
}
