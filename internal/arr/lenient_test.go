package arr

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFlexFloat(t *testing.T) {
	cases := map[string]float64{
		`12.5`:  12.5,
		`"42"`:  42,
		`" 7 "`: 7,
		`null`:  0,
		`"n/a"`: 0,
		`true`:  0,
		`1.5e9`: 1.5e9,
		`"1e3"`: 1000,
		`-3`:    -3,
		`"1"`:   1,
	}
	for in, want := range cases {
		var f FlexFloat
		if err := json.Unmarshal([]byte(in), &f); err != nil {
			t.Errorf("%s: error %v", in, err)
			continue
		}
		if float64(f) != want {
			t.Errorf("%s: got %v, want %v", in, f, want)
		}
	}
}

func TestFlexString(t *testing.T) {
	cases := map[string]string{
		`"https://x"`:             "https://x",
		`{"fullUri":"https://y"}`: "https://y",
		`{"other":1}`:             "",
		`null`:                    "",
		`12`:                      "12",
		`true`:                    "",
		`["a"]`:                   "",
	}
	for in, want := range cases {
		var s FlexString
		if err := json.Unmarshal([]byte(in), &s); err != nil {
			t.Errorf("%s: error %v", in, err)
			continue
		}
		if string(s) != want {
			t.Errorf("%s: got %q, want %q", in, s, want)
		}
	}
}

func TestDecodeRecordList(t *testing.T) {
	recs, total, paged, err := decodeRecordList([]byte(`[{"a":1},{"a":2}]`))
	if err != nil || len(recs) != 2 || total != 2 || paged {
		t.Errorf("bare array: %d %d %v %v", len(recs), total, paged, err)
	}
	recs, total, paged, err = decodeRecordList([]byte(`{"totalRecords":10,"records":[{"a":1}]}`))
	if err != nil || len(recs) != 1 || total != 10 || !paged {
		t.Errorf("envelope: %d %d %v %v", len(recs), total, paged, err)
	}
	// A total smaller than the page is corrected up.
	_, total, _, _ = decodeRecordList([]byte(`{"totalRecords":"x","records":[{},{}]}`))
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if recs, _, _, err := decodeRecordList([]byte(` null `)); err != nil || recs != nil {
		t.Errorf("null: %v %v", recs, err)
	}
	if _, _, _, err := decodeRecordList([]byte(`<html>`)); err == nil {
		t.Error("non-JSON must error")
	}
}

func TestParseTimeSpan(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"00:00:00", 0, true},
		{"01:02:03", time.Hour + 2*time.Minute + 3*time.Second, true},
		{"2.00:00:01", 48*time.Hour + time.Second, true},
		{"00:00:01.25", 1250 * time.Millisecond, true},
		{"1:2", 0, false},
		{"a:b:c", 0, false},
		{"x.00:00:00", 0, false},
		{"", 0, false},
		{"-1.00:00:00", 0, false},
	}
	for _, c := range cases {
		got, ok := parseTimeSpan(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("%q: got %v %v, want %v %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
