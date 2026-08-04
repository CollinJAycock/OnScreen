package transcode

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// EncoderEntry pairs an encoder with a human-friendly device label for the UI.
type EncoderEntry struct {
	Encoder Encoder `json:"encoder"`
	Label   string  `json:"label"`
}

// DetectEncoders probes available hardware encoders at startup (ADR-028).
// Returns encoders in priority order: NVENC → AMF → VAAPI → QSV → software.
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

	// AMF (AMD GPU — Windows only). RDNA2+ adds HEVC; RDNA3+ (Radeon
	// 7000-series dGPU, Ryzen Phoenix 7040+ iGPU) adds AV1. The probe
	// is a 1-second null encode, so on hardware without the AV1 block
	// it fails fast and we just don't add EncoderAV1AMF.
	if runtime.GOOS == "windows" {
		if probeEncoder(ctx, "h264_amf") {
			available = append(available, EncoderAMF)
		}
		if probeEncoder(ctx, "hevc_amf") {
			available = append(available, EncoderHEVCAMF)
		}
		if probeEncoder(ctx, "av1_amf") {
			available = append(available, EncoderAV1AMF)
		}
	}

	// VAAPI / QSV (Intel + AMD on Linux — requires DRI device). Intel
	// Arc + 11th-gen+ iGPUs expose both encoder families through the
	// same iHD driver; AMD GPUs on Linux expose VAAPI via the Mesa
	// radeonsi driver. We probe both independently and add VAAPI to
	// the slice first so the default selection on Intel hardware
	// prefers `h264_vaapi` / `hevc_vaapi` / `av1_vaapi` over their
	// `*_qsv` siblings.
	//
	// VAAPI-first priority is the result of same-hardware benchmarks
	// on an Intel Arc A770 (2026-05-21): the native VAAPI path is
	// consistently 2–5× faster than QSV/libmfx on HDR sources for
	// first-segment delivery (Dune 1080p HDR 2.6 s vs 12.9 s,
	// Interstellar 6.2 s vs 15.5 s, GoodFellas 4K HDR 23.0 s vs
	// 34.2 s). VAAPI has a more direct pipeline to the iHD driver —
	// QSV adds a libmfx → VA-API shim that costs per-frame overhead.
	// Operators on Intel boxes who need QSV explicitly (libmfx-
	// specific features, vendor support contract) can pin it via
	// TRANSCODE_ENCODERS=h264_qsv,hevc_qsv,av1_qsv.
	if !skipDRI {
		if probeEncoder(ctx, "h264_vaapi") {
			available = append(available, EncoderVAAPI)
			if probeEncoder(ctx, "hevc_vaapi") {
				available = append(available, EncoderHEVCVAAPI)
			}
			if probeEncoder(ctx, "av1_vaapi") {
				available = append(available, EncoderAV1VAAPI)
			}
		}
		if probeEncoder(ctx, "h264_qsv") {
			available = append(available, EncoderQSV)
			if probeEncoder(ctx, "hevc_qsv") {
				available = append(available, EncoderHEVCQSV)
			}
			if probeEncoder(ctx, "av1_qsv") {
				available = append(available, EncoderAV1QSV)
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

// BestH264Encoder returns the highest-priority encoder that emits H.264 — the
// first list entry that is neither HEVC- nor AV1-family — falling back to the
// software encoder (libx264, always available) when the list has none, e.g. an
// operator override pinning hevc_nvenc alone. Used to keep the worker's
// default pick honest when the job requested plain H.264 output.
func BestH264Encoder(encoders []Encoder) Encoder {
	for _, e := range encoders {
		if !IsHEVCEncoder(e) && !IsAV1Encoder(e) {
			return e
		}
	}
	return EncoderSoftware
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
	case "av1_vaapi":
		args = []string{
			"-hide_banner", "-loglevel", "quiet",
			"-vaapi_device", "/dev/dri/renderD128",
			"-f", "lavfi", "-i", "nullsrc=s=1280x720",
			"-t", "1",
			"-vf", "format=nv12,hwupload",
			"-c:v", "av1_vaapi",
			"-f", "null", "-",
		}
	case "av1_amf":
		args = []string{
			"-hide_banner", "-loglevel", "quiet",
			"-f", "lavfi", "-i", "nullsrc=s=1280x720",
			"-t", "1", "-c:v", "av1_amf",
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
	case EncoderAMF, EncoderHEVCAMF, EncoderAV1AMF:
		return "AMD GPU"
	case EncoderQSV, EncoderHEVCQSV, EncoderAV1QSV:
		return "Intel Quick Sync"
	case EncoderVAAPI, EncoderHEVCVAAPI, EncoderAV1VAAPI:
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
		if IsHEVCEncoder(e) {
			return true
		}
	}
	return false
}

// BestHEVCEncoder returns the highest-priority HEVC encoder from the list,
// or empty string if none available. The input list is already in priority
// order from DetectEncoders (or operator-specified order from a TRANSCODE_ENCODERS
// override), so the first HEVC variant we hit is the right pick.
func BestHEVCEncoder(encoders []Encoder) Encoder {
	for _, e := range encoders {
		if IsHEVCEncoder(e) {
			return e
		}
	}
	return ""
}

// HasAV1Encoder returns true if the encoder list contains an AV1-capable encoder.
func HasAV1Encoder(encoders []Encoder) bool {
	for _, e := range encoders {
		if IsAV1Encoder(e) {
			return true
		}
	}
	return false
}

// BestAV1Encoder returns the highest-priority AV1 encoder from the list,
// or empty string if none available. Same priority-order semantics as
// BestHEVCEncoder — first hit wins.
func BestAV1Encoder(encoders []Encoder) Encoder {
	for _, e := range encoders {
		if IsAV1Encoder(e) {
			return e
		}
	}
	return ""
}

// vendorClass returns a coarse hardware-vendor/device class for an encoder, used
// by FallbackEncoders to avoid "failing over" to the same physical GPU that just
// rejected the job. NVENC/QSV/VAAPI/AMF map to their vendor; everything else
// (the software encoders) is "software".
func vendorClass(enc Encoder) string {
	switch {
	case IsNVENCEncoder(enc):
		return "nvenc"
	case IsQSVEncoder(enc):
		return "qsv"
	case IsVAAPIEncoder(enc):
		return "vaapi"
	case enc == EncoderAMF || enc == EncoderHEVCAMF || enc == EncoderAV1AMF:
		return "amf"
	default:
		return "software"
	}
}

// IsHardwareEncoder reports whether enc runs on a GPU (NVENC/QSV/VAAPI/AMF) as
// opposed to a CPU (software) encoder. A stream-copy ("copy") is not an encoder
// and returns false.
func IsHardwareEncoder(enc Encoder) bool {
	return vendorClass(enc) != "software" && enc != "copy"
}

// codecFamily returns the output codec family ("h264", "hevc", "av1") an encoder
// produces. FallbackEncoders keeps fail-over within the same family so a session
// already stamped H.264 (.ts segments) doesn't switch to HEVC/AV1 (.m4s) mid-stream.
func codecFamily(enc Encoder) string {
	switch {
	case IsAV1Encoder(enc):
		return "av1"
	case IsHEVCEncoder(enc):
		return "hevc"
	default:
		return "h264"
	}
}

// softwareEncoderForFamily returns the CPU encoder that produces the given output
// codec family — the always-runnable last-resort fail-over target.
func softwareEncoderForFamily(family string) Encoder {
	switch family {
	case "av1":
		return EncoderAV1Software
	case "hevc":
		return EncoderHEVCSoftware
	default:
		return EncoderSoftware
	}
}

// FallbackEncoders returns the ordered list of alternate encoders to try when a
// hardware ENCODER can't start a job — most often a GeForce NVENC worker that hit
// the driver's 8-concurrent-session cap (the consumer-card limit), but also a
// QSV/VAAPI device momentarily out of surfaces. Candidates are drawn from this
// worker's detected `available` encoders (so fail-over only happens "if
// configured" — i.e. the box actually has a second provider), kept to the SAME
// output codec family as `primary`, and restricted to a DIFFERENT hardware vendor
// (retrying the same saturated GPU is pointless). The matching software encoder is
// appended as the always-runnable last resort.
//
// Example (laptop: RTX 4080 dGPU + Intel iGPU):
//
//	FallbackEncoders("h264_nvenc",
//	  [h264_nvenc, hevc_nvenc, av1_nvenc, h264_qsv, hevc_qsv, libx264])
//	  → [h264_qsv, libx264]
//
// A software or stream-copy primary returns nil — there's nothing to fail over
// from (the CPU encoder can't run out of GPU sessions).
func FallbackEncoders(primary Encoder, available []Encoder) []Encoder {
	if !IsHardwareEncoder(primary) {
		return nil
	}
	fam := codecFamily(primary)
	pv := vendorClass(primary)
	seen := map[Encoder]bool{primary: true}
	var out []Encoder
	for _, e := range available {
		if seen[e] || codecFamily(e) != fam || !IsHardwareEncoder(e) || vendorClass(e) == pv {
			continue
		}
		out = append(out, e)
		seen[e] = true
	}
	if sw := softwareEncoderForFamily(fam); !seen[sw] {
		out = append(out, sw)
	}
	return out
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
// lifetime; surfaced in the worker's startup log.
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

// ProbeEncoder returns true if the named FFmpeg encoder is available. Used to
// prefer libfdk_aac over the native aac encoder, whose multichannel startup is
// dramatically slower (a 7.1 TrueHD source's first HLS segment: ~15s native aac
// vs ~5s libfdk_aac — the native encoder dominated time-to-first-segment).
func ProbeEncoder(ctx context.Context, name string) bool {
	out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-encoders").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		// Encoder listing format: " A..... name  description"
		if len(fields) >= 2 && fields[1] == name {
			return true
		}
	}
	return false
}

// ProbeReadrateCatchup reports whether this FFmpeg build recognizes the
// -readrate_catchup option (added in 2025). With -readrate capping input
// reading at real time, an encoder that briefly falls behind (a GPU hiccup, a
// momentarily blocked output) can never rebuild its lead — it stays pinned at
// the live edge, so the next client stall has no buffer to ride out.
// -readrate_catchup lets ffmpeg read faster than the base rate until it has
// caught back up, then settle to real time. Older builds abort at parse time
// with "Option readrate_catchup not found", which would kill EVERY transcode,
// so the worker only adds the flag when this probe passes. The probe is a
// no-op run: ffmpeg validates option names before doing any real work.
func ProbeReadrateCatchup(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-readrate", "1.0", "-readrate_catchup", "2.0",
		"-f", "lavfi", "-i", "color=c=black:s=64x64", "-t", "0.1", "-f", "null", "-")
	return cmd.Run() == nil
}

// ProbeLibplaceboVulkan reports whether GPU HDR→SDR tonemapping via the
// libplacebo (Vulkan) filter actually works on this host — not just that the
// filter is compiled in, but that a Vulkan device initializes and a libplacebo
// pass runs. It's the preferred tonemap path: vendor-agnostic (NVIDIA / Intel /
// AMD all expose Vulkan), GPU-resident, and far faster than software zscale on
// 4K HDR. A real run is required because the filter can be present while Vulkan
// has no driver/ICD (common on a headless Linux box), in which case we must
// fall back to software zscale rather than emit a pipeline that fails at
// runtime.
func ProbeLibplaceboVulkan(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	default:
	}
	// Tonemap a 1-frame synthetic HDR (bt2020/PQ) source through libplacebo on a
	// Vulkan device. Succeeds only if -init_hw_device vulkan brings up a device
	// and the filter runs end to end.
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-init_hw_device", "vulkan",
		"-f", "lavfi",
		"-i", "color=c=black:s=256x256:r=1,format=yuv420p10le,setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc",
		"-frames:v", "1",
		"-vf", "libplacebo=w=128:h=128:tonemapping=bt.2390:colorspace=bt709:color_primaries=bt709:color_trc=bt709:range=tv:format=yuv420p,hwdownload,format=yuv420p",
		"-f", "null", "-",
	)
	return cmd.Run() == nil
}

