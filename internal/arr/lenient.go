package arr

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Radarr and Sonarr have changed the JSON shape of their status endpoints
// across major versions (numbers that became strings, objects that became
// strings, a paged envelope that used to be a bare array), and third-party
// forks drift further. The download-status sync reads these endpoints every
// few minutes for every approved request, so one odd field must never sink a
// whole response: the helpers here decode what they can and zero the rest,
// the same policy calendar.go's Timestamp applies to dates.

// FlexFloat decodes a JSON number that some builds send as a string ("1234")
// and others as null. Anything unparseable decodes to 0 rather than failing
// the surrounding object — sizes and percentages are advisory.
type FlexFloat float64

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexFloat) UnmarshalJSON(b []byte) error {
	*f = 0
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return nil
	}
	if s[0] == '"' {
		u, err := strconv.Unquote(s)
		if err != nil {
			return nil
		}
		s = strings.TrimSpace(u)
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*f = FlexFloat(v)
	}
	return nil
}

// FlexString decodes a string field that some builds serialise as an object
// — Radarr/Sonarr's HttpUri has at times been written as {"fullUri": "..."}
// instead of a plain string — or as a bare number. Objects without a usable
// member and any other shape decode to "".
type FlexString string

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexString) UnmarshalJSON(b []byte) error {
	*f = ""
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	switch b[0] {
	case '"':
		var s string
		if err := json.Unmarshal(b, &s); err == nil {
			*f = FlexString(s)
		}
	case '{':
		var obj map[string]any
		if err := json.Unmarshal(b, &obj); err != nil {
			return nil
		}
		for _, k := range []string{"fullUri", "FullUri", "uri", "url"} {
			if s, ok := obj[k].(string); ok {
				*f = FlexString(s)
				return nil
			}
		}
	default:
		// A bare number or boolean: keep its literal text.
		if b[0] == '-' || (b[0] >= '0' && b[0] <= '9') {
			*f = FlexString(b)
		}
	}
	return nil
}

// decodeLenient unmarshals data into v, tolerating per-field type mismatches.
// encoding/json keeps filling the remaining fields after a type error and
// only reports the first one; that partial result is exactly what the sync
// wants, so a *json.UnmarshalTypeError is swallowed. Syntax errors (a
// truncated or non-JSON body) are still returned.
func decodeLenient(data []byte, v any) error {
	err := json.Unmarshal(data, v)
	var typeErr *json.UnmarshalTypeError
	if err != nil && errors.As(err, &typeErr) {
		return nil
	}
	return err
}

// errNotObject rejects a record that isn't a JSON object at all ("oops",
// 42, null): decoding one into a struct is only a type error, which
// decodeLenient would accept as an empty row.
var errNotObject = errors.New("arr: record is not a JSON object")

// decodeObject is decodeLenient for one record of a list: the record must be
// a JSON object, but its fields may be individually malformed.
func decodeObject(data []byte, v any) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return errNotObject
	}
	return decodeLenient(data, v)
}

// decodeRecordList decodes either a bare JSON array or a paged envelope
// ({"records": [...], "totalRecords": N}) into raw records. total is the
// envelope's totalRecords, or len(records) for a bare array; paged reports
// which of the two it was (a bare array is the whole list — asking for the
// next page would return it again).
func decodeRecordList(data []byte) (records []json.RawMessage, total int, paged bool, err error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil, 0, false, nil
	}
	if data[0] == '[' {
		if err := json.Unmarshal(data, &records); err != nil {
			return nil, 0, false, err
		}
		return records, len(records), false, nil
	}
	var page struct {
		TotalRecords FlexFloat         `json:"totalRecords"`
		Records      []json.RawMessage `json:"records"`
	}
	if err := decodeLenient(data, &page); err != nil {
		return nil, 0, false, err
	}
	total = int(page.TotalRecords)
	if total < len(page.Records) {
		total = len(page.Records)
	}
	return page.Records, total, true, nil
}

// parseTimeSpan reads a .NET TimeSpan as Radarr/Sonarr serialise it:
// "hh:mm:ss", "d.hh:mm:ss", optionally with fractional seconds. ok is false
// for anything else, including negative spans.
func parseTimeSpan(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "-") {
		return 0, false
	}
	var days int64
	if dot := strings.Index(s, "."); dot >= 0 && dot < strings.Index(s, ":") {
		d, err := strconv.ParseInt(s[:dot], 10, 64)
		if err != nil {
			return 0, false
		}
		days = d
		s = s[dot+1:]
	}
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, err1 := strconv.ParseInt(parts[0], 10, 64)
	m, err2 := strconv.ParseInt(parts[1], 10, 64)
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil || h < 0 || m < 0 || sec < 0 {
		return 0, false
	}
	d := time.Duration(days)*24*time.Hour +
		time.Duration(h)*time.Hour +
		time.Duration(m)*time.Minute +
		time.Duration(sec*float64(time.Second))
	return d, true
}
