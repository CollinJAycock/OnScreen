package settings

import (
	"errors"
	"testing"
)

func TestDVRConfig_DefaultsAreOff(t *testing.T) {
	c := DVRConfig{}.WithDefaults()
	if c.Remux || c.Transcode || c.DeleteOriginal {
		t.Errorf("zero config switched something on: %+v", c)
	}
	if c.RemuxContainer != DVRContainerMKV || c.TranscodeCodec != DVRCodecH264 ||
		c.TranscodeQuality != DVRQualityBalanced || c.Commercials != DVRCommercialsOff {
		t.Errorf("defaults = %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("defaults invalid: %v", err)
	}
}

func TestDVRConfig_Validate(t *testing.T) {
	cases := []struct {
		name string
		cfg  DVRConfig
		ok   bool
	}{
		{"cut needs a rewrite", DVRConfig{Commercials: DVRCommercialsCut}, false},
		{"cut with remux", DVRConfig{Remux: true, Commercials: DVRCommercialsCut}, true},
		{"cut with transcode", DVRConfig{Transcode: true, Commercials: DVRCommercialsCut}, true},
		{"mark alone", DVRConfig{Commercials: DVRCommercialsMark}, true},
		{"bad container", DVRConfig{RemuxContainer: "avi"}, false},
		{"bad codec", DVRConfig{TranscodeCodec: "av1"}, false},
		{"bad quality", DVRConfig{TranscodeQuality: "lossless"}, false},
		{"bad commercials", DVRConfig{Commercials: "skip"}, false},
		{"hevc mp4 small", DVRConfig{Transcode: true, TranscodeCodec: DVRCodecHEVC, RemuxContainer: DVRContainerMP4, TranscodeQuality: DVRQualitySmall}, true},
	}
	for _, tc := range cases {
		err := tc.cfg.WithDefaults().Validate()
		if tc.ok && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidDVRConfig) {
			t.Errorf("%s: err = %v, want ErrInvalidDVRConfig", tc.name, err)
		}
	}
}
