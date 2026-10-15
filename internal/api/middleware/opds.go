package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/observability"
)

// OPDS catalogs (/opds/…) are read by e-reader apps — KOReader, Moon+ Reader,
// Panels, Chunky — that can authenticate only with HTTP Basic or by putting a
// credential in the URL. OPDSRequired adds HTTP Basic to the usual carriers,
// with one rule: the password must be a personal API key (`osk_…`). The
// username is ignored. The account password is never accepted over Basic:
// that would be a password-guessing surface with no second factor, on a
// route a reader app polls unattended. A key admitted this way acts exactly
// as it does in a header (the owner's current library access, rating ceiling
// and label restriction; revocation on the next request), and every OPDS
// route is a GET, so a read_only key is enough.
//
// Basic is read nowhere else: on every other route an Authorization: Basic
// header is not a credential at all, so the RefuseAPIKey routes can't be
// reached with a key this way either.

// FailureCounter is the failed-attempt throttle OPDSRequired uses. Satisfied
// by *valkey.RateLimiter.
type FailureCounter interface {
	Failures(ctx context.Context, key string) (int, error)
	IncrFailure(ctx context.Context, key string, window time.Duration)
}

const (
	// opdsMaxFailures failed Basic attempts from one address within
	// opdsFailureWindow (counted from the last failure) lock that address
	// out of OPDS Basic sign-in until the window passes.
	opdsMaxFailures   = 10
	opdsFailureWindow = 15 * time.Minute
	opdsRealm         = `Basic realm="OnScreen OPDS", charset="UTF-8"`
)

// OPDSRequired authenticates an OPDS request: HTTP Basic with an API key as
// the password, or anything Required accepts (Bearer, X-Api-Key, the session
// cookie). allowQuery also accepts the purpose-scoped ?token= carrier — used
// only on the cover route, whose {fileId} binds a stream token to one book
// file, so a reader that drops credentials on image links still gets covers.
//
// Failed Basic attempts are counted per client address (failures may be nil
// in tests); past the cap the address gets 429 before its key is even looked
// up. Successful requests are not counted, so a reader fetching a page of
// covers at once is never throttled by this.
func (a *Authenticator) OPDSRequired(failures FailureCounter, allowQuery bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var claims *auth.Claims
			if _, pass, ok := r.BasicAuth(); ok {
				failKey := fmt.Sprintf("opdsauth:%s", clientIP(r))
				if failures != nil {
					if n, _ := failures.Failures(r.Context(), failKey); n >= opdsMaxFailures {
						w.Header().Set("Retry-After", fmt.Sprintf("%d", int(opdsFailureWindow.Seconds())))
						writeRateLimitError(w, http.StatusTooManyRequests,
							"RATE_LIMITED", "too many failed sign-in attempts")
						return
					}
				}
				c, err := a.admitAPIKey(r, pass)
				if err != nil || c == nil {
					if failures != nil {
						failures.IncrFailure(r.Context(), failKey, opdsFailureWindow)
					}
					opdsChallenge(w, r)
					return
				}
				claims = c
			} else {
				c, err := a.admit(r, allowQuery)
				if err != nil || c == nil {
					opdsChallenge(w, r)
					return
				}
				claims = c
			}
			ctx := context.WithValue(r.Context(), claimsKey{}, claims)
			ctx = observability.ContextWithUserID(ctx, claims.UserID.String())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// opdsChallenge answers 401 with a Basic challenge, which is what makes a
// reader app prompt for (or retry with) its stored credentials.
func opdsChallenge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", opdsRealm)
	writeAdmitError(w, r, nil)
}