// ProbeCudaHevcDecode reports whether NVDEC HEVC decode (hevc_cuvid) actually
// works end-to-end on this host. The transcode pipeline software-decodes HEVC
// by default because mainline ffmpeg + some NVIDIA drivers fail NVDEC HEVC on
// certain inputs ("No decoder surfaces left", EINVAL); it switches to NVDEC
// only when this probe passes. Decoded frames are kept in system memory (no
// -hwaccel_output_format cuda), matching how BuildHLS uses it — so this offloads
// only the (4K-bound) decode while the existing scale/tonemap/encode chain is
// unchanged. A real round-trip is required: encode a tiny HEVC clip with NVENC
// (which also confirms the GPU is usable), then decode it back via NVDEC.
func ProbeCudaHevcDecode(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	default:
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tmp, err := os.CreateTemp("", "nvdec-probe-*.hevc")
	if err != nil {
		return false
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	// 1) tiny HEVC clip via NVENC (confirms the NVIDIA GPU encode path too).
	enc := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=256x256:r=10:d=1",
		"-c:v", "hevc_nvenc", "-frames:v", "10", tmp.Name())
	if enc.Run() != nil {
		return false
	}
	// 2) decode it back on the GPU via NVDEC, output to system memory.
	dec := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-c:v", "hevc_cuvid", "-i", tmp.Name(), "-frames:v", "5", "-f", "null", "-")
	return dec.Run() == nil
}

