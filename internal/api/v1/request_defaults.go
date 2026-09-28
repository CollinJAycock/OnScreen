package v1

import (
	"context"

	"github.com/onscreen/onscreen/internal/domain/settings"
)

// RequestDefaultsReader is the slice of settings.Service the account-creation
// paths (local register, invite accept, OIDC/SAML/LDAP provisioning) read to
// seed a new full account's media-request auto-approval toggles. Satisfied by
// *settings.Service. nil = both off, which keeps tests and minimal wirings on
// the pre-feature behaviour.
type RequestDefaultsReader interface {
	Requests(ctx context.Context) settings.RequestsConfig
}

// NewAccountRequestDefaults returns the auto_approve_movies / auto_approve_tv
// values a new FULL account is created with. They are copied into the users
// row at creation, so changing the defaults later never reaches existing
// accounts. Managed household profiles don't call this: they always start with
// both off.
func NewAccountRequestDefaults(ctx context.Context, src RequestDefaultsReader) (movies, tv bool) {
	if src == nil {
		return false, false
	}
	d := src.Requests(ctx)
	return d.DefaultAutoApproveMovies, d.DefaultAutoApproveTV
}
