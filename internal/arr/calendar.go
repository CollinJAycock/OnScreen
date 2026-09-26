package arr

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strconv"
	"time"
)

// Timestamp decodes the date fields Radarr and Sonarr put on calendar rows.
// Current releases send full RFC3339 timestamps ("2026-09-28T00:00:00Z"), but
// older builds and hand-edited entries have produced zone-less and date-only
// forms, and an absent date arrives as null. All of those decode here: a
// zone-less value is read as UTC (both apps store dates in UTC), and a value
// that is null, empty or unparseable leaves the zero time. That last case is
// deliberately not an error — one malformed field on one row must not sink the
// whole calendar response. Callers test IsZero before using the value.
type Timestamp struct {
	time.Time
}

// timestampLayouts are the zone-less forms tried after RFC3339. Go accepts a
// fractional-seconds suffix when parsing even if the layout has none, so
// "2026-09-28T01:00:00.0000000" (.NET's default) matches the first one.
var timestampLayouts = []string{
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// UnmarshalJSON implements json.Unmarshaler.
func (t *Timestamp) UnmarshalJSON(b []byte) error {
	t.Time = time.Time{}
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	s, err := strconv.Unquote(string(b))
	if err != nil || s == "" {
		return nil
	}
	if v, err := time.Parse(time.RFC3339, s); err == nil {
		t.Time = v.UTC()
		return nil
	}
	for _, layout := range timestampLayouts {
		if v, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			t.Time = v
			return nil
		}
	}
	return nil
}

// MarshalJSON implements json.Marshaler, writing the zero value as null so a
// round trip through encoding/json is lossless.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.UTC().Format(time.RFC3339))
}

// calendarQuery builds the query shared by both apps' /api/v3/calendar.
// unmonitored=false matches what their own calendar pages show by default: a
// title nobody is waiting for is not "expected to arrive".
func calendarQuery(start, end time.Time) url.Values {
	return url.Values{
		"start":       {start.UTC().Format(time.RFC3339)},
		"end":         {end.UTC().Format(time.RFC3339)},
		"unmonitored": {"false"},
	}
}
