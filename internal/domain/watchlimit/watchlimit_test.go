package watchlimit

import (
	"testing"
	"time"
)

func intp(i int) *int { return &i }

// at builds a local time at the given hour:minute on a fixed date.
func at(hour, min int) time.Time {
	return time.Date(2026, 1, 2, hour, min, 0, 0, time.Local)
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name        string
		policy      Policy
		usedSeconds int
		now         time.Time
		wantAllowed bool
		wantReason  string
	}{
		{
			name:        "unrestricted policy always allowed",
			policy:      Policy{},
			usedSeconds: 99999,
			now:         at(3, 0),
			wantAllowed: true,
		},
		{
			name:        "daily cap: under limit allowed",
			policy:      Policy{DailyLimitMinutes: intp(120)},
			usedSeconds: 119 * 60,
			now:         at(12, 0),
			wantAllowed: true,
		},
		{
			name:        "daily cap: exactly at limit blocked",
			policy:      Policy{DailyLimitMinutes: intp(120)},
			usedSeconds: 120 * 60,
			now:         at(12, 0),
			wantAllowed: false,
			wantReason:  ReasonDailyLimit,
		},
		{
			name:        "daily cap: over limit blocked",
			policy:      Policy{DailyLimitMinutes: intp(60)},
			usedSeconds: 90 * 60,
			now:         at(12, 0),
			wantAllowed: false,
			wantReason:  ReasonDailyLimit,
		},
		{
			name:        "zero daily cap blocks immediately",
			policy:      Policy{DailyLimitMinutes: intp(0)},
			usedSeconds: 0,
			now:         at(12, 0),
			wantAllowed: false,
			wantReason:  ReasonDailyLimit,
		},
		{
			name:        "daytime window: inside allowed",
			policy:      Policy{AllowedStartMinute: intp(8 * 60), AllowedEndMinute: intp(20 * 60)},
			now:         at(12, 0),
			wantAllowed: true,
		},
		{
			name:        "daytime window: at start allowed",
			policy:      Policy{AllowedStartMinute: intp(8 * 60), AllowedEndMinute: intp(20 * 60)},
			now:         at(8, 0),
			wantAllowed: true,
		},
		{
			name:        "daytime window: at end blocked (exclusive)",
			policy:      Policy{AllowedStartMinute: intp(8 * 60), AllowedEndMinute: intp(20 * 60)},
			now:         at(20, 0),
			wantAllowed: false,
			wantReason:  ReasonOutsideHours,
		},
		{
			name:        "daytime window: before start blocked",
			policy:      Policy{AllowedStartMinute: intp(8 * 60), AllowedEndMinute: intp(20 * 60)},
			now:         at(7, 30),
			wantAllowed: false,
			wantReason:  ReasonOutsideHours,
		},
		{
			name:        "overnight window: late evening allowed",
			policy:      Policy{AllowedStartMinute: intp(22 * 60), AllowedEndMinute: intp(6 * 60)},
			now:         at(23, 0),
			wantAllowed: true,
		},
		{
			name:        "overnight window: early morning allowed",
			policy:      Policy{AllowedStartMinute: intp(22 * 60), AllowedEndMinute: intp(6 * 60)},
			now:         at(5, 0),
			wantAllowed: true,
		},
		{
			name:        "overnight window: midday blocked",
			policy:      Policy{AllowedStartMinute: intp(22 * 60), AllowedEndMinute: intp(6 * 60)},
			now:         at(13, 0),
			wantAllowed: false,
			wantReason:  ReasonOutsideHours,
		},
		{
			name:        "equal-bounds window treated as unrestricted",
			policy:      Policy{AllowedStartMinute: intp(0), AllowedEndMinute: intp(0)},
			now:         at(13, 0),
			wantAllowed: true,
		},
		{
			name:        "hours checked before cap: outside-hours wins",
			policy:      Policy{DailyLimitMinutes: intp(120), AllowedStartMinute: intp(8 * 60), AllowedEndMinute: intp(20 * 60)},
			usedSeconds: 10 * 60, // under cap
			now:         at(22, 0),
			wantAllowed: false,
			wantReason:  ReasonOutsideHours,
		},
		{
			name:        "inside hours but over cap blocked",
			policy:      Policy{DailyLimitMinutes: intp(120), AllowedStartMinute: intp(8 * 60), AllowedEndMinute: intp(20 * 60)},
			usedSeconds: 200 * 60,
			now:         at(12, 0),
			wantAllowed: false,
			wantReason:  ReasonDailyLimit,
		},
		{
			name:        "inside hours and under cap allowed",
			policy:      Policy{DailyLimitMinutes: intp(120), AllowedStartMinute: intp(8 * 60), AllowedEndMinute: intp(20 * 60)},
			usedSeconds: 30 * 60,
			now:         at(12, 0),
			wantAllowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, reason := Evaluate(tt.policy, tt.usedSeconds, tt.now)
			if allowed != tt.wantAllowed {
				t.Errorf("allowed = %v, want %v", allowed, tt.wantAllowed)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

func TestRestricted(t *testing.T) {
	tests := []struct {
		name string
		p    Policy
		want bool
	}{
		{"empty", Policy{}, false},
		{"cap only", Policy{DailyLimitMinutes: intp(60)}, true},
		{"window only", Policy{AllowedStartMinute: intp(0), AllowedEndMinute: intp(60)}, true},
		{"half window is not restricted", Policy{AllowedStartMinute: intp(60)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Restricted(); got != tt.want {
				t.Errorf("Restricted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLocalDay(t *testing.T) {
	now := time.Date(2026, 3, 14, 15, 9, 26, 535, time.Local)
	day := LocalDay(now)
	if day.Hour() != 0 || day.Minute() != 0 || day.Second() != 0 || day.Nanosecond() != 0 {
		t.Errorf("LocalDay not midnight: %v", day)
	}
	if day.Year() != 2026 || day.Month() != 3 || day.Day() != 14 {
		t.Errorf("LocalDay wrong date: %v", day)
	}
	if day.Location() != now.Location() {
		t.Errorf("LocalDay changed location: %v", day.Location())
	}
}
