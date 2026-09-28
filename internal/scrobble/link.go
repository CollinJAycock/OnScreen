package scrobble

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Linking Last.fm or Trakt is two calls from the browser with the user
// approving OnScreen on the service's site in between: start returns a
// "pending" handle, complete (polled) exchanges it for credentials. The
// handle is the service's own short-lived code (a Last.fm request token, a
// Trakt device code) sealed with the server key and bound to the user and
// service, so any API instance can finish the link, nothing is stored until
// it succeeds, and a handle is useless to another account.

// ErrNotConfigured means the operator hasn't entered the service's API
// credentials, so no user can link it.
var ErrNotConfigured = errors.New("scrobble: service not configured")

// ErrAppRejected means the service turned down the operator's API
// credentials (a mistyped Last.fm key or secret, an unknown Trakt client),
// so no user can link until an admin fixes them in Settings.
var ErrAppRejected = errors.New("scrobble: service rejected the server's API credentials")

// ErrInvalidPending means a pending handle didn't open: tampered with, from
// another user, or sealed under a since-rotated server key.
var ErrInvalidPending = errors.New("scrobble: invalid link handle")

var errPendingExpired = errors.New("scrobble: link handle expired")

// LinkStatus is where a link flow stands after a complete call.
type LinkStatus string

const (
	LinkPending  LinkStatus = "pending"   // not approved yet; poll again
	LinkSlowDown LinkStatus = "slow_down" // polling too fast; back off
	LinkLinked   LinkStatus = "linked"
	LinkExpired  LinkStatus = "expired" // start over
	LinkDenied   LinkStatus = "denied"  // the user declined on the service's site
)

// LinkResult is the outcome of a complete call. Username is set once linked.
type LinkResult struct {
	Status   LinkStatus
	Username string
}

const (
	serviceLastFM = "lastfm"
	serviceTrakt  = "trakt"
)

type pendingLink struct {
	Code    string    `json:"c"`
	Expires time.Time `json:"e"`
}

func pendingContext(service string, userID uuid.UUID) string {
	return "scrobble-link:" + service + ":" + userID.String()
}

func (s *Service) sealPending(service string, userID uuid.UUID, code string, ttl time.Duration) (string, error) {
	b, err := json.Marshal(pendingLink{Code: code, Expires: s.now().Add(ttl)})
	if err != nil {
		return "", fmt.Errorf("scrobble: marshal link handle: %w", err)
	}
	sealed, err := s.sealer.EncryptContext(string(b), pendingContext(service, userID))
	if err != nil {
		return "", fmt.Errorf("scrobble: seal link handle: %w", err)
	}
	return sealed, nil
}

// openPending returns the service code inside a handle sealed for this user
// and service, or ErrInvalidPending / errPendingExpired.
func (s *Service) openPending(service string, userID uuid.UUID, sealed string) (string, error) {
	raw, err := s.sealer.DecryptContext(sealed, pendingContext(service, userID))
	if err != nil {
		return "", ErrInvalidPending
	}
	var p pendingLink
	if err := json.Unmarshal([]byte(raw), &p); err != nil || p.Code == "" {
		return "", ErrInvalidPending
	}
	if s.now().After(p.Expires) {
		return "", errPendingExpired
	}
	return p.Code, nil
}
