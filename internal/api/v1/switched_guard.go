package v1

import (
	"net/http"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
)

// blockSwitchedSession refuses an action when the caller holds a PIN-switched
// credential, writing a 403 and reporting true.
//
// /auth/pin-switch deliberately mints the weakest token the system issues: it
// is derived from a 4-digit PIN, carries Switched=true, and gets no refresh
// token, because a household member typing a PIN at the TV is not the same
// assurance as an account login. Anything that can permanently destroy the
// target account, or seize control of how it authenticates, therefore has to
// require a real login — otherwise the PIN becomes a full credential for
// everything except the two places that happen to ask for a password.
//
// PINSwitch already refuses to chain off an already-switched token. The
// endpoints below were the gap — account deletion and 2FA enrolment (and,
// since v2.6, a password change) were reachable with nothing but the PIN.
func blockSwitchedSession(w http.ResponseWriter, r *http.Request, action string) bool {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.Switched {
		return false
	}
	respond.Error(w, r, http.StatusForbidden, "PIN_SESSION_FORBIDDEN",
		"sign in with your account password to "+action)
	return true
}
