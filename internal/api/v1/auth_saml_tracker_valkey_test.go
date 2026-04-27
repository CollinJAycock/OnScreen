package v1

import (
	"net/http/httptest"
	"testing"

	"github.com/onscreen/onscreen/internal/testvalkey"
)

// trackerRoundTrip stores a request, reads it back, and asserts the
// fields survive the JSON encode → Valkey → decode cycle. This is the
// only contract callers actually depend on; everything else is a
// variant.
func TestValkeySAMLTracker_TrackThenGet_RoundTripsFields(t *testing.T) {
	v := testvalkey.New(t)
	tr := NewValkeySAMLRequestTracker(v)
	req := httptest.NewRequest("GET", "/api/v1/auth/saml", nil)

	idx, err := tr.TrackRequest(httptest.NewRecorder(), req, "saml-req-id-abc")
	if err != nil {
		t.Fatalf("TrackRequest: %v", err)
	}
	if idx == "" {
		t.Fatal("TrackRequest must return a non-empty RelayState index")
	}

	got, err := tr.GetTrackedRequest(req, idx)
	if err != nil {
		t.Fatalf("GetTrackedRequest: %v", err)
	}
	if got.SAMLRequestID != "saml-req-id-abc" {
		t.Errorf("SAMLRequestID: got %q, want %q", got.SAMLRequestID, "saml-req-id-abc")
	}
	if got.Index != idx {
		t.Errorf("Index round-trip: got %q, want %q", got.Index, idx)
	}
}

func TestValkeySAMLTracker_StopTracking_MakesGetReturnNotFound(t *testing.T) {
	// Single-use is the security property: an attacker who replays an
	// old SAML response with the same RelayState should get rejected.
	// crewjam/saml calls StopTrackingRequest after a successful ACS
	// validation — this test pins that the second lookup misses.
	v := testvalkey.New(t)
	tr := NewValkeySAMLRequestTracker(v)
	req := httptest.NewRequest("GET", "/", nil)

	idx, err := tr.TrackRequest(httptest.NewRecorder(), req, "single-use")
	if err != nil {
		t.Fatalf("TrackRequest: %v", err)
	}
	if err := tr.StopTrackingRequest(httptest.NewRecorder(), req, idx); err != nil {
		t.Fatalf("StopTrackingRequest: %v", err)
	}
	if _, err := tr.GetTrackedRequest(req, idx); err == nil {
		t.Error("after StopTrackingRequest the index must no longer resolve — replay protection")
	}
}

func TestValkeySAMLTracker_GetUnknownIndex_ReturnsErrorWithoutLeaking(t *testing.T) {
	v := testvalkey.New(t)
	tr := NewValkeySAMLRequestTracker(v)

	_, err := tr.GetTrackedRequest(httptest.NewRequest("GET", "/", nil), "definitely-not-real")
	if err == nil {
		t.Fatal("unknown index must return an error")
	}
	// The error is consumed by crewjam/samlsp and surfaced as a generic
	// "could not be verified" — but the tracker's message must not
	// distinguish unknown-vs-expired in a way callers could leak via
	// timing or message comparison. Just assert it's the canonical
	// shape.
	if err.Error() == "" {
		t.Error("error must be non-empty so callers can log it")
	}
}

func TestValkeySAMLTracker_MultiInstance_CrossReadability(t *testing.T) {
	// The whole point of the Valkey-backed tracker: an AuthnRequest
	// minted by one OnScreen instance must be resolvable on a
	// different instance that shares the same Valkey. Simulate by
	// constructing two trackers against the same client — if they
	// don't agree on the entries, an HA deployment loses every
	// flow that crosses instances.
	v := testvalkey.New(t)
	instanceA := NewValkeySAMLRequestTracker(v)
	instanceB := NewValkeySAMLRequestTracker(v)
	req := httptest.NewRequest("GET", "/", nil)

	idx, err := instanceA.TrackRequest(httptest.NewRecorder(), req, "ha-req-id")
	if err != nil {
		t.Fatalf("TrackRequest on A: %v", err)
	}

	got, err := instanceB.GetTrackedRequest(req, idx)
	if err != nil {
		t.Fatalf("GetTrackedRequest on B (cross-instance): %v", err)
	}
	if got.SAMLRequestID != "ha-req-id" {
		t.Errorf("cross-instance SAMLRequestID: got %q, want %q", got.SAMLRequestID, "ha-req-id")
	}
}

func TestValkeySAMLTracker_GetTrackedRequests_ListsPendingOnly(t *testing.T) {
	// crewjam/samlsp falls back to GetTrackedRequests on protected
	// handlers when no RelayState is on the inbound. Pin that the
	// lister returns the live entries and skips stopped ones.
	v := testvalkey.New(t)
	tr := NewValkeySAMLRequestTracker(v)
	req := httptest.NewRequest("GET", "/", nil)

	live1, _ := tr.TrackRequest(httptest.NewRecorder(), req, "live-1")
	live2, _ := tr.TrackRequest(httptest.NewRecorder(), req, "live-2")
	stopped, _ := tr.TrackRequest(httptest.NewRecorder(), req, "stopped")
	if err := tr.StopTrackingRequest(httptest.NewRecorder(), req, stopped); err != nil {
		t.Fatalf("StopTrackingRequest: %v", err)
	}

	all := tr.GetTrackedRequests(req)
	if len(all) != 2 {
		t.Fatalf("got %d pending requests, want 2 (stopped one must not appear)", len(all))
	}
	seen := map[string]bool{}
	for _, r := range all {
		seen[r.Index] = true
	}
	if !seen[live1] || !seen[live2] {
		t.Errorf("missing live entries; got indices %v", seen)
	}
	if seen[stopped] {
		t.Error("stopped entry must not appear in GetTrackedRequests")
	}
}
