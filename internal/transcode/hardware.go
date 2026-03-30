package transcode

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// DetectEncoders probes available hardware encoders at startup (ADR-028).
// Returns encoders in priority order: NVENC → QSV → VAAPI → software.
// override: comma-separated encoder list (e.g. "software"), empty = auto-detect.
func DetectEncoders(ctx context.Context, override string) ([]Encoder, error) {
	if override != "" {
		return parseOverride(override), nil
	}

	var available []Encoder

	// Phase 1: device file check (Linux-only device paths).
	hasNVIDIA := runtime.GOOS == "linux" && fileExists("/dev/nvidia0")
	hasDRI := runtime.GOOS == "linux" && fileExists("/dev/dri/renderD128")

	// Phase 2: FFmpeg null encode to verify the encoder actually works.
	if hasNVIDIA {
		if probeEncoder(ctx, "h264_nvenc") {
			available = append(available, EncoderNVENC)
		}
	}

	if hasDRI {
		if probeEncoder(ctx, "h264_qsv") {
			available = append(available, EncoderQSV)
		} else if probeEncoder(ctx, "h264_vaapi") {
			available = append(available, EncoderVAAPI)
		}
	}

	// Software is always available as the fallback.
	available = append(available, EncoderSoftware)
	return available, nil
}

// BestEncoder returns the highest-priority available encoder.
func BestEncoder(encoders []Encoder) Encoder {
	if len(encoders) == 0 {
		return EncoderSoftware
	}
	return encoders[0]
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// probeEncoder runs a 1-second null encode to verify the encoder is functional.
func probeEncoder(ctx context.Context, encoder string) bool {
	// Quick context-cancelled check.
	select {
	case <-ctx.Done():
		return false
	default:
	}

	var args []string
	switch encoder {
	case "h264_nvenc":
		args = []string{
			"-hide_banner", "-loglevel", "quiet",
			"-f", "lavfi", "-i", "nullsrc=s=1280x720",
			"-t", "1", "-c:v", "h264_nvenc",
			"-f", "null", "-",
		}
	case "h264_vaapi":
		args = []string{
			"-hide_banner", "-loglevel", "quiet",
			"-vaapi_device", "/dev/dri/renderD128",
			"-f", "lavfi", "-i", "nullsrc=s=1280x720",
			"-t", "1",
			"-vf", "format=nv12,hwupload",
			"-c:v", "h264_vaapi",
			"-f", "null", "-",
		}
	case "h264_qsv":
		args = []string{
			"-hide_banner", "-loglevel", "quiet",
			"-f", "lavfi", "-i", "nullsrc=s=1280x720",
			"-t", "1", "-c:v", "h264_qsv",
			"-f", "null", "-",
		}
	default:
		return false
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	return cmd.Run() == nil
}

func parseOverride(override string) []Encoder {
	var encoders []Encoder
	for _, s := range strings.Split(override, ",") {
		switch strings.TrimSpace(strings.ToLower(s)) {
		case "nvenc":
			encoders = append(encoders, EncoderNVENC)
		case "vaapi":
			encoders = append(encoders, EncoderVAAPI)
		case "qsv":
			encoders = append(encoders, EncoderQSV)
		case "software", "libx264":
			encoders = append(encoders, EncoderSoftware)
		}
	}
	if len(encoders) == 0 {
		encoders = append(encoders, EncoderSoftware)
	}
	return encoders
}
