package itemlabel

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Kids":            "kids",
		"  Kids   OK  ":   "kids ok",
		"HORROR\tmovies":  "horror movies",
		"":                "",
		"   ":             "",
		"Ünïcode Läbel":   "ünïcode läbel",
		"line\nbreak tag": "line break tag",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeList(t *testing.T) {
	got, err := NormalizeList([]string{"War", " horror", "", "war", "HORROR "}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "horror,war" {
		t.Errorf("got %v, want sorted de-duplicated [horror war]", got)
	}
	if got, err := NormalizeList(nil, 10); err != nil || got == nil || len(got) != 0 {
		t.Errorf("nil input = %v, %v; want empty non-nil", got, err)
	}

	tooLong := strings.Repeat("a", MaxLen+1)
	if _, err := NormalizeList([]string{tooLong}, 10); !errors.Is(err, ErrInvalid) {
		t.Errorf("over-long label: err = %v, want ErrInvalid", err)
	}
	if _, err := NormalizeList([]string{strings.Repeat("é", MaxLen)}, 10); err != nil {
		t.Errorf("MaxLen runes (multi-byte) refused: %v", err)
	}
	if _, err := NormalizeList([]string{"bad\x00label"}, 10); !errors.Is(err, ErrInvalid) {
		t.Errorf("control character: err = %v, want ErrInvalid", err)
	}
	if _, err := NormalizeList([]string{"a", "b", "c"}, 2); !errors.Is(err, ErrInvalid) {
		t.Errorf("over the cap: err = %v, want ErrInvalid", err)
	}
	// Duplicates collapse before the cap is applied.
	if _, err := NormalizeList([]string{"a", "A", " a "}, 1); err != nil {
		t.Errorf("duplicates counted against the cap: %v", err)
	}
}

func TestFilterPermits(t *testing.T) {
	cases := []struct {
		name      string
		f         Filter
		effective []string
		want      bool
	}{
		{"zero filter", Filter{}, []string{"horror"}, true},
		{"blocked hit", Filter{Blocked: []string{"horror"}}, []string{"kids", "horror"}, false},
		{"blocked miss", Filter{Blocked: []string{"horror"}}, []string{"kids"}, true},
		{"blocked, unlabelled", Filter{Blocked: []string{"horror"}}, nil, true},
		{"allowed hit", Filter{Allowed: []string{"kids"}}, []string{"kids"}, true},
		{"allowed miss", Filter{Allowed: []string{"kids"}}, []string{"family"}, false},
		{"allowed, unlabelled", Filter{Allowed: []string{"kids"}}, nil, false},
		{"blocked beats allowed", Filter{Blocked: []string{"horror"}, Allowed: []string{"kids"}}, []string{"kids", "horror"}, false},
	}
	for _, tc := range cases {
		if got := tc.f.Permits(tc.effective); got != tc.want {
			t.Errorf("%s: Permits = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFilterArgsAndKey(t *testing.T) {
	var zero Filter
	if zero.Active() || zero.BlockedArg() != nil || zero.AllowedArg() != nil || zero.Key() != "" {
		t.Error("zero filter must be inactive, with NULL args and an empty key")
	}
	empty := Filter{Blocked: []string{}, Allowed: []string{}}
	if empty.Active() || empty.BlockedArg() != nil || empty.AllowedArg() != nil {
		t.Error("empty lists must behave as no restriction (NULL args)")
	}
	a := Filter{Blocked: []string{"horror"}}
	b := Filter{Allowed: []string{"horror"}}
	if a.Key() == b.Key() || a.Key() == "" {
		t.Errorf("blocked and allowed lists must key differently: %q vs %q", a.Key(), b.Key())
	}
}
