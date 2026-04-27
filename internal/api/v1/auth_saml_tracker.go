package v1

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/crewjam/saml/samlsp"
)

// memorySAMLRequestTracker is a server-side replacement for crewjam/saml's
// CookieRequestTracker.
//
// The default cookie tracker fails on local cross-port HTTP testing
// (localhost:8080 IdP → localhost:7070 SP) because the SP's `saml_<state>`
// cookie defaults to SameSite=Lax — and Chromium drops Lax cookies on
// cross-site top-level POSTs, which is exactly the binding the SAML IdP
// uses for the response. Without the cookie, the ACS handler can't find
// the original request ID and rejects the assertion with `InResponseTo
// does not match any of the possible request IDs (expected [])`.
//
// Production over HTTPS doesn't hit this — SameSite=None+Secure cookies
// work — but local dev on plain HTTP does, and locking the only manual
// test path behind "stand up nginx in front of two services" was a worse
// product story than just storing requests on the server.
//
// Lookup is keyed by RelayState, which the IdP echoes back unmodified in
// the SAML response (and which is independent of cookies, headers, or
// any browser policy). Single-instance only — multi-instance OnScreen
// would need a shared store (Valkey/Postgres). Documented as a v2.1
// candidate; in practice SAML deployments are rarely multi-instance,
// and the shared-store version is a drop-in replacement.
type memorySAMLRequestTracker struct {
	mu      sync.Mutex
	pending map[string]memorySAMLEntry
	ttl     time.Duration
}

type memorySAMLEntry struct {
	req       samlsp.TrackedRequest
	expiresAt time.Time
}

// newMemorySAMLRequestTracker returns a tracker with a 10 minute TTL. The
// SAML AuthnRequest itself has 1 minute clock skew tolerance, but a user
// at an IdP password prompt may legitimately take a few minutes — 10 is
// a comfortable upper bound that still bounds memory growth.
func newMemorySAMLRequestTracker() *memorySAMLRequestTracker {
	return &memorySAMLRequestTracker{
		pending: make(map[string]memorySAMLEntry),
		ttl:     10 * time.Minute,
	}
}

// TrackRequest stores the request ID and returns a freshly-generated
// RelayState index. The crewjam/saml middleware writes the index back as
// the `RelayState` form parameter when redirecting to the IdP.
func (t *memorySAMLRequestTracker) TrackRequest(_ http.ResponseWriter, _ *http.Request, samlRequestID string) (string, error) {
	idx, err := randomIndex()
	if err != nil {
		return "", err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gcLocked()
	t.pending[idx] = memorySAMLEntry{
		req:       samlsp.TrackedRequest{Index: idx, SAMLRequestID: samlRequestID, URI: "/"},
		expiresAt: time.Now().Add(t.ttl),
	}
	return idx, nil
}

// StopTrackingRequest removes the entry — single-use, called by samlsp on
// successful ACS validation.
func (t *memorySAMLRequestTracker) StopTrackingRequest(_ http.ResponseWriter, _ *http.Request, index string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pending, index)
	return nil
}

// GetTrackedRequests returns every non-expired pending request. crewjam
// uses this on protected handlers when no RelayState is present on the
// inbound — for our SP-init flow the RelayState path is always taken,
// but the interface requires it.
func (t *memorySAMLRequestTracker) GetTrackedRequests(_ *http.Request) []samlsp.TrackedRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gcLocked()
	out := make([]samlsp.TrackedRequest, 0, len(t.pending))
	for _, e := range t.pending {
		out = append(out, e.req)
	}
	return out
}

// GetTrackedRequest looks up by RelayState index. ErrNotFound when expired
// or never tracked — the ACS handler then surfaces the standard
// "could not be verified" error to the caller.
func (t *memorySAMLRequestTracker) GetTrackedRequest(_ *http.Request, index string) (*samlsp.TrackedRequest, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gcLocked()
	e, ok := t.pending[index]
	if !ok {
		return nil, errors.New("saml: tracked request not found (expired or unknown RelayState)")
	}
	r := e.req
	return &r, nil
}

// gcLocked must be called with the mutex held. Sweeps expired entries.
// O(n) per call but n is bounded by the TTL × concurrent SAML inits.
func (t *memorySAMLRequestTracker) gcLocked() {
	now := time.Now()
	for k, e := range t.pending {
		if now.After(e.expiresAt) {
			delete(t.pending, k)
		}
	}
}

// randomIndex returns a 32-byte URL-safe random string used as the
// RelayState. Long enough that an attacker can't guess a valid index
// even with millions of attempts.
func randomIndex() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
