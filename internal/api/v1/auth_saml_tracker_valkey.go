package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/crewjam/saml/samlsp"

	"github.com/onscreen/onscreen/internal/valkey"
)

// valkeySAMLRequestTracker is the multi-instance counterpart to
// memorySAMLRequestTracker — same RequestTracker contract, but state
// lives in Valkey so an AuthnRequest issued by one OnScreen instance
// can be validated by an ACS callback that hits a different instance
// behind a load balancer. v2.1 Track A item 2.
//
// The memory tracker remains the default for single-instance dev /
// home installs; cmd/server swaps in this implementation when Valkey
// is configured. Same 10-minute TTL, same RelayState index keying
// (so existing clients see no protocol change), same lookup
// semantics — only the storage backend differs.
//
// Per-key TTL is enforced by Valkey itself (Set with ttl), so the
// tracker doesn't need a sweep goroutine the way the memory version
// does. GetTrackedRequests is the only awkward operation: SCAN
// across the prefix is O(n) in pending requests, but for SAML that's
// bounded by (concurrent SP-init flows × 10 min) — single digits
// in practice.
type valkeySAMLRequestTracker struct {
	v *valkey.Client
}

// NewValkeySAMLRequestTracker wires the tracker to a live Valkey
// client and returns it as the samlsp.RequestTracker interface so
// callers can pass it straight to SAMLHandler.WithRequestTracker.
// Caller owns the client's lifecycle.
func NewValkeySAMLRequestTracker(v *valkey.Client) samlsp.RequestTracker {
	return &valkeySAMLRequestTracker{v: v}
}

const (
	samlTrackerKeyPrefix = "saml:tracker:"
	samlTrackerTTL       = 10 * time.Minute
)

func samlTrackerKey(index string) string {
	return samlTrackerKeyPrefix + index
}

// TrackRequest mints a fresh RelayState index, stores the
// TrackedRequest under it with the standard TTL, and returns the
// index for the middleware to embed in the AuthnRequest's
// RelayState query parameter.
func (t *valkeySAMLRequestTracker) TrackRequest(_ http.ResponseWriter, _ *http.Request, samlRequestID string) (string, error) {
	idx, err := randomIndex()
	if err != nil {
		return "", err
	}
	tr := samlsp.TrackedRequest{Index: idx, SAMLRequestID: samlRequestID, URI: "/"}
	body, err := json.Marshal(tr)
	if err != nil {
		return "", fmt.Errorf("saml: marshal tracked request: %w", err)
	}
	if err := t.v.Set(context.Background(), samlTrackerKey(idx), string(body), samlTrackerTTL); err != nil {
		return "", fmt.Errorf("saml: store tracked request: %w", err)
	}
	return idx, nil
}

// StopTrackingRequest removes the entry — single-use, called by
// samlsp on successful ACS validation. Errors from DEL are ignored
// the same way the memory tracker silently succeeds: the entry will
// expire on its own and the failure mode is a stale row, not a
// security issue.
func (t *valkeySAMLRequestTracker) StopTrackingRequest(_ http.ResponseWriter, _ *http.Request, index string) error {
	return t.v.Del(context.Background(), samlTrackerKey(index))
}

// GetTrackedRequests returns every non-expired pending request.
// crewjam/samlsp uses this on protected handlers when no RelayState
// is present on the inbound request — for our SP-init flow the
// RelayState path is always taken, so this is rarely called. SCAN
// is acceptable here.
//
// Note: samlsp.TrackedRequest.Index has the JSON tag "-" so it
// doesn't survive serialization — we re-derive it from the Valkey
// key (everything after the prefix) on the way out, so callers see
// the same struct shape the memory tracker emits.
func (t *valkeySAMLRequestTracker) GetTrackedRequests(_ *http.Request) []samlsp.TrackedRequest {
	keys, err := t.v.Scan(context.Background(), samlTrackerKeyPrefix+"*")
	if err != nil {
		return nil
	}
	out := make([]samlsp.TrackedRequest, 0, len(keys))
	for _, k := range keys {
		raw, gerr := t.v.Get(context.Background(), k)
		if gerr != nil {
			// Expired between SCAN and GET; skip rather than fail the whole
			// listing. Other transient errors (network blip) similarly
			// should not fault the remaining entries.
			continue
		}
		var tr samlsp.TrackedRequest
		if json.Unmarshal([]byte(raw), &tr) == nil {
			tr.Index = strings.TrimPrefix(k, samlTrackerKeyPrefix)
			out = append(out, tr)
		}
	}
	return out
}

// GetTrackedRequest looks up by RelayState index. Mirrors the memory
// tracker's error semantics: expired/unknown returns "not found"
// without distinguishing the two — the ACS handler maps either to
// "could not be verified" so an attacker can't tell them apart by
// timing or response shape.
func (t *valkeySAMLRequestTracker) GetTrackedRequest(_ *http.Request, index string) (*samlsp.TrackedRequest, error) {
	raw, err := t.v.Get(context.Background(), samlTrackerKey(index))
	if err != nil {
		return nil, errors.New("saml: tracked request not found (expired or unknown RelayState)")
	}
	var tr samlsp.TrackedRequest
	if err := json.Unmarshal([]byte(raw), &tr); err != nil {
		// A non-JSON value at the key shape we own is a programmer
		// error or a key-collision in shared Valkey; surface it
		// rather than returning a misleading "not found".
		return nil, fmt.Errorf("saml: corrupt tracked request payload: %w", err)
	}
	// samlsp.TrackedRequest.Index is `json:"-"` so it never survives
	// the JSON round-trip — re-attach it from the lookup key. The
	// memory tracker exposes Index for parity with crewjam's other
	// tracker implementations; callers (including the ACS handler)
	// rely on it being set.
	tr.Index = index
	return &tr, nil
}
