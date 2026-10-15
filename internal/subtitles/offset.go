package subtitles

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MaxOffsetMS bounds a stored subtitle timing offset either way: ten
// minutes, far beyond any real mistiming (a wrong-cut release is seconds
// off, a wrong-framerate one drifts rather than shifts). Matches the CHECK
// constraints in migration 00055.
const MaxOffsetMS = 10 * 60 * 1000

// ErrOffsetRange is returned by ValidateOffset for an offset outside
// ±MaxOffsetMS.
var ErrOffsetRange = fmt.Errorf("offset_ms must be between %d and %d", -MaxOffsetMS, MaxOffsetMS)

// ValidateOffset reports whether ms is a storable offset.
func ValidateOffset(ms int) error {
	if ms < -MaxOffsetMS || ms > MaxOffsetMS {
		return ErrOffsetRange
	}
	return nil
}

// ShiftVTT moves every cue of a WebVTT document by offsetMS milliseconds
// (positive = later). A cue that would start before 0 starts at 0; one that
// would also END at or before 0 is dropped, with its identifier. Everything
// else (the header, NOTE / STYLE / REGION blocks, cue settings, cue text) is
// kept as it was. Lines that look like a timing line but don't parse are left
// alone rather than guessed at. Line endings come out as \n.
//
// offsetMS == 0 returns data unchanged.
func ShiftVTT(data []byte, offsetMS int) []byte {
	if offsetMS == 0 || len(data) == 0 {
		return data
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blank := func(s string) bool { return strings.TrimSpace(s) == "" }

	for i := 0; i < len(lines); {
		line := lines[i]
		start, end, settings, ok := parseTimingLine(line)
		if !ok {
			out = append(out, line)
			i++
			continue
		}
		start += int64(offsetMS)
		end += int64(offsetMS)
		if end > 0 {
			if start < 0 {
				start = 0
			}
			out = append(out, formatVTTTime(start)+" --> "+formatVTTTime(end)+settings)
			i++
			continue
		}
		// Drop the cue: its identifier (a lone line right after a blank one,
		// never the header), the timing line, the text up to the next blank
		// line, and that blank line so the gap doesn't double.
		if n := len(out); n >= 2 && !blank(out[n-1]) && blank(out[n-2]) &&
			!strings.HasPrefix(out[n-1], "WEBVTT") {
			out = out[:n-1]
		}
		i++
		for i < len(lines) && !blank(lines[i]) {
			if _, _, _, timing := parseTimingLine(lines[i]); timing {
				break // malformed input with no gap before the next cue
			}
			i++
		}
		if i < len(lines) && blank(lines[i]) {
			i++
		}
	}
	return []byte(strings.Join(out, "\n"))
}

// parseTimingLine splits a cue timing line ("00:01:02.500 --> 00:01:04.000
// align:start") into start and end in milliseconds and the settings suffix
// (with its leading space). SRT-style commas are accepted.
func parseTimingLine(line string) (start, end int64, settings string, ok bool) {
	left, right, found := strings.Cut(line, "-->")
	if !found {
		return 0, 0, "", false
	}
	right = strings.TrimLeft(right, " \t")
	endStr := right
	if i := strings.IndexAny(right, " \t"); i >= 0 {
		endStr, settings = right[:i], right[i:]
	}
	s, err := parseVTTTime(strings.TrimSpace(left))
	if err != nil {
		return 0, 0, "", false
	}
	e, err := parseVTTTime(endStr)
	if err != nil {
		return 0, 0, "", false
	}
	return s, e, strings.TrimRight(settings, " \t"), true
}

var errBadTimestamp = errors.New("bad timestamp")

// parseVTTTime parses "HH:MM:SS.mmm" or "MM:SS.mmm" (hours may run past two
// digits; "," is accepted for "."; a short fraction is read as decimals).
func parseVTTTime(s string) (int64, error) {
	s = strings.Replace(s, ",", ".", 1)
	clock, frac, _ := strings.Cut(s, ".")
	parts := strings.Split(clock, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, errBadTimestamp
	}
	var h, m, sec int64
	nums := make([]int64, len(parts))
	for i, p := range parts {
		if p == "" {
			return 0, errBadTimestamp
		}
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return 0, errBadTimestamp
		}
		nums[i] = n
	}
	if len(nums) == 3 {
		h, m, sec = nums[0], nums[1], nums[2]
	} else {
		m, sec = nums[0], nums[1]
	}
	if m > 59 || sec > 59 {
		return 0, errBadTimestamp
	}
	var ms int64
	if frac != "" {
		if len(frac) > 3 {
			frac = frac[:3]
		}
		for len(frac) < 3 {
			frac += "0"
		}
		n, err := strconv.ParseInt(frac, 10, 64)
		if err != nil || n < 0 {
			return 0, errBadTimestamp
		}
		ms = n
	}
	return ((h*60+m)*60+sec)*1000 + ms, nil
}

// formatVTTTime renders milliseconds as HH:MM:SS.mmm.
func formatVTTTime(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3_600_000
	ms -= h * 3_600_000
	m := ms / 60_000
	ms -= m * 60_000
	s := ms / 1000
	ms -= s * 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
