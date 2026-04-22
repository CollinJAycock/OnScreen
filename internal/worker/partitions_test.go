package worker

import (
	"testing"
	"time"
)

func TestValidPartitionName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"watch_events_2026_01", true},
		{"watch_events_1999_12", true},
		{"watch_events_2026_1", false},   // single-digit month
		{"watch_events_26_01", false},    // 2-digit year
		{"watch_events_2026_13", true},   // regex doesn't validate month range; that's fine
		{"WATCH_EVENTS_2026_01", false},  // case-sensitive
		{"watch_events_2026_01;", false}, // SQL injection attempt
		{"; DROP TABLE", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validPartitionName.MatchString(c.name); got != c.want {
				t.Errorf("validPartitionName(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestNextMonthStart(t *testing.T) {
	got := nextMonthStart()

	if got.Day() != 1 {
		t.Errorf("day: got %d, want 1", got.Day())
	}
	if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 || got.Nanosecond() != 0 {
		t.Errorf("not midnight: got %v", got)
	}
	if got.Location() != time.UTC {
		t.Errorf("not UTC: got %v", got.Location())
	}
	now := time.Now().UTC()
	if !got.After(now) {
		t.Errorf("nextMonthStart (%v) is not after now (%v)", got, now)
	}
	// Should be no more than ~32 days out (longest month + DST slack).
	if got.Sub(now) > 32*24*time.Hour {
		t.Errorf("nextMonthStart too far in future: %v", got.Sub(now))
	}
}
