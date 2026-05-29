// Package watchlimit holds the parental watch-control policy: a per-user daily
// active-watch cap plus an optional allowed-hours window, enforced server-side
// at the playback chokepoints (progress heartbeat + transcode start).
//
// The evaluation logic here is pure and timezone-agnostic — callers pass the
// "now" they want evaluated (the server's local time in production) and the
// seconds already watched today. Storage + accounting live in store.go.
package watchlimit

import "time"

// Policy is a user's parental watch policy. The zero value (all nil) is
// unrestricted; Restricted reports whether any limit applies.
type Policy struct {
	// DailyLimitMinutes caps active watching per local day. nil = no cap.
	DailyLimitMinutes *int
	// AllowedStartMinute / AllowedEndMinute bound playback to a window,
	// expressed as minutes from local midnight in [0,1440). They are set or
	// cleared together. start < end is a same-day window; start > end wraps
	// midnight (an overnight window). nil = no time-of-day restriction.
	AllowedStartMinute *int
	AllowedEndMinute   *int
}

// Restricted reports whether the policy imposes any limit. An unrestricted
// policy lets callers skip the usage read + accounting entirely.
func (p Policy) Restricted() bool {
	return p.DailyLimitMinutes != nil ||
		(p.AllowedStartMinute != nil && p.AllowedEndMinute != nil)
}

// Block reasons returned by Evaluate (also the `reason` body on the 403).
const (
	ReasonOutsideHours = "outside_allowed_hours"
	ReasonDailyLimit   = "daily_limit_reached"
)

// Evaluate decides whether playback is allowed right now under the policy,
// given how many seconds the user has already watched today. now is read in
// its own location, so the caller controls the timezone (server-local in
// production). A blocked result carries the reason; an allowed result has "".
func Evaluate(p Policy, usedSeconds int, now time.Time) (allowed bool, reason string) {
	// Allowed-hours window first — cheap and independent of usage.
	if p.AllowedStartMinute != nil && p.AllowedEndMinute != nil {
		nowMin := now.Hour()*60 + now.Minute()
		start, end := *p.AllowedStartMinute, *p.AllowedEndMinute
		var inWindow bool
		switch {
		case start == end:
			// Equal bounds are a degenerate window. Treat as "no restriction"
			// (always allowed) rather than "never" — locking a user out for
			// 24h from an admin typo (e.g. 0..0) is the worse failure mode.
			inWindow = true
		case start < end:
			inWindow = nowMin >= start && nowMin < end
		default: // start > end: overnight window wrapping midnight
			inWindow = nowMin >= start || nowMin < end
		}
		if !inWindow {
			return false, ReasonOutsideHours
		}
	}

	// Daily cap.
	if p.DailyLimitMinutes != nil && usedSeconds >= *p.DailyLimitMinutes*60 {
		return false, ReasonDailyLimit
	}

	return true, ""
}

// LocalDay returns midnight of now's calendar date in now's own location — the
// key for the per-day usage row. Keeping the date computation in Go (rather
// than relying on the database session timezone) makes "today" deterministic
// against the server's local clock.
func LocalDay(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
}
