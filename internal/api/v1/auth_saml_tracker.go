package v1

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strings"
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

// ── Browser binding (login-CSRF defence) ────────────────────────────────────

// RelayState alone does not bind a SAML sign-in to the browser that started
// it: an attacker can start a flow in their own browser, authenticate at the
// IdP as THEMSELVES, and have a victim's browser POST the resulting assertion
// + RelayState to the ACS — silently signing the victim into the attacker's
// account (whatever the victim then uploads, links or types lands there).
//
// bindingSAMLRequestTracker closes that by pairing each tracked request with
// a short-lived HttpOnly cookie set on the browser that hit /auth/saml. The
// cookie's digest is stored with the tracked request (appended to its stored
// SAMLRequestID, so it rides through both the memory and the Valkey tracker
// unchanged, and works across HA instances), and the ACS lookup refuses a
// request whose browser does not present the matching cookie.
//
// The ACS is a cross-site POST from the IdP, so over HTTPS the cookie is
// SameSite=None; Secure — and a missing cookie there fails closed. Browsers
// refuse SameSite=None without Secure, so on plain HTTP the cookie is Lax and
// usually does NOT survive the cross-site POST; a MISSING cookie is then
// accepted with a warning (the pre-binding behaviour; plain-HTTP SSO is a dev
// setup), while a PRESENT-but-mismatched one is still refused.
type bindingSAMLRequestTracker struct {
	inner  samlsp.RequestTracker
	logger *slog.Logger
}

const (
	samlBindCookieName = "onscreen_saml_bind"
	// Covers GET /api/v1/auth/saml (start) and POST /api/v1/auth/saml/acs.
	samlBindCookiePath = "/api/v1/auth/saml"
	// samlBindSep joins the SAML request ID and the cookie digest in the
	// tracker's stored SAMLRequestID. crewjam request IDs are "id-<hex>", so
	// the separator cannot occur in them.
	samlBindSep = "|bind:"
)

var (
	errSAMLBindMissing  = errors.New("saml: sign-in binding cookie missing on a secure request")
	errSAMLBindMismatch = errors.New("saml: sign-in binding cookie does not match this sign-in")
)

func newBindingSAMLRequestTracker(inner samlsp.RequestTracker, logger *slog.Logger) *bindingSAMLRequestTracker {
	return &bindingSAMLRequestTracker{inner: inner, logger: logger}
}

// TrackRequest tracks the request via the inner tracker with the binding
// digest attached, and sets the binding cookie on the starting browser. A
// browser that already holds a well-formed binding cookie keeps it, so two
// sign-ins started in parallel tabs both stay valid.
func (t *bindingSAMLRequestTracker) TrackRequest(w http.ResponseWriter, r *http.Request, samlRequestID string) (string, error) {
	nonce := ""
	if c, err := r.Cookie(samlBindCookieName); err == nil && validSAMLBindNonce(c.Value) {
		nonce = c.Value
	} else {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		nonce = base64.RawURLEncoding.EncodeToString(b)
	}
	idx, err := t.inner.TrackRequest(w, r, samlRequestID+samlBindSep+samlBindDigest(nonce))
	if err != nil {
		return "", err
	}
	setSAMLBindCookie(w, r, nonce, int(samlTrackerTTL/time.Second))
	return idx, nil
}

// StopTrackingRequest ends the tracked request and clears the binding cookie.
func (t *bindingSAMLRequestTracker) StopTrackingRequest(w http.ResponseWriter, r *http.Request, index string) error {
	err := t.inner.StopTrackingRequest(w, r, index)
	setSAMLBindCookie(w, r, "", -1)
	return err
}

// GetTrackedRequests lists pending requests with the binding digest stripped.
func (t *bindingSAMLRequestTracker) GetTrackedRequests(r *http.Request) []samlsp.TrackedRequest {
	all := t.inner.GetTrackedRequests(r)
	for i := range all {
		all[i].SAMLRequestID, _ = splitSAMLBinding(all[i].SAMLRequestID)
	}
	return all
}

// GetTrackedRequest resolves the RelayState index and enforces the browser
// binding: errSAMLBindMissing / errSAMLBindMismatch when the caller's browser
// is not the one that started this sign-in.
func (t *bindingSAMLRequestTracker) GetTrackedRequest(r *http.Request, index string) (*samlsp.TrackedRequest, error) {
	tr, err := t.inner.GetTrackedRequest(r, index)
	if err != nil || tr == nil {
		return tr, err
	}
	id, digest := splitSAMLBinding(tr.SAMLRequestID)
	if err := checkSAMLBinding(r, digest); err != nil {
		return nil, err
	}
	c, cerr := r.Cookie(samlBindCookieName)
	if (cerr != nil || c.Value == "") && t.logger != nil {
		// Only reachable on a non-TLS request (checkSAMLBinding fails closed
		// on TLS). Accepted for plain-HTTP dev setups; flag it.
		t.logger.WarnContext(r.Context(), "saml acs: no sign-in binding cookie on a plain-HTTP request; "+
			"accepting without browser binding (serve OnScreen over HTTPS to enforce it)")
	}
	out := *tr
	out.SAMLRequestID = id
	return &out, nil
}

// checkSAMLBinding compares the request's binding cookie against the digest
// stored with the tracked request. nil = bound (or the tolerated plain-HTTP
// missing-cookie case).
func checkSAMLBinding(r *http.Request, storedDigest string) error {
	c, err := r.Cookie(samlBindCookieName)
	if err != nil || c.Value == "" {
		if isSecure(r) {
			return errSAMLBindMissing
		}
		return nil
	}
	// A cookie is present: it must match. An entry tracked before binding
	// existed (no stored digest) can't be verified, so it is refused too.
	if storedDigest == "" ||
		subtle.ConstantTimeCompare([]byte(samlBindDigest(c.Value)), []byte(storedDigest)) != 1 {
		return errSAMLBindMismatch
	}
	return nil
}

// splitSAMLBinding separates a stored "<requestID>|bind:<digest>" value.
// Entries without a digest come back with digest "".
func splitSAMLBinding(stored string) (requestID, digest string) {
	if i := strings.LastIndex(stored, samlBindSep); i >= 0 {
		return stored[:i], stored[i+len(samlBindSep):]
	}
	return stored, ""
}

// samlBindDigest stores only a hash of the cookie, so reading the tracker
// store (Valkey) is not enough to forge the cookie.
func samlBindDigest(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func validSAMLBindNonce(v string) bool {
	b, err := base64.RawURLEncoding.DecodeString(v)
	return err == nil && len(b) == 32
}

// setSAMLBindCookie writes (maxAge > 0) or clears (maxAge < 0) the binding
// cookie. Attributes must be identical on both so the browser evicts it.
func setSAMLBindCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	c := &http.Cookie{
		Name:     samlBindCookieName,
		Value:    value,
		Path:     samlBindCookiePath,
		HttpOnly: true,
		MaxAge:   maxAge,
		SameSite: http.SameSiteLaxMode,
	}
	if isSecure(r) {
		// Must reach the ACS on the IdP's cross-site POST.
		c.Secure = true
		c.SameSite = http.SameSiteNoneMode
	}
	http.SetCookie(w, c)
}
