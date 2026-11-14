package watchlimit

import (
	"context"
	"testing"
	"time"
)

// Store.Now is the household clock every gate evaluates with: with a zone it
// reads in that zone; without one it is the process clock, as before.
func TestStoreNow_UsesTheHouseholdZone(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(nil).WithZone(func(context.Context) *time.Location { return tokyo })
	now := s.Now(context.Background())
	if now.Location() != tokyo {
		t.Errorf("Now in %v, want Asia/Tokyo", now.Location())
	}
	if d := time.Since(now); d < 0 || d > time.Second {
		t.Errorf("Now is %v away from the wall clock", d)
	}

	if got := NewStore(nil).Now(context.Background()).Location(); got != time.Local {
		t.Errorf("no zone: Now in %v, want the process zone", got)
	}
	nilZone := NewStore(nil).WithZone(func(context.Context) *time.Location { return nil })
	if got := nilZone.Now(context.Background()).Location(); got != time.Local {
		t.Errorf("zone func returning nil: Now in %v, want the process zone", got)
	}
}

// The same instant is inside the allowed hours in one zone and outside them
// in another, and on a different usage day: why the zone matters.
func TestEvaluateAndLocalDay_FollowTheZoneOfNow(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	instant := time.Date(2026, 1, 1, 19, 0, 0, 0, time.UTC) // 04:00 on Jan 2 in Tokyo
	window := Policy{AllowedStartMinute: intp(8 * 60), AllowedEndMinute: intp(20 * 60)}

	if ok, _ := Evaluate(window, 0, instant); !ok {
		t.Error("19:00 UTC should be inside 08:00–20:00 in UTC")
	}
	if ok, reason := Evaluate(window, 0, instant.In(tokyo)); ok || reason != ReasonOutsideHours {
		t.Errorf("04:00 Tokyo: allowed=%v reason=%q, want outside_allowed_hours", ok, reason)
	}
	if d := LocalDay(instant.In(tokyo)); d.Day() != 2 || d.Location() != tokyo {
		t.Errorf("usage day in Tokyo = %v, want Jan 2 Tokyo", d)
	}
}
