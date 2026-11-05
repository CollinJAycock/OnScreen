package media

import "testing"

func TestPrefixTSQuery(t *testing.T) {
	cases := []struct {
		in   string
		want string // "" = nil
	}{
		{"avat", "avat:*"},
		{"Avatar the way", "avatar:* & the:* & way:*"},
		{"  spider-man: no way ", "spider:* & man:* & way:*"},
		{"Amélie", "amélie:*"},
		{"Amé", "amé:*"}, // runes, not bytes
		{"2001", "2001:*"},
		// Words of one or two letters match too much as prefixes.
		{"avatar wa", "avatar:*"},
		{"ti", ""},
		{"28 da", ""},
		// tsquery operators can't get through.
		{"abc & bcd | !cde <-> (def):*", "abc:* & bcd:* & cde:* & def:*"},
		{"'; DROP TABLE", "drop:* & table:*"},
		{"!!!", ""},
		{"", ""},
		{"one two three four five six seven eight nine ten", "one:* & two:* & three:* & four:* & five:* & six:* & seven:* & eight:*"},
	}
	for _, c := range cases {
		got := PrefixTSQuery(c.in)
		switch {
		case c.want == "" && got != nil:
			t.Errorf("PrefixTSQuery(%q) = %q, want nil", c.in, *got)
		case c.want != "" && (got == nil || *got != c.want):
			g := "<nil>"
			if got != nil {
				g = *got
			}
			t.Errorf("PrefixTSQuery(%q) = %q, want %q", c.in, g, c.want)
		}
	}
}
