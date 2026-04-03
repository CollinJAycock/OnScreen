package transcode

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// EncoderEntry pairs an encoder with a human-friendly device label for the UI.
type EncoderEntry struct {
	Encoder Encoder `json:"encoder"`
	Label   string  `json:"label"`
}

// DetectEncoders probes available hardware encoders at startup (ADR-028).
// Returns encoders in priority order: NVENC → QSV → VAAPI → software.
// override: comma-separated encoder list (e.g. "software"), empty = auto-detect.
func DetectEncoders(ctx context.Context, override string) ([]Encoder, error) {
	if override != "" {
		return ParseOverride(override), nil
	}

	var available []Encoder

	// On Linux, check device files first as a fast pre-filter.
	// On Windows/macOS, skip device checks and probe FFmpeg directly.
	// WSL2/Docker Desktop exposes GPU via /dev/dxg instead of /dev/nvidia0.
	skipNVENC := runtime.GOOS == "linux" && !fileExists("/dev/nvidia0") && !fileExists("/dev/dxg")
	skipDRI := runtime.GOOS == "linux" && !fileExists("/dev/dri/renderD128")

	// NVENC (NVIDIA GPU — works on Linux and Windows).
	if !skipNVENC {
		if probeEncoder(ctx, "h264_nvenc") {
			available = append(available, EncoderNVENC)
		}
		if probeEncoder(ctx, "hevc_nvenc") {
			available = append(available, EncoderHEVCNVENC)
		}
	}

	// AMF (AMD GPU — Windows only).
	if runtime.GOOS == "windows" {
		if probeEncoder(ctx, "h264_amf") {
			available = append(available, EncoderAMF)
		}
	}

	// QSV / VAAPI (Intel — Linux only, requires DRI device).
	if !skipDRI {
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
	case "hevc_nvenc":
		args = []string{
			"-hide_banner", "-loglevel", "quiet",
			"-f", "lavfi", "-i", "nullsrc=s=1280x720",
			"-t", "1", "-c:v", "hevc_nvenc",
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
	case "h264_amf":
		args = []string{
			"-hide_banner", "-loglevel", "quiet",
			"-f", "lavfi", "-i", "nullsrc=s=1280x720",
			"-t", "1", "-c:v", "h264_amf",
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

// EncoderLabel returns a human-readable label for an encoder without probing hardware.
// Used when the server doesn't have the GPU but a worker reports the capability.
func EncoderLabel(enc Encoder) string {
	switch enc {
	case EncoderNVENC:
		return "NVIDIA GPU"
	case EncoderHEVCNVENC:
		return "NVIDIA GPU (HEVC)"
	case EncoderAMF:
		return "AMD GPU"
	case EncoderQSV:
		return "Intel Quick Sync"
	case EncoderVAAPI:
		return "VA-API"
	case EncoderSoftware:
		return "Software (CPU)"
	case EncoderHEVCSoftware:
		return "Software (CPU, HEVC)"
	default:
		return string(enc)
	}
}

// HasHEVCEncoder returns true if the encoder list contains a HEVC-capable encoder.
func HasHEVCEncoder(encoders []Encoder) bool {
	for _, e := range encoders {
		if e == EncoderHEVCNVENC || e == EncoderHEVCSoftware {
			return true
		}
	}
	return false
}

// BestHEVCEncoder returns the highest-priority HEVC encoder from the list,
// or empty string if none available.
func BestHEVCEncoder(encoders []Encoder) Encoder {
	for _, e := range encoders {
		if e == EncoderHEVCNVENC || e == EncoderHEVCSoftware {
			return e
		}
	}
	return ""
}

// detectGPUName tries to return a human-readable GPU name (e.g. "NVIDIA GeForce RTX 5080").
// Falls back to a generic label based on the encoder type.
func detectGPUName(ctx context.Context, enc Encoder) string {
	switch enc {
	case EncoderNVENC:
		out, err := exec.CommandContext(ctx, "nvidia-smi",
			"--query-gpu=name", "--format=csv,noheader").Output()
		if err == nil {
			name := strings.TrimSpace(strings.Split(string(out), "\n")[0])
			if name != "" {
				return name
			}
		}
		return "NVIDIA GPU"
	case EncoderAMF:
		out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command",
			"Get-CimInstance Win32_VideoController | Where-Object { $_.Name -match 'AMD|Radeon' } | Select-Object -First 1 -ExpandProperty Name").Output()
		if err == nil {
			name := strings.TrimSpace(string(out))
			if name != "" {
				return name
			}
		}
		return "AMD GPU"
	case EncoderQSV:
		return "Intel Quick Sync"
	case EncoderVAAPI:
		return "VA-API"
	case EncoderSoftware:
		return "Software (CPU)"
	default:
		return string(enc)
	}
}

// EncoderEntries returns encoder entries with human-readable device labels.
func EncoderEntries(ctx context.Context, encoders []Encoder) []EncoderEntry {
	entries := make([]EncoderEntry, len(encoders))
	for i, e := range encoders {
		entries[i] = EncoderEntry{
			Encoder: e,
			Label:   detectGPUName(ctx, e),
		}
	}
	return entries
}

// FilterAvailable removes encoders from wanted that were not actually detected.
// Always ensures at least EncoderSoftware is returned.
func FilterAvailable(wanted, detected []Encoder) []Encoder {
	avail := make(map[Encoder]bool, len(detected))
	for _, e := range detected {
		avail[e] = true
	}
	var result []Encoder
	for _, e := range wanted {
		if avail[e] {
			result = append(result, e)
		}
	}
	if len(result) == 0 {
		result = append(result, EncoderSoftware)
	}
	return result
}

// ProbeFilter returns true if the named FFmpeg filter is available.
func ProbeFilter(ctx context.Context, name string) bool {
	out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-filters").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		// Filter listing format: " T. name  V->V  description"
		if len(fields) >= 2 && fields[1] == name {
			return true
		}
	}
	return false
}

func ParseOverride(override string) []Encoder {
	var encoders []Encoder
	for _, s := range strings.Split(override, ",") {
		switch strings.TrimSpace(strings.ToLower(s)) {
		case "nvenc", "h264_nvenc":
			encoders = append(encoders, EncoderNVENC)
		case "hevc_nvenc":
			encoders = append(encoders, EncoderHEVCNVENC)
		case "amf", "h264_amf":
			encoders = append(encoders, EncoderAMF)
		case "vaapi", "h264_vaapi":
			encoders = append(encoders, EncoderVAAPI)
		case "qsv", "h264_qsv":
			encoders = append(encoders, EncoderQSV)
		case "software", "libx264":
			encoders = append(encoders, EncoderSoftware)
		case "hevc_software", "libx265":
			encoders = append(encoders, EncoderHEVCSoftware)
		}
	}
	if len(encoders) == 0 {
		encoders = append(encoders, EncoderSoftware)
	}
	return encoders
}