// ProbeCudaHevcScale reports whether the full-VRAM HEVC pipeline — NVDEC decode
// straight into CUDA memory, scale_cuda downscale in VRAM, NVENC encode — works
// end-to-end on this host. This is the path that fixes 4K SDR transcodes (the
// 4K scale never touches the CPU), but it is the historically fragile mainline
// chain: the HEVC NVDEC auto-path fails to create the decoder on some
// ffmpeg/driver combos, and reference-heavy sources can exhaust the decoder
// surface pool. So the worker enables CudaVRAM only when this real probe
// passes, mirroring exactly what BuildHLS emits (explicit hevc_cuvid,
// extra_hw_frames, scale_cuda).
//
// Both bit depths are exercised — 8-bit (the everyday nv12→nv12 case) and
// Main10 (the fragile P010→nv12 conversion) — and the probe VALIDATES THE
// PIXELS, not just the exit code: the chain can "succeed" while handing the
// encoder surfaces with zeroed chroma, encoding every frame solid green
// (observed on a Turing RTX 5000 whose exit-code-only probe passed). The
// output is written to a real file and decoded back through signalstats; dead
// chroma fails the probe.
func ProbeCudaHevcScale(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	default:
	}
	// Generous timeout: on a cold CUDA JIT cache the driver compiles the
	// scale_cuda kernels for this GPU arch on first use, which is CPU-bound and
	// can take ~90s (measured ~84s on a Turing RTX 5000). The worker runs this
	// in the background so the wait doesn't block startup, and CUDA_CACHE_PATH
	// (persistent volume) means it's a one-time cost — warm runs return in ~2s.
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	// runVRAMChain encodes a tiny HEVC clip at the given bit depth, pushes it
	// through the exact chain BuildHLS emits, and checks the surviving pixels.
	runVRAMChain := func(main10 bool) bool {
		src, err := os.CreateTemp("", "nvscale-probe-src-*.hevc")
		if err != nil {
			return false
		}
		src.Close()
		defer os.Remove(src.Name())
		out, err := os.CreateTemp("", "nvscale-probe-out-*.hevc")
		if err != nil {
			return false
		}
		out.Close()
		defer os.Remove(out.Name())

		encArgs := []string{"-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc2=s=640x480:r=10:d=1"}
		if main10 {
			encArgs = append(encArgs, "-vf", "format=p010le", "-c:v", "hevc_nvenc", "-profile:v", "main10")
		} else {
			encArgs = append(encArgs, "-c:v", "hevc_nvenc")
		}
		encArgs = append(encArgs, "-frames:v", "10", src.Name())
		if exec.CommandContext(ctx, "ffmpeg", encArgs...).Run() != nil {
			return false
		}
		// Full VRAM chain: cuvid decode -> scale_cuda -> NVENC, as BuildHLS
		// emits — written to a real file so the result can be inspected.
		dec := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
			"-hwaccel", "cuda", "-hwaccel_output_format", "cuda", "-extra_hw_frames", "8",
			"-c:v", "hevc_cuvid", "-i", src.Name(),
			"-vf", "scale_cuda=w=320:h=240:format=nv12",
			"-c:v", "hevc_nvenc", "-frames:v", "5", "-f", "hevc", out.Name())
		if dec.Run() != nil {
			return false
		}
		// testsrc2 is saturated with color; dead chroma here means the chain
		// corrupts surfaces and must stay disabled.
		return outputChromaAlive(ctx, out.Name())
	}

	return runVRAMChain(false) && runVRAMChain(true)
}

