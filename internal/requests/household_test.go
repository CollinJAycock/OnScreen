package requests

import (
	"context"
	"errors"
	"testing"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// S7: the pending-request cap counts the household (owner + managed profiles,
// GetUserRequestPermissions.household_pending), so creating profiles doesn't
// multiply it. The profile's own pending count is still checked too.
func TestCreate_PendingCapCountsTheHousehold(t *testing.T) {
	h := newHarness(t)
	// A profile with no open requests of its own, in a household at the cap.
	uid := h.user(gen.GetUserRequestPermissionsRow{HouseholdPending: maxPendingRequestsPerUser})
	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949}); !errors.Is(err, ErrTooManyPending) {
		t.Errorf("household at the cap: err = %v, want ErrTooManyPending", err)
	}

	uid = h.user(gen.GetUserRequestPermissionsRow{HouseholdPending: maxPendingRequestsPerUser - 1})
	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949}); err != nil {
		t.Errorf("household under the cap: %v", err)
	}
}

// S7: a profile whose owner may not request is refused like the owner. The
// combined flag comes from GetUserRequestPermissions (can_request = the
// profile's AND the owner's); Create honours it as it always has.
func TestCreate_ProfileOfBlockedOwnerRefused(t *testing.T) {
	h := newHarness(t)
	uid := h.userRow(gen.GetUserRequestPermissionsRow{CanRequest: false})
	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949}); !errors.Is(err, ErrRequestsDisabled) {
		t.Errorf("err = %v, want ErrRequestsDisabled", err)
	}
	if ok, err := h.svc.CanRequest(context.Background(), uid); err != nil || ok {
		t.Errorf("CanRequest = %v, %v; want false (the Discover gate uses it)", ok, err)
	}
}
