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

	// NVENC (NVIDIA GPU — works on Linux and Windows). Probe each
	// codec the GPU might support; AV1 only lights up on Ada (40-series)
	// and Blackwell (50-series) cards but the probe is cheap and the
	// answer caches at startup.
	if !skipNVENC {
		if probeEncoder(ctx, "h264_nvenc") {
			available = append(available, EncoderNVENC)
		}
		if probeEncoder(ctx, "hevc_nvenc") {
			available = append(available, EncoderHEVCNVENC)
		}
		if probeEncoder(ctx, "av1_nvenc") {
			available = append(available, EncoderAV1NVENC)
		}
	}

	// AMF (AMD GPU — Windows only). RDNA2+ adds HEVC. AV1 AMF
	// (RDNA3+) needs an EncoderAV1AMF constant that doesn't exist
	// yet; deferred until AMD-AV1 hardware is on the test bench.
	if runtime.GOOS == "windows" {
		if probeEncoder(ctx, "h264_amf") {
			available = append(available, EncoderAMF)
		}
		if probeEncoder(ctx, "hevc_amf") {
			available = append(available, EncoderHEVCAMF)
		}
	}

	// QSV / VAAPI (Intel — Linux only, requires DRI device). Intel Arc
	// + 11th-gen+ iGPUs do AV1 encode via QSV.
	if !skipDRI {
		if probeEncoder(ctx, "h264_qsv") {
			available = append(available, EncoderQSV)
			if probeEncoder(ctx, "hevc_qsv") {
				available = append(available, EncoderHEVCQSV)
			}
			if probeEncoder(ctx, "av1_qsv") {
				available = append(available, EncoderAV1QSV)
			}
		} else if probeEncoder(ctx, "h264_vaapi") {
			available = append(available, EncoderVAAPI)
			if probeEncoder(ctx, "hevc_vaapi") {
				available = append(available, EncoderHEVCVAAPI)
			}
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
	case "av1_nvenc":
		args = []string{
			"-hide_banner", "-loglevel", "quiet",
			"-f", "lavfi", "-i", "nullsrc=s=1280x720",
			"-t", "1", "-c:v", "av1_nvenc",
			"-f", "null", "-",
		}
	case "hevc_amf":
		args = []string{
			"-hide_banner", "-loglevel", "quiet",
			"-f", "lavfi", "-i", "nullsrc=s=1280x720",
			"-t", "1", "-c:v", "hevc_amf",
			"-f", "null", "-",
		}
	case "hevc_qsv":
		args = []string{
			"-hide_banner", "-loglevel", "quiet",
			"-f", "lavfi", "-i", "nullsrc=s=1280x720",
			"-t", "1", "-c:v", "hevc_qsv",
			"-f", "null", "-",
		}
	case "av1_qsv":
		args = []string{
			"-hide_banner", "-loglevel", "quiet",
			"-f", "lavfi", "-i", "nullsrc=s=1280x720",
			"-t", "1", "-c:v", "av1_qsv",
			"-f", "null", "-",
		}
	case "hevc_vaapi":
		args = []string{
			"-hide_banner", "-loglevel", "quiet",
			"-vaapi_device", "/dev/dri/renderD128",
			"-f", "lavfi", "-i", "nullsrc=s=1280x720",
			"-t", "1",
			"-vf", "format=nv12,hwupload",
			"-c:v", "hevc_vaapi",
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
	case EncoderNVENC, EncoderHEVCNVENC, EncoderAV1NVENC:
		return "NVIDIA GPU"
	case EncoderAMF, EncoderHEVCAMF:
		return "AMD GPU"
	case EncoderQSV, EncoderHEVCQSV, EncoderAV1QSV:
		return "Intel Quick Sync"
	case EncoderVAAPI, EncoderHEVCVAAPI:
		return "VA-API"
	case EncoderSoftware, EncoderHEVCSoftware, EncoderAV1Software:
		return "Software (CPU)"
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
	case EncoderNVENC, EncoderHEVCNVENC, EncoderAV1NVENC:
		// All NVENC codec variants run on the same physical NVIDIA
		// GPU — the UI groups them by label, so we return the same
		// nvidia-smi output for all three. Picking the device in the
		// dropdown enables every codec the GPU supports.
		out, err := exec.CommandContext(ctx, "nvidia-smi",
			"--query-gpu=name", "--format=csv,noheader").Output()
		if err == nil {
			name := strings.TrimSpace(strings.Split(string(out), "\n")[0])
			if name != "" {
				return name
			}
		}
		return "NVIDIA GPU"
	case EncoderAMF, EncoderHEVCAMF:
		out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command",
			"Get-CimInstance Win32_VideoController | Where-Object { $_.Name -match 'AMD|Radeon' } | Select-Object -First 1 -ExpandProperty Name").Output()
		if err == nil {
			name := strings.TrimSpace(string(out))
			if name != "" {
				return name
			}
		}
		return "AMD GPU"
	case EncoderQSV, EncoderHEVCQSV, EncoderAV1QSV:
		return "Intel Quick Sync"
	case EncoderVAAPI, EncoderHEVCVAAPI:
		return "VA-API"
	case EncoderSoftware, EncoderHEVCSoftware, EncoderAV1Software:
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

// OpenCLDevice represents a single (platform_index, device_index) pair
// returned by ffmpeg's `-init_hw_device opencl=list`. The Name fields
// are the human-readable strings ffmpeg prints — we match against them
// to pick the platform whose vendor lines up with the encoder we're
// going to use (NVIDIA-named platform for NVENC, Intel for QSV, AMD
// for AMF). Without this, the bare `opencl=ocl` device init fails
// (-19) on any host with more than one OpenCL platform visible.
type OpenCLDevice struct {
	Index        string // "N.M" suitable for `opencl=ocl:N.M`
	PlatformName string
	DeviceName   string
}

// ListOpenCLDevices runs ffmpeg's OpenCL platform listing and parses
// the output. The listing is probed once per worker startup; the
// process is fast (<100 ms) and the result is cached for the worker's
// lifetime via PickOpenCLDevice.
//
// Output shape (one of many examples):
//
//	[OpenCL @ ...] 2 OpenCL platforms found.
//	[OpenCL @ ...] 1 OpenCL devices found on platform "NVIDIA CUDA".
//	[OpenCL @ ...] 0.0: NVIDIA CUDA / NVIDIA GeForce RTX 5080
//	[OpenCL @ ...] 1 OpenCL devices found on platform "AMD ...".
//	[OpenCL @ ...] 1.0: AMD Accelerated Parallel Processing / gfx1036
func ListOpenCLDevices(ctx context.Context) []OpenCLDevice {
	out, err := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-v", "verbose",
		"-init_hw_device", "opencl=list",
	).CombinedOutput()
	if err != nil && len(out) == 0 {
		return nil
	}
	var devices []OpenCLDevice
	for _, line := range strings.Split(string(out), "\n") {
		// The platform/device-pair lines look like:
		//   [OpenCL @ ...] 0.0: NVIDIA CUDA / NVIDIA GeForce RTX 5080
		idx := strings.Index(line, "] ")
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(line[idx+2:])
		colon := strings.Index(rest, ": ")
		if colon < 0 {
			continue
		}
		head := rest[:colon]
		// head looks like "0.0" — verify shape so the "5 OpenCL
		// platforms found" header line doesn't false-match.
		if !strings.Contains(head, ".") {
			continue
		}
		dot := strings.Index(head, ".")
		if dot <= 0 || dot == len(head)-1 {
			continue
		}
		body := rest[colon+2:]
		var platform, device string
		if slash := strings.Index(body, " / "); slash >= 0 {
			platform = strings.TrimSpace(body[:slash])
			device = strings.TrimSpace(body[slash+3:])
		} else {
			platform = strings.TrimSpace(body)
		}
		devices = append(devices, OpenCLDevice{
			Index:        head,
			PlatformName: platform,
			DeviceName:   device,
		})
	}
	return devices
}

// PickOpenCLDevice returns the platform.device index whose vendor
// matches the active encoder. Falls back to "0.0" when no device is
// found or no good match exists — works on single-vendor hosts where
// 0.0 is by definition correct, and is the safest default elsewhere.
//
// Returns the index alone (e.g. "0.0") so callers can drop it into
// `opencl=ocl:N.M` directly.
func PickOpenCLDevice(devices []OpenCLDevice, enc Encoder) string {
	if len(devices) == 0 {
		return "0.0"
	}
	// Vendor keyword to look for in the OpenCL platform name. Picked
	// to match the strings ffmpeg actually prints on Windows + Linux
	// builds. NVIDIA's CUDA-OpenCL ICD reports as "NVIDIA CUDA";
	// AMD APP reports as "AMD Accelerated Parallel Processing";
	// Intel reports as "Intel(R) OpenCL" (or "Intel(R) OpenCL HD
	// Graphics" on iGPUs).
	var vendor string
	switch enc {
	case EncoderNVENC, EncoderHEVCNVENC, EncoderAV1NVENC:
		vendor = "nvidia"
	case EncoderAMF, EncoderHEVCAMF:
		vendor = "amd"
	case EncoderQSV, EncoderHEVCQSV, EncoderAV1QSV, EncoderVAAPI, EncoderHEVCVAAPI:
		vendor = "intel"
	}
	if vendor == "" {
		return devices[0].Index
	}
	for _, d := range devices {
		hay := strings.ToLower(d.PlatformName + " " + d.DeviceName)
		if strings.Contains(hay, vendor) {
			return d.Index
		}
	}
	// No vendor match — fall through to first device, which is the
	// platform ffmpeg would have picked anyway if there were only one.
	return devices[0].Index
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
		case "av1_nvenc":
			encoders = append(encoders, EncoderAV1NVENC)
		case "amf", "h264_amf":
			encoders = append(encoders, EncoderAMF)
		case "hevc_amf":
			encoders = append(encoders, EncoderHEVCAMF)
		case "vaapi", "h264_vaapi":
			encoders = append(encoders, EncoderVAAPI)
		case "hevc_vaapi":
			encoders = append(encoders, EncoderHEVCVAAPI)
		case "qsv", "h264_qsv":
			encoders = append(encoders, EncoderQSV)
		case "hevc_qsv":
			encoders = append(encoders, EncoderHEVCQSV)
		case "av1_qsv":
			encoders = append(encoders, EncoderAV1QSV)
		case "software", "libx264":
			encoders = append(encoders, EncoderSoftware)
		case "hevc_software", "libx265":
			encoders = append(encoders, EncoderHEVCSoftware)
		case "av1_software", "libsvtav1":
			encoders = append(encoders, EncoderAV1Software)
		}
	}
	if len(encoders) == 0 {
		encoders = append(encoders, EncoderSoftware)
	}
	return encoders
}
