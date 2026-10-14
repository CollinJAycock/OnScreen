package settings

import (
	"errors"
	"testing"
)

func TestRemoteAccessConfig_DefaultIsOff(t *testing.T) {
	c := RemoteAccessConfig{}.WithDefaults()
	if c.MapPort {
		t.Fatal("port mapping must default to off")
	}
	if c.Protocol != "TCP" || c.ExternalPort != 0 {
		t.Fatalf("defaults = %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
}

func TestRemoteAccessConfig_Validate(t *testing.T) {
	cases := []struct {
		name string
		cfg  RemoteAccessConfig
		ok   bool
	}{
		{"listen port", RemoteAccessConfig{MapPort: true}, true},
		{"custom port", RemoteAccessConfig{MapPort: true, ExternalPort: 32400}, true},
		{"lowercase tcp", RemoteAccessConfig{Protocol: "tcp"}, true},
		{"negative port", RemoteAccessConfig{ExternalPort: -1}, false},
		{"port too big", RemoteAccessConfig{ExternalPort: 70000}, false},
		{"udp", RemoteAccessConfig{Protocol: "UDP"}, false},
	}
	for _, tc := range cases {
		err := tc.cfg.WithDefaults().Validate()
		if tc.ok && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidRemoteAccessConfig) {
			t.Errorf("%s: err = %v, want ErrInvalidRemoteAccessConfig", tc.name, err)
		}
	}
}
