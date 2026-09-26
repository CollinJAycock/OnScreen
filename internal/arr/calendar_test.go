package arr

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTimestamp_DecodesEveryShape(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{`"2026-09-28T00:00:00Z"`, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)},
		{`"2026-09-28T01:30:00.123Z"`, time.Date(2026, 9, 28, 1, 30, 0, 123e6, time.UTC)},
		// An offset is normalised to UTC.
		{`"2026-09-28T02:00:00+02:00"`, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)},
		// Zone-less forms are read as UTC.
		{`"2026-09-28T01:00:00"`, time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)},
		{`"2026-09-28T01:00:00.0000000"`, time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)},
		{`"2026-09-28"`, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)},
		// Absent / unusable values are the zero time, never an error.
		{`null`, time.Time{}},
		{`""`, time.Time{}},
		{`"not a date"`, time.Time{}},
		{`12345`, time.Time{}},
	}
	for _, c := range cases {
		var ts Timestamp
		if err := json.Unmarshal([]byte(c.in), &ts); err != nil {
			t.Errorf("%s: unexpected error %v", c.in, err)
			continue
		}
		if !ts.Equal(c.want) {
			t.Errorf("%s: got %v, want %v", c.in, ts.Time, c.want)
		}
		if !ts.IsZero() && ts.Location() != time.UTC {
			t.Errorf("%s: location = %v, want UTC", c.in, ts.Location())
		}
	}
}

func TestTimestamp_RoundTrip(t *testing.T) {
	in := struct {
		A Timestamp `json:"a"`
		B Timestamp `json:"b"`
	}{A: Timestamp{time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `{"a":"2026-09-28T00:00:00Z","b":null}` {
		t.Errorf("marshal = %s", b)
	}
}
