package settings

import (
	"context"
	"errors"
	"fmt"
)

const keyDVRConfig = "dvr_config"

// DVRConfig is the DVR post-processing configuration (Settings ▸ Live TV).
// The zero value — every switch off — is the behaviour before post-processing
// existed: a capture is left exactly as the tuner wrote it. That holds for new
// installs and upgrades alike; the settings page recommends turning remux on.
//
// The post-processing script and the comskip binary are deliberately NOT here:
// they are executable paths, settable only through the environment
// (ONSCREEN_DVR_POSTPROCESS_SCRIPT, ONSCREEN_COMSKIP_PATH). A UI-settable
// executable would turn any admin session into remote code execution.
type DVRConfig struct {
	// Remux rewrites the capture into RemuxContainer by stream copy, with
	// regenerated timestamps, so seeking is reliable.
	Remux          bool   `json:"remux"`
	RemuxContainer string `json:"remux_container"` // "mkv" | "mp4"
	// Transcode re-encodes the video to save space (audio is copied). Output
	// goes into RemuxContainer.
	Transcode        bool   `json:"transcode"`
	TranscodeCodec   string `json:"transcode_codec"`   // "h264" | "hevc"
	TranscodeQuality string `json:"transcode_quality"` // "high" | "balanced" | "small"
	// DeleteOriginal removes the capture once a processed copy has been
	// verified. Off keeps it in a ".dvr-originals" folder beside the recording.
	DeleteOriginal bool `json:"delete_original"`
	// Commercials: "off" | "mark" (Skip ad button) | "cut" (removed from the
	// file; needs Remux or Transcode).
	Commercials string `json:"commercials"`
}

// DVR settings values.
const (
	DVRContainerMKV = "mkv"
	DVRContainerMP4 = "mp4"

	DVRCodecH264 = "h264"
	DVRCodecHEVC = "hevc"

	DVRQualityHigh     = "high"
	DVRQualityBalanced = "balanced"
	DVRQualitySmall    = "small"

	DVRCommercialsOff  = "off"
	DVRCommercialsMark = "mark"
	DVRCommercialsCut  = "cut"
)

// ErrInvalidDVRConfig is returned by DVRConfig.Validate.
var ErrInvalidDVRConfig = errors.New("settings: invalid DVR config")

// WithDefaults fills empty enum fields with their defaults (MKV, H.264,
// balanced, commercials off).
func (c DVRConfig) WithDefaults() DVRConfig {
	if c.RemuxContainer == "" {
		c.RemuxContainer = DVRContainerMKV
	}
	if c.TranscodeCodec == "" {
		c.TranscodeCodec = DVRCodecH264
	}
	if c.TranscodeQuality == "" {
		c.TranscodeQuality = DVRQualityBalanced
	}
	if c.Commercials == "" {
		c.Commercials = DVRCommercialsOff
	}
	return c
}

// Validate checks every enum and that cut mode has a rewrite to cut during.
// Call on a config that has been through WithDefaults.
func (c DVRConfig) Validate() error {
	switch c.RemuxContainer {
	case DVRContainerMKV, DVRContainerMP4:
	default:
		return fmt.Errorf("%w: remux_container must be mkv or mp4", ErrInvalidDVRConfig)
	}
	switch c.TranscodeCodec {
	case DVRCodecH264, DVRCodecHEVC:
	default:
		return fmt.Errorf("%w: transcode_codec must be h264 or hevc", ErrInvalidDVRConfig)
	}
	switch c.TranscodeQuality {
	case DVRQualityHigh, DVRQualityBalanced, DVRQualitySmall:
	default:
		return fmt.Errorf("%w: transcode_quality must be high, balanced or small", ErrInvalidDVRConfig)
	}
	switch c.Commercials {
	case DVRCommercialsOff, DVRCommercialsMark:
	case DVRCommercialsCut:
		if !c.Remux && !c.Transcode {
			return fmt.Errorf("%w: cutting commercials needs remux or transcode enabled", ErrInvalidDVRConfig)
		}
	default:
		return fmt.Errorf("%w: commercials must be off, mark or cut", ErrInvalidDVRConfig)
	}
	return nil
}

// DVR returns the stored DVR config with defaults filled in; all switches off
// when nothing is stored.
func (s *Service) DVR(ctx context.Context) DVRConfig {
	var cfg DVRConfig
	s.getJSON(ctx, keyDVRConfig, &cfg)
	return cfg.WithDefaults()
}

// SetDVR validates and persists the DVR config.
func (s *Service) SetDVR(ctx context.Context, cfg DVRConfig) error {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	return s.setJSON(ctx, keyDVRConfig, cfg)
}