// ProbeTonemapCuda reports whether the all-VRAM CUDA HDR→SDR pipeline works
// end-to-end: NVDEC decode into CUDA memory, scale_cuda downscale, tonemap_cuda
// HDR→SDR, NVENC — all in VRAM, no round-trips. tonemap_cuda is our own
// curated patch (lifted from jellyfin-ffmpeg onto mainline 8.1.1); it's the
// preferred HDR path on NVIDIA because it needs only CUDA, not Vulkan (so it
// works on TrueNAS where libplacebo can't init). A REAL round-trip is required
// because the filter's CUDA kernels can fault on archs jellyfin hasn't validated
// (observed CUDA_ERROR_ILLEGAL_ADDRESS on Blackwell/sm_120) — there the probe
// fails and the worker falls back to scale_cuda+zscale or software. Same cold-
// JIT cost as ProbeCudaHevcScale, so the worker runs it in the background.
func ProbeTonemapCuda(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	default:
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	tmp, err := os.CreateTemp("", "nvtonemap-probe-*.hevc")
	if err != nil {
		return false
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	// 1) tiny 10-bit HDR10 (PQ/bt2020) clip to exercise the tonemap.
	enc := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=640x480:r=10:d=1",
		"-vf", "format=p010le,setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc",
		"-c:v", "hevc_nvenc", "-profile:v", "main10", "-frames:v", "10", tmp.Name())
	if enc.Run() != nil {
		return false
	}
	// 2) all-VRAM chain: cuvid -> scale_cuda -> tonemap_cuda -> NVENC, as BuildHLS emits.
	dec := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-hwaccel", "cuda", "-hwaccel_output_format", "cuda", "-extra_hw_frames", "8",
		"-c:v", "hevc_cuvid", "-i", tmp.Name(),
		"-vf", "scale_cuda=w=320:h=240,tonemap_cuda=tonemap=bt2390:t=bt709:m=bt709:p=bt709:r=tv:format=nv12",
		"-c:v", "hevc_nvenc", "-frames:v", "5", "-f", "null", "-")
	return dec.Run() == nil
}

