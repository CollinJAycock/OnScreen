package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// Encoder tuning saved in Settings ▸ Transcode used to be stored and never
// read: the embedded worker and download runner kept the env values. It is
// merged at startup like the output ceilings.
func TestApplyTranscodeConfig_EncoderTuning(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{
		TranscodeNVENCPreset:  "p4",
		TranscodeNVENCTune:    "hq",
		TranscodeNVENCRC:      "vbr",
		TranscodeMaxrateRatio: 1.5,
	}

	// Nothing stored: the env values stand.
	applyTranscodeConfig(cfg, settings.TranscodeConfig{}, logger)
	if cfg.TranscodeNVENCPreset != "p4" || cfg.TranscodeNVENCTune != "hq" ||
		cfg.TranscodeNVENCRC != "vbr" || cfg.TranscodeMaxrateRatio != 1.5 {
		t.Fatalf("empty TranscodeConfig changed the env values: %+v", cfg)
	}

	applyTranscodeConfig(cfg, settings.TranscodeConfig{
		NVENCPreset: "p7", NVENCTune: "ll", NVENCRC: "cbr", MaxrateRatio: 2,
	}, logger)
	if cfg.TranscodeNVENCPreset != "p7" || cfg.TranscodeNVENCTune != "ll" ||
		cfg.TranscodeNVENCRC != "cbr" || cfg.TranscodeMaxrateRatio != 2 {
		t.Errorf("stored encoder tuning not applied: %+v", cfg)
	}

	eff := transcodeEffective(cfg)
	if eff.NVENCPreset != "p7" || eff.NVENCTune != "ll" || eff.NVENCRC != "cbr" || eff.MaxrateRatio != 2 {
		t.Errorf("transcodeEffective doesn't report the running tuning: %+v", eff)
	}
}
