package settings

import (
	"errors"
	"testing"
)

func TestDigestConfig_DefaultIsOff(t *testing.T) {
	c := DefaultDigestConfig()
	if c.Enabled {
		t.Error("the digest must default to off server-wide")
	}
	if c.Weekday != 1 || c.Hour != 8 || !c.IncludeContinueWatching || !c.IncludeStats {
		t.Errorf("defaults = %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("defaults invalid: %v", err)
	}
}

func TestDigestConfig_Validate(t *testing.T) {
	cases := []struct {
		name string
		cfg  DigestConfig
		ok   bool
	}{
		{"sunday midnight", DigestConfig{Weekday: 0, Hour: 0}, true},
		{"saturday 23h", DigestConfig{Weekday: 6, Hour: 23}, true},
		{"weekday 7", DigestConfig{Weekday: 7, Hour: 8}, false},
		{"negative weekday", DigestConfig{Weekday: -1, Hour: 8}, false},
		{"hour 24", DigestConfig{Weekday: 1, Hour: 24}, false},
	}
	for _, tc := range cases {
		err := tc.cfg.Validate()
		if tc.ok && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidDigestConfig) {
			t.Errorf("%s: err = %v, want ErrInvalidDigestConfig", tc.name, err)
		}
	}
}
