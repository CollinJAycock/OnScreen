package settings

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestValidateTimeZone(t *testing.T) {
	for _, ok := range []string{"UTC", "America/Chicago", "Europe/Berlin", "Asia/Kolkata"} {
		if err := ValidateTimeZone(ok); err != nil {
			t.Errorf("%q: %v, want valid", ok, err)
		}
	}
	for _, bad := range []string{"", "Local", "Mars/Olympus_Mons", "../../etc/passwd", "EST5EDT,M3"} {
		err := ValidateTimeZone(bad)
		if err == nil {
			t.Errorf("%q: valid, want refused", bad)
			continue
		}
		if !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("%q: %v, want ErrInvalidSetting", bad, err)
		}
	}
}

func TestProcessTimeZoneName_PrefersTZ(t *testing.T) {
	t.Setenv("TZ", "America/Chicago")
	if got := ProcessTimeZoneName(); got != "America/Chicago" {
		t.Errorf("TZ=America/Chicago: got %q", got)
	}
	t.Setenv("TZ", ":Europe/Berlin") // the POSIX leading colon
	if got := ProcessTimeZoneName(); got != "Europe/Berlin" {
		t.Errorf("TZ=:Europe/Berlin: got %q", got)
	}
}

type fakeZoneReader struct {
	name  string
	err   error
	reads int
}

func (f *fakeZoneReader) TimeZone(context.Context) (string, error) {
	f.reads++
	return f.name, f.err
}

func TestZoneSource(t *testing.T) {
	ctx := context.Background()
	src := &fakeZoneReader{}
	z := NewZoneSource(src)
	z.fallback = time.UTC
	clock := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	z.now = func() time.Time { return clock }

	// Unset: the process zone.
	if got := z.Location(ctx); got != time.UTC {
		t.Errorf("unset: %v, want the fallback", got)
	}

	// Set: applied once the cached read ages out — no restart.
	src.name = "Asia/Tokyo"
	if got := z.Location(ctx); got != time.UTC {
		t.Errorf("within the cache: %v, want the cached zone", got)
	}
	clock = clock.Add(zoneCacheTTL)
	if got := z.Location(ctx).String(); got != "Asia/Tokyo" {
		t.Errorf("after the cache: %v, want Asia/Tokyo", got)
	}
	if src.reads != 2 {
		t.Errorf("%d reads, want one per cache period", src.reads)
	}

	// A failed read keeps the zone it has instead of jumping to the fallback.
	src.err = errors.New("db down")
	clock = clock.Add(zoneCacheTTL)
	if got := z.Location(ctx).String(); got != "Asia/Tokyo" {
		t.Errorf("read failure: %v, want the last zone kept", got)
	}

	// A stored name this build can't load falls back.
	src.err, src.name = nil, "Not/A_Zone"
	clock = clock.Add(zoneCacheTTL)
	if got := z.Location(ctx); got != time.UTC {
		t.Errorf("unloadable stored zone: %v, want the fallback", got)
	}
}
