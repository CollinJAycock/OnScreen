//go:build integration

package settings

import (
	"context"
	"testing"
	"time"
)

// The time zone rides in system_config: TimeZone reads what SetSystem stored,
// reports unset as "", and ZoneSource turns it into the location.
func TestService_TimeZoneRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainer)")
	}
	svc := newServiceForTest(t)
	ctx := context.Background()

	if got, err := svc.TimeZone(ctx); err != nil || got != "" {
		t.Fatalf("nothing stored: %q, %v; want \"\", nil", got, err)
	}
	name := "America/Chicago"
	if err := svc.SetSystem(ctx, SystemConfig{TimeZone: &name}); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.TimeZone(ctx); err != nil || got != name {
		t.Fatalf("stored: %q, %v; want %q", got, err, name)
	}
	if got := svc.System(ctx).TimeZone; got == nil || *got != name {
		t.Errorf("System().TimeZone = %v, want %q", got, name)
	}
	z := NewZoneSource(svc)
	z.fallback = time.UTC
	if got := z.Location(ctx).String(); got != name {
		t.Errorf("ZoneSource = %s, want %s", got, name)
	}
}