// ProbeTonemapVAAPI reports whether the all-VRAM VAAPI HDR→SDR chain works end to
// end on this host: VAAPI decode into VA surfaces, scale_vaapi downscale, tonemap_vaapi
// HDR→SDR, VAAPI encode — all in VRAM, no CPU zscale. tonemap_vaapi's HDR-metadata
// handling varies by iHD/Mesa driver version, so the worker enables the VRAM HDR path
// only when this real round-trip passes; otherwise it keeps the software zscale tonemap.
// A genuine HDR10 source is required, and crucially tonemap_vaapi refuses input
// without SMPTE-2086 mastering-display metadata ("No mastering display data from
// input"). Real HDR10 movies always carry it, but a synthetic source doesn't — so
// the probe builds the test clip with libx265 + master-display/max-cll params, which
// writes the metadata tonemap_vaapi needs. (HLG or metadata-less HDR sources fail
// tonemap_vaapi at runtime and fall back to the CPU zscale path per-job.)
func ProbeTonemapVAAPI(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	default:
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tmp, err := os.CreateTemp("", "vatonemap-probe-*.hevc")
	if err != nil {
		return false
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	// 1) tiny HDR10 (PQ/bt2020) clip WITH mastering-display + content-light metadata
	// (libx265 -x265-params) — tonemap_vaapi requires that metadata on the input.
	enc := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=640x480:r=10:d=1",
		"-vf", "format=p010le",
		"-c:v", "libx265",
		"-x265-params", "hdr-opt=1:repeat-headers=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(40000000,50):max-cll=1000,400",
		"-frames:v", "10", tmp.Name())
	if enc.Run() != nil {
		return false
	}
	// 2) all-VRAM chain: vaapi decode -> scale_vaapi -> tonemap_vaapi -> vaapi encode, as BuildHLS emits.
	dec := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-vaapi_device", "/dev/dri/renderD128",
		"-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi", "-i", tmp.Name(),
		"-vf", "scale_vaapi=w=320:h=240,tonemap_vaapi=format=nv12:matrix=bt709:primaries=bt709:transfer=bt709",
		"-c:v", "h264_vaapi", "-frames:v", "5", "-f", "null", "-")
	return dec.Run() == nil
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
		case "av1_amf":
			encoders = append(encoders, EncoderAV1AMF)
		case "vaapi", "h264_vaapi":
			encoders = append(encoders, EncoderVAAPI)
		case "hevc_vaapi":
			encoders = append(encoders, EncoderHEVCVAAPI)
		case "av1_vaapi":
			encoders = append(encoders, EncoderAV1VAAPI)
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
