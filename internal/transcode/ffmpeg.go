package transcode

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Encoder is a supported FFmpeg video encoder.
type Encoder string

const (
	EncoderNVENC    Encoder = "h264_nvenc"
	EncoderAMF      Encoder = "h264_amf"
	EncoderVAAPI    Encoder = "h264_vaapi"
	EncoderQSV      Encoder = "h264_qsv"
	EncoderSoftware Encoder = "libx264"

	// HEVC (H.265) output encoders — used for 4K to reduce bitrate ~40%.
	EncoderHEVCNVENC    Encoder = "hevc_nvenc"
	EncoderHEVCQSV      Encoder = "hevc_qsv"   // Intel Quick Sync HEVC (Skylake+)
	EncoderHEVCVAAPI    Encoder = "hevc_vaapi" // Linux generic GPU
	EncoderHEVCAMF      Encoder = "hevc_amf"   // AMD GPUs on Windows
	EncoderHEVCSoftware Encoder = "libx265"

	// AV1 output encoders — large-source archival use case where the
	// bitrate savings (~40% over HEVC) justify the encode cost.
	// SVT-AV1 is the only software encoder that's actually fast
	// enough for live transcode at 1080p; AOMENC stays out of the
	// list because it's ~10× slower in tests.
	EncoderAV1Software Encoder = "libsvtav1"
	EncoderAV1NVENC    Encoder = "av1_nvenc" // RTX 40-series only
	EncoderAV1QSV      Encoder = "av1_qsv"   // Intel ARC and 11th-gen+ iGPU
	EncoderAV1VAAPI    Encoder = "av1_vaapi" // Intel ARC / AMD RDNA3 via VA-API (native ffmpeg branch)
	EncoderAV1AMF      Encoder = "av1_amf"   // AMD RDNA3 dGPU on Windows (Ryzen iGPUs through Phoenix lack the AV1 block)
)

// EncoderOpts holds per-deployment encoder tuning knobs. Operators set these
// via environment variables to match their GPU model and upload bandwidth.
// All fields have sensible defaults; zero values are replaced at build time.
type EncoderOpts struct {
	NVENCPreset  string  // NVENC preset: "p1" (fastest) .. "p7" (best quality), default "p4"
	NVENCTune    string  // NVENC tune: "hq", "ll", "ull", default "hq"
	NVENCRC      string  // NVENC rate control: "vbr", "cbr", "constqp", default "vbr"
	MaxrateRatio float64 // maxrate = bitrate × ratio, default 1.5 (50% headroom)
}

// DefaultEncoderOpts returns the default encoder options.
func DefaultEncoderOpts() EncoderOpts {
	return EncoderOpts{
		NVENCPreset:  "p4",
		NVENCTune:    "hq",
		NVENCRC:      "vbr",
		MaxrateRatio: 1.5,
	}
}

// BuildArgs holds the inputs needed to construct an FFmpeg HLS transcode command.
type BuildArgs struct {
	// Input
	InputPath   string
	StartOffset float64 // seconds (seek to this position)

	// Video
	Encoder      Encoder
	Width        int
	Height       int
	BitrateKbps  int
	NeedsToneMap bool // HDR→SDR tone mapping (ADR-030)
	// CudaTonemap opts into the all-VRAM CUDA HDR→SDR path: NVDEC decode into
	// CUDA memory, scale_cuda downscale, tonemap_cuda HDR→SDR, NVENC — all in
	// VRAM, no round-trips. tonemap_cuda is our curated patch (jellyfin-ffmpeg's
	// filter on mainline 8.1.1); it needs only CUDA, not Vulkan, so it's the
	// preferred HDR path on NVIDIA hosts where libplacebo can't init (TrueNAS).
	// The worker sets it from a startup probe (NeedsToneMap + NVENC + HEVC/H.264)
	// and falls back to libplacebo / scale_cuda+zscale / software on failure.
	CudaTonemap bool
	HasZscale   bool // zscale filter available (libzimg) for software tonemap
	// ForceFMP4 pins the segment container to fMP4 regardless of which
	// encoder was actually selected — see Job.ForceFMP4. Set when the
	// client is already holding a playlist that promised .m4s.
	ForceFMP4 bool
	// OutputTSOffsetSec shifts output media timestamps so they start at this
	// content time instead of zero. `-ss` rebases the timeline, which is right
	// for a solo mid-stream start (the client maps via StartOffsetSec) but
	// wrong for an ABR rung child, whose segments are spliced into a predicted
	// full-timeline playlist: without the shift, a child restarted at segment N
	// emits segment N with segment 0's timestamps and the player lurches to
	// 0:00 or stalls on the splice. See Job.OutputTSOffsetSec.
	OutputTSOffsetSec float64
	// AudioOnly builds an audio-only HLS session: no video map, no encoder, no
	// filters, MPEG-TS segments. The pipeline used to hard-map `0:v:0`, which
	// killed ffmpeg instantly ("Stream map '0:v:0' matches no streams") for
	// every source with no video stream — the exact files (undeclared-container
	// music, .dsf/.ape rips, audiobooks) the decision engine deliberately
	// routes here.
	AudioOnly bool
	// NoAudio drops the audio map + audio args for sources with no audio
	// stream. The mirror image of AudioOnly: the unconditional `-map 0:a:0`
	// made every audio-less video (screen recordings, silent home video,
	// timelapses) die on the stream map the same way.
	NoAudio bool
	// Force8Bit pins the OUTPUT to 8-bit regardless of encoder family. The
	// H.264 encoders always strip to 8-bit (they reject 10-bit input), but the
	// HEVC/AV1 variants happily emit Main 10 — which is exactly wrong when the
	// bit-depth REDUCTION is why the client was sent to transcode at all: an
	// 8-bit-only HEVC client got a 10-bit HEVC "fix" it couldn't decode, audio
	// over a black screen. Set from the client's declared MaxVideoBitDepth.
	Force8Bit bool
	// HasLibfdkAAC selects the libfdk_aac encoder over the native aac encoder for
	// AAC output. libfdk is higher quality and — critically — far faster to start
	// on multichannel audio: a 7.1 TrueHD source's first HLS segment took ~15s
	// with native aac vs ~5s with libfdk (native aac's multichannel startup
	// dominated HDR-transcode time-to-first-segment). The worker sets it from a
	// startup encoder probe; falls back to native aac when libfdk isn't built in.
	HasLibfdkAAC bool
	// HasLibplacebo enables GPU HDR→SDR tonemap via the libplacebo (Vulkan)
	// filter — the preferred path: vendor-agnostic, GPU-resident, and far
	// faster than software zscale on 4K HDR (it also does the downscale). When
	// set, the tonemap runs on the GPU and the zscale chain is skipped.
	HasLibplacebo bool
	IsVAAPI       bool // VAAPI needs hwupload filter
	IsHEVC        bool // source is HEVC (informational, NVDEC auto-selects decoder)
	// QSVDecode opts into Intel QSV hardware HEVC decode (-hwaccel qsv -c:v
	// hevc_qsv) on the input, offloading the 4K HEVC decode from the CPU.
	// Only honored for HEVC sources on a re-encode; the worker sets it from
	// TRANSCODE_QSV_DECODE and falls back to software decode on failure.
	QSVDecode bool
	// QSVVRAM opts into the full-VRAM Intel QSV path for SDR HEVC/H.264/AV1
	// re-encodes on a QSV encoder: QSV decodes straight into QSV (VA) surfaces
	// (-hwaccel qsv -hwaccel_output_format qsv -c:v {hevc,h264,av1}_qsv), vpp_qsv
	// scales in GPU memory, and the QSV encoder reads those surfaces directly —
	// the Intel analogue of the NVDEC→scale_cuda→NVENC path. Requires ONE Intel
	// GPU to both decode and encode (the system-memory QSVDecode path stays for
	// the iGPU-decode/dGPU-encode split). SDR-only: HDR tonemap has no trusted
	// QSV filter, so it keeps the libplacebo/zscale path. Opt-in
	// (TRANSCODE_QSV_VRAM), default off, software fallback on failure — needs
	// validation on real Intel hardware before it's trusted.
	QSVVRAM bool
	// VAAPIVRAM opts into the full-VRAM VAAPI path for SDR re-encodes on a VAAPI
	// encoder: VAAPI hardware-decodes into VA surfaces (-hwaccel vaapi
	// -hwaccel_output_format vaapi), scale_vaapi runs in GPU memory, and the
	// VAAPI encoder reads those surfaces — no software decode, no hwupload (the
	// default VAAPI path software-decodes then uploads). Same single-GPU + SDR-
	// only constraints, opt-in (TRANSCODE_VAAPI_VRAM), default off, software
	// fallback — needs validation on real Intel/AMD hardware.
	VAAPIVRAM bool
	// VAAPITonemap opts into the all-VRAM VAAPI HDR→SDR path (the VAAPI analogue of
	// CudaTonemap): VAAPI hardware-decodes the HDR source into VA surfaces,
	// scale_vaapi downscales in GPU memory keeping the HDR signal, tonemap_vaapi
	// does HDR→SDR on the GPU, and the VAAPI encoder reads the result — all in VRAM,
	// no software zscale, no hwdownload. Replaces the CPU zscale tonemap that the
	// default VAAPI HDR path uses. SDR uses VAAPIVRAM; this is the HDR counterpart.
	// The worker sets it from a startup probe of the tonemap_vaapi chain (the iHD
	// driver's HDR-metadata handling varies, so it's probe-gated) and falls back to
	// the software zscale path on failure.
	VAAPITonemap bool
	// CudaHevcDecode opts into NVDEC hardware HEVC decode (-c:v hevc_cuvid) on
	// the input, offloading the (4K-bound) HEVC decode to the GPU while keeping
	// frames in system memory so the existing scale/tonemap/encode chain runs
	// unchanged — like QSVDecode, but for NVIDIA. Only honored for HEVC sources
	// on a re-encode; the worker sets it from a startup decode probe and falls
	// back to software decode on failure. Distinct from the AV1 full-VRAM
	// (-hwaccel_output_format cuda) path.
	CudaHevcDecode bool
	// CudaVRAM opts into the full-VRAM path for SDR HEVC/H.264 re-encodes: NVDEC
	// decodes straight into CUDA memory (-hwaccel cuda -hwaccel_output_format
	// cuda -c:v {hevc,h264}_cuvid) and scale_cuda downscales in VRAM, so the
	// 4K→1080p scale never touches the CPU and frames feed NVENC zero-copy. The
	// HEVC/H.264 analogue of the AV1 full-VRAM path. SDR-only: HDR re-encodes
	// need a tonemap, which on this build runs through libplacebo (Vulkan) and
	// so keeps the CudaHevcDecode (system-memory) path instead. The worker sets
	// it from a startup probe of the full cuvid→scale_cuda→NVENC chain (the
	// historically fragile mainline path) and falls back to software on failure.
	CudaVRAM bool
	// CudaHDRTonemap opts into the GPU-assisted HDR path for HEVC/H.264: NVDEC
	// decodes into CUDA memory, scale_cuda downscales in VRAM keeping 10-bit
	// (p010, so the HDR signal survives), then the frame is hwdownload'd and the
	// zscale tonemap runs on the now-small (e.g. 1080p) frame. This puts the
	// expensive 4K pixel work on the GPU while only the light tonemap touches the
	// CPU. It's the fallback for hosts where libplacebo (Vulkan) is unavailable —
	// notably TrueNAS, whose GPU passthrough doesn't inject the Vulkan stack. The
	// worker sets it (NeedsToneMap + cuda_scale + zscale + no libplacebo + NVENC);
	// libplacebo, when present, is still preferred and takes precedence.
	CudaHDRTonemap bool
	// IsAV1 marks an AV1 source. Required so video_copy remux switches
	// the HLS container to fMP4 + av01 tag — mpegts has no AV1 stream
	// type, so an `-c:v copy` into mpegts segments crashes the muxer
	// (Could not find tag for codec av1 in stream #0).
	IsAV1 bool
	// IsH264 marks an H.264 source. Used to pin the h264_cuvid decoder on the
	// CudaVRAM path; H.264 NVDEC is the most mature, so it shares the same
	// scale_cuda→NVENC machinery the HEVC probe validates.
	IsH264 bool

	// Audio (ADR-018)
	AudioCodec       string // "copy" | "aac"
	AudioChannels    int    // 0 = keep source
	AudioBitrateKbps int    // 0 = auto
	AudioStreamIndex int    // -1 = default (first); >= 0 = specific stream index

	// Subtitles
	//
	// INDEX CONVENTION (important): these use the RELATIVE subtitle-stream
	// index — i.e. the Nth subtitle stream in the file (0 = first subtitle
	// track), fed to ffmpeg as `0:s:N` and `subtitles=...:si=N`. This is a
	// DIFFERENT convention from the API's subtitle_streams[].index and the
	// /media/subtitles/{file}/{index} endpoint, which use the ABSOLUTE
	// ffprobe stream index (`0:%d`). If these fields are ever populated from
	// API-supplied indices (today nothing wires them — the on-demand
	// ServeSubtitle sidecar path delivers subtitles instead), convert
	// absolute→relative first or the wrong/no track will be selected.
	ExtractSubtitles bool
	SubtitleStreams  []int // relative subtitle-stream indices to extract as WebVTT

	// Encoder tuning
	EncoderOpts EncoderOpts

	// ReadRate paces ffmpeg's input-reading speed in source-time per
	// wall-time (1.0 = real-time, 1.5 = 50 % faster). Zero disables
	// the flag entirely. Production transcodes set 1.0 so ffmpeg
	// stays alive for the full playback duration; without pacing,
	// NVDEC + NVENC encodes a 24 min episode in ~65 s and the
	// worker's post-completion session-dir cleanup wipes segments
	// while the player is still consuming them. Integration tests
	// leave this zero so a multi-output `-t 8` test (HLS + WebVTT
	// extraction) doesn't wait real-time for the WebVTT context to
	// drain a 2.5 h subtitle stream.
	ReadRate             float64
	ReadRateInitialBurst int // seconds of input read at full speed before pacing kicks in
	// ReadrateCatchup is the catch-up read multiple used alongside -readrate:
	// when a transcode falls behind real time (a GPU hiccup, a blocked output)
	// ffmpeg may read up to this many times real time until it regains its lead,
	// then settles back to ReadRate. Zero omits the flag. Probe-gated by the
	// worker (ProbeReadrateCatchup) — pre-2025 ffmpeg aborts on the unknown
	// option, which would kill every transcode. Only emitted when > ReadRate.
	ReadrateCatchup float64

	// Output
	SessionDir    string // abs path, e.g. /tmp/onscreen/sessions/{id}
	SegmentPrefix string // relative prefix for .ts files, e.g. "seg"
	// StartNumber sets the HLS first-segment index (`-start_number`). Zero
	// uses ffmpeg's default (0). Used by the auto-continue path to resume
	// numbering past the segments an earlier ffmpeg run already wrote, so
	// the continuation's segment files don't collide with the prefix.
	StartNumber int
	// PlaylistName overrides the output playlist filename within SessionDir.
	// Empty defaults to "index.m3u8" (the file the API serves). The
	// auto-continue path writes to a side playlist (e.g. "cont.m3u8") that
	// the worker stitches onto index.m3u8 itself.
	PlaylistName string
}

// readrateCatchupRate returns the -readrate_catchup multiple to request when
// the ffmpeg build supports it (probe-gated), or 0 to omit the flag. 2x lets a
// transcode that briefly fell behind real time regain its lead quickly without
// racing to completion (the very thing -readrate is there to prevent).
func readrateCatchupRate(supported bool) float64 {
	if supported {
		return 2.0
	}
	return 0
}

// SegmentDuration is the HLS segment duration in seconds (ADR-007).
const SegmentDuration = 4

// isHTTPURL reports whether p is an http(s) URL (a remote HTTP source
// input) rather than a local filesystem path, so the input-side reconnect
// options are only added when they apply.
func isHTTPURL(p string) bool {
	return strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://")
}

// BuildHLS constructs the FFmpeg argv for an HLS transcode session.
// The caller is responsible for creating SessionDir before executing.
//
// When Encoder is "copy", video is stream-copied (no re-encode). This is
// ideal when the source video codec is already browser-compatible (H.264)
// but the audio or container needs transcoding.
func BuildHLS(a BuildArgs) []string {
	// Apply defaults for zero-value encoder opts.
	opts := a.EncoderOpts
	if opts.NVENCPreset == "" {
		opts.NVENCPreset = "p4"
	}
	if opts.NVENCTune == "" {
		opts.NVENCTune = "hq"
	}
	if opts.NVENCRC == "" {
		opts.NVENCRC = "vbr"
	}
	if opts.MaxrateRatio <= 0 {
		opts.MaxrateRatio = 1.5
	}

	videoCopy := IsVideoCopy(a.Encoder)

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
	}

	// Seek to start position (fast input seek for keyframe alignment).
	if a.StartOffset > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", a.StartOffset))
	}

	// Pace ffmpeg input so the encoder doesn't finish faster than the
	// player consumes. NVDEC + NVENC for AV1 hits 22× real-time on a
	// desktop GPU, which means a 24-minute episode finishes encoding
	// in ~65 seconds wall time. ffmpeg then exits cleanly and the
	// worker's post-completion session-dir cleanup (worker.go) wipes
	// the segment dir 30 s later — long before the player has consumed
	// past seg ~17. Player gets a 404 wall on the next segment fetch.
	//
	// ReadRate=1.0 keeps ffmpeg alive for the whole playback duration;
	// ReadRateInitialBurst=60 lets the first 60 s of source burst at
	// full speed for fast seg0 + a deep enough initial buffer that a
	// transient network dip (e.g. a Cloudflare-tunnel throughput sag)
	// drains the client cushion without stalling — the client refills
	// the produced-but-unfetched segments from disk at network speed,
	// not at the 1x encoder pace. Standard pattern Plex / Jellyfin both
	// use. Caller decides whether to enable — e.g.
	// integration tests using `-t 8` skip this so a multi-output run
	// doesn't wait real-time for ancillary contexts.
	if a.ReadRate > 0 {
		args = append(args, "-readrate", fmt.Sprintf("%.2f", a.ReadRate))
		if a.ReadRateInitialBurst > 0 {
			args = append(args, "-readrate_initial_burst", fmt.Sprint(a.ReadRateInitialBurst))
		}
		// Let ffmpeg read faster than real time to rebuild its lead after
		// falling behind, then settle back to ReadRate. Probe-gated by the
		// worker — pre-2025 builds don't have the option.
		if a.ReadrateCatchup > a.ReadRate {
			args = append(args, "-readrate_catchup", fmt.Sprintf("%.2f", a.ReadrateCatchup))
		}
	}

	// NVDEC for AV1 sources is a narrow exception to the
	// software-decode default. The mainline-ffmpeg + driver
	// fragility documented below is HEVC-specific (decoder-surfaces
	// pool exhaustion, 10-bit cuda-frame chain failures); AV1 NVDEC
	// has a cleaner path on RTX 30+ generations and survives mainline
	// ffmpeg 8.x without those failure modes.
	//
	// Why we *need* this for AV1: software AV1 decode (dav1d) runs
	// at ~3-5× real-time on 1080p on a fast desktop CPU — narrow
	// margin once you add NVENC encode in the same process.
	// In practice this surfaces as the encoder falling behind the
	// player after ~60-70 seconds of buffered content (segments
	// 0-13 burst-produced, then encoder stalls and player times
	// out at seg14). HEVC software decode runs at 17× real-time
	// for comparison — 4× the headroom AV1 has.
	//
	// HDR paths still skip cuda decode (HDR needs a tonemap, which on
	// this build runs through libplacebo/Vulkan from system memory — so
	// HDR HEVC uses the CudaHevcDecode system-memory path instead).
	// HEVC/H.264 join the full-VRAM path only when CudaVRAM is set (gated
	// on a startup probe of the fragile mainline cuvid→scale_cuda chain);
	// AV1 has always been reliable enough to take it unconditionally.
	useCUDADecode := IsNVENCEncoder(a.Encoder) && !a.NeedsToneMap &&
		(a.IsAV1 || ((a.IsHEVC || a.IsH264) && a.CudaVRAM))

	if !videoCopy && !a.AudioOnly {
		// VAAPI init filter (must come before input for hardware decode).
		if a.IsVAAPI {
			args = append(args, "-vaapi_device", "/dev/dri/renderD128")
			// Full-VRAM VAAPI: hardware-decode into VA surfaces so scale_vaapi +
			// the encoder run with no software decode and no hwupload. SDR uses
			// VAAPIVRAM; HDR uses VAAPITonemap (scale_vaapi → tonemap_vaapi, all in
			// VRAM). Either way the decode stays on VA surfaces; buildVideoFilter
			// skips the hwupload when this is set. (The default VAAPI path software-
			// decodes, CPU-tonemaps for HDR, then uploads.)
			if (a.VAAPIVRAM && !a.NeedsToneMap) || a.VAAPITonemap {
				args = append(args, "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi")
			}
		}

		if useCUDADecode || a.CudaHDRTonemap || a.CudaTonemap {
			// Decode to NVDEC and keep frames in VRAM. SDR (useCUDADecode):
			// scale_cuda→nv12 straight to the encoder. HDR via tonemap_cuda
			// (CudaTonemap): scale_cuda→tonemap_cuda, all VRAM. HDR fallback
			// (CudaHDRTonemap): scale_cuda→p010 then hwdownload + zscale. Either
			// way the decode lands in CUDA memory.
			args = append(args, "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
			// AV1 lets ffmpeg auto-select the decoder, but the HEVC NVDEC
			// auto-path fails to bring up the decoder on mainline ffmpeg 8.x
			// (cuvidCreateDecoder -> CUDA_ERROR_INVALID_VALUE, then a broken
			// fallback to software→cuda that scale_cuda can't consume). Pin the
			// dedicated cuvid decoder explicitly for HEVC/H.264, which works.
			// extra_hw_frames enlarges the decoder's output surface pool so
			// scale_cuda + NVENC holding references downstream don't exhaust it
			// ("No decoder surfaces left") on reference-heavy real-world sources.
			switch {
			case a.IsHEVC:
				args = append(args, "-extra_hw_frames", "8", "-c:v", "hevc_cuvid")
			case a.IsH264:
				args = append(args, "-extra_hw_frames", "8", "-c:v", "h264_cuvid")
			}
		}

		// QSV hardware HEVC decode (opt-in per worker via TRANSCODE_QSV_DECODE).
		// Offloads the 4K HEVC decode to the Intel iGPU. We deliberately DON'T
		// set -hwaccel_output_format qsv: decoded frames land back in system
		// memory, so the existing software scale/tonemap chain and the chosen
		// encoder (NVENC/AMF/software) run unchanged downstream — no cross-GPU
		// surface sharing (the worker's iGPU decodes, the dGPU encodes). Guarded
		// to HEVC sources on a re-encode; AV1 has its own CUDA carve-out above
		// and the two are mutually exclusive by source codec. Opt-in per worker
		// (TRANSCODE_QSV_DECODE); disable it for a worker if a source fails to
		// decode on QSV.
		if a.QSVDecode && a.IsHEVC && !a.QSVVRAM && !useCUDADecode && !a.CudaHDRTonemap && !a.CudaTonemap {
			args = append(args, "-hwaccel", "qsv", "-c:v", "hevc_qsv")
		}

		// Full-VRAM Intel QSV (opt-in TRANSCODE_QSV_VRAM): decode straight into
		// QSV (VA) surfaces with -hwaccel_output_format qsv and keep them there
		// through vpp_qsv + the QSV encoder — the Intel analogue of the NVDEC→
		// scale_cuda→NVENC path. SDR-only; the worker only sets QSVVRAM on a QSV
		// encoder (single Intel GPU does both decode and encode). Mutually
		// exclusive with the system-memory QSVDecode path above.
		if a.QSVVRAM && !a.NeedsToneMap && !useCUDADecode && !a.CudaHDRTonemap && !a.CudaTonemap {
			args = append(args, "-hwaccel", "qsv", "-hwaccel_output_format", "qsv")
			switch {
			case a.IsHEVC:
				args = append(args, "-c:v", "hevc_qsv")
			case a.IsH264:
				args = append(args, "-c:v", "h264_qsv")
			case a.IsAV1:
				args = append(args, "-c:v", "av1_qsv")
			}
		}

		// NVDEC hardware HEVC decode (the NVIDIA analogue of the QSV path).
		// hevc_cuvid decodes on the GPU and, with no -hwaccel_output_format
		// cuda, returns frames to system memory so the existing scale/tonemap
		// chain + NVENC encode run unchanged — this offloads the expensive 4K
		// HEVC decode that software-decoding bottlenecks below real-time (the
		// cause of the 4K-HDR stall). Gated on a real startup decode probe
		// (CudaHevcDecode) because mainline ffmpeg + some drivers fail NVDEC
		// HEVC on certain sources; the worker retries with software decode if a
		// session produces no segments. Mutually exclusive with the QSV path
		// and the AV1 full-VRAM carve-out above.
		if a.CudaHevcDecode && a.IsHEVC && !useCUDADecode && !a.CudaHDRTonemap && !a.CudaTonemap && !a.QSVDecode {
			args = append(args, "-c:v", "hevc_cuvid")
		}

		// Vulkan device for the libplacebo GPU tonemap (buildVideoFilter emits
		// the libplacebo filter when HasLibplacebo + an HDR source). Must be
		// initialized before -i so the filter can bind it. Mirrors the
		// lpTonemap condition in buildVideoFilter.
		if a.NeedsToneMap && a.HasLibplacebo && !a.IsVAAPI {
			args = append(args, "-init_hw_device", "vulkan")
		}

		// NVENC: software decode + NVENC encode (default path).
		// Setting `-hwaccel cuda -hwaccel_output_format cuda` for
		// HEVC / 10-bit sources is the textbook Jellyfin /
		// nvidia-recommended pattern, but on mainline ffmpeg 8.x +
		// recent NVIDIA drivers it's fragile across source files —
		// we hit "[hevc] No decoder surfaces left" on x265 BDRip
		// sources and h264_nvenc -22 EINVAL on the cuda-frame chain
		// for 10-bit HEVC sources. Both manifest as a 70 s playlist-
		// endpoint stall (deadline-wait for seg 0 that never comes,
		// because ffmpeg crashed at filter init).
		//
		// jellyfin-ffmpeg patches around these driver quirks; mainline
		// + Gyan.dev does not. Until we ship a Jellyfin-fork ffmpeg in
		// our installer, software input decode is the only way to make
		// NVENC reliable across HEVC sources, drivers, and mainline
		// builds. Software HEVC decode runs at 17× real-time on 1080p
		// sources and 3-4× on 4K on the CPUs that ship with NVENC-
		// capable boxes — plenty of headroom for live transcoding.
		// NVENC encode itself is unaffected (it gets nv12 frames over
		// PCIe instead of straight from VRAM; trivial copy on modern
		// PCIe-4/5). The AV1 carve-out above gets the cuda decode
		// path because dav1d software decode is too slow for parity.

		// AMF: software decode + AMF encode. Setting `-hwaccel d3d11va`
		// here looked like a free win but it picks the *first* D3D11
		// adapter — on a dual-GPU box (NVIDIA dGPU + AMD iGPU, common
		// on Ryzen X-series desktops) that's NVIDIA, and the AMF
		// encoder then refuses to bind to a non-AMD device with
		// ENODEV (-19, ffmpeg surfaces it as exit 0xffffffed).
		// Pinning d3d11va to the AMD adapter requires a per-host
		// adapter-index probe that's brittle across reboots / driver
		// updates; dropping input hwaccel for AMF lets AMF own its
		// device unambiguously, and software decode is plenty on the
		// CPU class that ships with an iGPU worth encoding through.
	}

	// HTTP source input (a remote worker pulling the source from the primary
	// over the LAN — see TranscodeJob.SourceURL). Reconnect on transient
	// blips and reuse the TCP connection for the Range requests ffmpeg issues
	// while seeking; without these a dropped connection aborts the encode
	// mid-segment. These options apply to the http(s) protocol and must
	// precede -i. No-op for a local file input.
	if isHTTPURL(a.InputPath) {
		args = append(args,
			"-reconnect", "1",
			"-reconnect_streamed", "1",
			"-reconnect_delay_max", "2",
			"-multiple_requests", "1",
		)
	}

	// Speed up container probing for files with many streams (e.g. Blu-ray
	// rips with 10+ PGS subtitle tracks). FFmpeg wastes up to analyzeduration
	// per subtitle stream trying to find codec parameters it can't determine.
	// Keep both values low — we only need video+audio params, not subtitles.
	args = append(args, "-analyzeduration", "3000000", "-probesize", "5000000")

	args = append(args, "-i", a.InputPath)

	// ── Video ────────────────────────────────────────────────────────────────
	if a.AudioOnly {
		// No video output at all — no codec, no filters, no tags. The maps
		// below select only the audio stream.
	} else if videoCopy {
		// Stream copy — no re-encode, no filters, no bitrate control.
		args = append(args, "-c:v", "copy")
	} else {
		vf := buildVideoFilter(a)
		if vf != "" {
			args = append(args, "-vf", vf)
		}

		// Scale filter is embedded in vf; set codec and bitrate.
		// Headroom above target prevents NVENC from choking on complex scenes.
		// Configurable via TRANSCODE_MAXRATE_RATIO (default 1.5 = 50% headroom).
		maxrate := int(float64(a.BitrateKbps) * opts.MaxrateRatio)
		args = append(args,
			"-c:v", string(a.Encoder),
			"-b:v", fmt.Sprintf("%dk", a.BitrateKbps),
		)
		// SVT-AV1 rejects -maxrate unless the encoder is in CRF mode
		// ("Max Bitrate only supported with CRF mode"). VBR with target
		// bitrate is the live-stream shape we want, so just skip the
		// maxrate clamp for libsvtav1 — its internal rate control already
		// constrains output around -b:v without needing the muxer-side
		// hint that NVENC/AMF/x264 want.
		if a.Encoder != EncoderAV1Software {
			args = append(args,
				"-maxrate", fmt.Sprintf("%dk", maxrate),
				"-bufsize", fmt.Sprintf("%dk", maxrate*2),
			)
		}

		// Encoder-specific flags. NVENC preset/tune/rc are configurable via
		// TRANSCODE_NVENC_PRESET, TRANSCODE_NVENC_TUNE, TRANSCODE_NVENC_RC.
		//
		// Keyframe scheduling: every encoder uses `-force_key_frames` at
		// `SegmentDuration` boundaries so ffmpeg's HLS muxer cuts segments
		// at *exactly* SegmentDuration seconds, independent of source fps.
		// We keep `-g` as a defensive upper bound (a 4-second-without-
		// scene-cut run at 60fps shouldn't end up with a 240-frame GOP
		// nobody asked for) but DON'T set `-keyint_min`: with keyint_min
		// pinned to gopUpperBound (= SegmentDuration*30 = 120 frames),
		// force_key_frames was suppressed on any source < 30 fps. A 24
		// fps film wants keyframes at frame 96 (4 s × 24 fps), but
		// keyint_min=120 forbids them earlier than frame 120 — so
		// segments cut at 4 s started mid-GOP, MSE rejected them, and
		// the player's gap-jumper visibly skipped the playhead
		// forward every few seconds.
		gopUpperBound := fmt.Sprint(SegmentDuration * 30)
		forceKey := []string{
			"-force_key_frames",
			fmt.Sprintf("expr:gte(t,n_forced*%d)", SegmentDuration),
		}
		switch a.Encoder {
		case EncoderNVENC, EncoderHEVCNVENC, EncoderAV1NVENC:
			args = append(args,
				"-preset", opts.NVENCPreset, "-tune", opts.NVENCTune,
				"-rc", opts.NVENCRC,
				"-g", gopUpperBound,
				"-sc_threshold:v:0", "0",
				// NVENC honors -force_key_frames only as a plain I-frame
				// unless -forced-idr is set; without it the forced frames
				// aren't IDR, the HLS muxer won't split on them, and
				// segments fall back to the -g GOP boundary (e.g. 120
				// frames = 5.0 s on a 23.976 fps source instead of the
				// intended 4 s). Required for the on-demand ABR timeline,
				// whose predicted playlist assumes exact SegmentDuration cuts.
				"-forced-idr", "1",
			)
			args = append(args, forceKey...)
			// HEVC: main profile, let NVENC auto-select the level from resolution.
			if a.Encoder == EncoderHEVCNVENC {
				args = append(args, "-profile:v", "main")
			}
			// AV1 NVENC requires RTX 40-series; on older cards FFmpeg
			// fails fast with "No NVENC capable device found" — that's
			// the operator's GPU detection job, not ours.
		case EncoderAMF, EncoderHEVCAMF, EncoderAV1AMF:
			args = append(args,
				"-quality", "balanced", "-rc", "cbr",
				"-g", gopUpperBound,
				"-sc_threshold:v:0", "0",
			)
			args = append(args, forceKey...)
			if a.Encoder == EncoderHEVCAMF {
				// HEVC main profile; AV1 AMF has no equivalent profile flag
				// and picks the level automatically from input dimensions.
				args = append(args, "-profile:v", "main")
			}
		case EncoderHEVCQSV, EncoderAV1QSV:
			// Quick Sync uses its own preset names. Default to "medium"
			// for the bitrate-vs-speed sweet spot; "veryfast" cuts
			// quality noticeably on 4K HEVC, "slow" eats the realtime
			// budget on a NUC-class CPU.
			args = append(args,
				"-preset", "medium",
				"-g", gopUpperBound,
				"-sc_threshold:v:0", "0",
			)
			args = append(args, forceKey...)
			if a.Encoder == EncoderHEVCQSV {
				args = append(args, "-profile:v", "main")
			}
		case EncoderHEVCVAAPI, EncoderAV1VAAPI:
			args = append(args,
				"-g", gopUpperBound,
				"-sc_threshold:v:0", "0",
			)
			args = append(args, forceKey...)
			if a.Encoder == EncoderHEVCVAAPI {
				// HEVC main profile; AV1 VAAPI takes the level from input
				// dimensions and has no equivalent profile flag.
				args = append(args, "-profile:v", "main")
			}
		case EncoderAV1Software:
			// libsvtav1 is the only realtime-capable AV1 software
			// encoder. preset 8 is the live-streaming sweet spot per
			// the SVT-AV1 maintainer guidance — preset 4 is film-
			// archival quality but ~6× slower, preset 12 strips too
			// much detail for the bitrate.
			args = append(args,
				"-preset", "8",
				"-g", gopUpperBound,
				"-sc_threshold", "0",
			)
			args = append(args, forceKey...)
		default:
			// Software / VAAPI / QSV — expression-based keyframes work fine.
			args = append(args,
				"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", SegmentDuration),
				"-sc_threshold", "0",
			)
			// HEVC software: pin Main profile ONLY when the input is being
			// stripped to 8-bit (Force8Bit adds format=yuv420p upstream, so
			// the pin is consistent). The unconditional pin was a landmine:
			// libx265 REFUSES 10-bit input under `-profile:v main` and dies
			// at encoder init — and 10-bit is the most common HEVC content —
			// so the software-encoder last resort of every HEVC fallback
			// chain was guaranteed dead exactly when a fleet needed it.
			// Unpinned, libx265 infers main/main10 from the input pixel
			// format, and a client that reaches a 10-bit encode here has
			// declared a ≥10-bit decoder (the decision layer transcodes
			// depth-capped clients, which sets Force8Bit).
			if a.Encoder == EncoderHEVCSoftware && a.Force8Bit {
				args = append(args, "-profile:v", "main")
			}
		}
	}

	// ── Stream mapping ───────────────────────────────────────────────────────
	// Map video stream explicitly so we can independently select an audio
	// stream. Each map is conditional on the stream class actually existing:
	// a hard `-map` of a missing stream kills ffmpeg instantly ("Stream map
	// matches no streams"), which made audio-only sources and audio-less
	// videos equally unplayable — an endless spinner while the playlist
	// endpoint waited for segments that would never come.
	if !a.AudioOnly {
		args = append(args, "-map", "0:v:0")
	}
	if !a.NoAudio {
		if a.AudioStreamIndex >= 0 {
			args = append(args, "-map", fmt.Sprintf("0:a:%d", a.AudioStreamIndex))
		} else {
			args = append(args, "-map", "0:a:0")
		}
	}

	// ── Audio ────────────────────────────────────────────────────────────────
	// Prefer libfdk_aac over the native aac encoder when the build has it: the
	// native encoder is ~10s slower to emit the first segment on multichannel
	// audio (7.1 TrueHD source: ~15s vs ~5s), which dominated HDR-transcode start
	// latency. The "aac" config block below keys on the logical codec, so it still
	// applies. Falls back to native aac when libfdk isn't available.
	//
	// AudioCodec "copy" passes the source audio through untouched — used on a
	// remux whose source audio the client already decodes (AAC/AC3/EAC3/MP3
	// within its channel cap). Re-encoding those to AAC anyway, as the remux
	// path always did, burned CPU to strictly degrade audio the client could
	// have played bit-exact. The aac-only extras (-ac/-b:a/-af) are naturally
	// skipped by the codec keying below.
	if !a.NoAudio {
		audioCodec := a.AudioCodec
		if a.AudioCodec == "aac" && a.HasLibfdkAAC {
			audioCodec = "libfdk_aac"
		}
		args = append(args, "-c:a", audioCodec)
	}
	if !a.NoAudio && a.AudioCodec == "aac" {
		channels := a.AudioChannels
		if channels <= 0 {
			channels = 2 // default stereo
		}
		args = append(args, "-ac", fmt.Sprint(channels))
		bitrate := a.AudioBitrateKbps
		if bitrate <= 0 {
			// Scale with channel count so a 5.1 / 7.1 downmix isn't starved
			// the way a flat 128k would be (128k stereo, ~64k/channel above).
			bitrate = AACBitrateKbps(channels)
		}
		args = append(args, "-b:a", fmt.Sprintf("%dk", bitrate))
		// Align audio with video on remux (video-copy + audio-reencode)
		// sessions where the source's first keyframe isn't at PTS=0.
		// aresample=async=1 lets the resampler stretch/squeeze to keep
		// audio aligned with video mid-stream. Only apply at the
		// start of the file: with mid-stream -ss, the filter buffers
		// samples for its initial resync calculation and never
		// flushes them, so segment 0 ships with zero audio packets
		// — MSE refuses to append the empty audio sourceBuffer and
		// playback stalls. FFmpeg's natural A/V alignment after a
		// keyframe-aligned -ss is already tight (sub-100 ms), so we
		// don't need the filter for resume seeks. Do not pass
		// first_pts=0 here either: it aborts the HLS muxer.
		if a.StartOffset <= 0 {
			args = append(args, "-af", "aresample=async=1")
		}
	}

	// ── HLS output ───────────────────────────────────────────────────────────
	// HEVC and AV1 output require fMP4 segments — HLS.js's MPEG-TS
	// transmuxer doesn't support either codec. fMP4 segments are passed
	// directly to MSE without transmuxing. AV1 also has no MPEG-TS
	// stream type at all, so `-c:v copy` of an AV1 source into mpegts
	// segments crashes the muxer ("Could not find tag for codec av1");
	// AV1 source remux must therefore also route through fMP4.
	//
	// HEVC REMUX NEEDS THE SAME ROUTING, and used to miss it because the old
	// predicate carried `&& !videoCopy` — so copying an HEVC source produced
	// mpegts. Unlike AV1 this does NOT crash: MPEG-TS has a stream type for
	// HEVC (0x24), so ffmpeg writes the segments happily and the failure lands
	// entirely on the client. HLS.js cannot transmux HEVC, and no browser
	// decodes HEVC-in-TS via MSE, so the video track silently never decodes
	// while the AAC audio does — which on screen is a player that alternates
	// black and picture instead of erroring. Failing silently is exactly why
	// this outlasted the AV1 case that failed loudly.
	//
	// ResolveVideoOutput is the ONE place that answers "what comes out and in
	// what container"; see videooutput.go for why this must not be re-derived
	// per call site. ForceFMP4 is folded in there too, so the worker — which
	// hunts for these same files by extension — cannot disagree with us.
	vout := ResolveVideoOutput(a.Encoder, a.IsHEVC, a.IsAV1, a.ForceFMP4)
	if a.AudioOnly {
		// No video stream: AAC-in-MPEG-TS, no codec tag, no init segment.
		vout = VideoOutput{}
	}
	needsFMP4 := vout.NeedsFMP4()
	segExt := vout.SegExt()
	segType := vout.SegType()

	segPattern := filepath.Join(a.SessionDir, a.SegmentPrefix+"%05d"+segExt)
	playlistName := a.PlaylistName
	if playlistName == "" {
		playlistName = "index.m3u8"
	}
	playlistPath := filepath.Join(a.SessionDir, playlistName)
	// Single muxed init + segs for fMP4 sessions. hls.js's transmuxer
	// demuxes muxed fMP4 internally before appending to the per-track
	// SourceBuffers, so we don't need ffmpeg's `-var_stream_map`
	// audio/video split (that demux was a shaka-MSE workaround during
	// the DASH era — gone now). Single-rendition muxed playlist also
	// avoids the master+child two-step that would need a child-playlist
	// route in the API; the existing `playlist.m3u8` endpoint serves
	// the one m3u8 directly.

	// Codec tag: HEVC → hvc1, AV1 → av01. Browser MSE requires the
	// modern fourCC tag rather than the codec's native one to
	// recognize the stream.
	if tag := vout.TagV(); tag != "" {
		args = append(args, "-tag:v", tag)
	}

	// temp_file: write each segment as name.tmp and RENAME on completion.
	// Every serving path — the worker's segment server, the API's local
	// fallback, HighestSegmentIndex, WaitForSeg0Audio — treats "file exists"
	// as "segment complete". Without the rename, a request racing ffmpeg's
	// write got the truncated prefix of an in-progress segment with a 200,
	// and the player decoded garbage or stalled mid-buffer. The .tmp suffix
	// keeps a partial file invisible to every one of those existence checks.
	// NOTE on delete_segments: it is INERT here. With `-hls_list_size 0` no
	// entry ever leaves the playlist, and delete_segments only removes
	// segments that have rotated out — so single-rendition sessions keep every
	// segment on disk for their whole lifetime (which the ABR seek path relies
	// on: abrReachableSoon serves any already-produced segment from disk). The
	// real disk bound is the session lifecycle: idle-kill reaps abandoned
	// encodes at 60 s and the session-aware wipe reclaims the dir afterwards.
	// The flag is kept for the day a rolling window returns; the
	// hls_delete_threshold below is equally inert until then.
	hlsFlags := "independent_segments+delete_segments+temp_file"
	// For video-copy (remux), FFmpeg runs 10-100× real-time producing segments
	// almost instantly. Use a generous delete threshold (150 segments ≈ 10 min at
	// 4 s/segment) so backward seeks rarely hit a deleted file, while still
	// bounding disk usage. For full re-encode, 30 segments (≈ 2 min) suffices.
	deleteThreshold := 30
	if videoCopy {
		deleteThreshold = 150
	}
	// Rebase output timestamps to the true content time for jobs spliced into
	// a shared timeline (ABR rung children). Must precede the muxer options —
	// it is an output option and applies to the HLS output that follows.
	if a.OutputTSOffsetSec > 0 {
		args = append(args, "-output_ts_offset", fmt.Sprintf("%.6f", a.OutputTSOffsetSec))
	}
	args = append(args,
		"-max_muxing_queue_size", "2048",
		"-f", "hls",
		"-max_delay", "5000000",
		"-hls_time", fmt.Sprint(SegmentDuration),
		"-hls_list_size", "0", // keep all segments in playlist
		"-hls_segment_type", segType,
		"-hls_flags", hlsFlags,
		"-hls_segment_filename", segPattern,
		"-hls_delete_threshold", fmt.Sprint(deleteThreshold),
	)
	// Resume segment numbering for an auto-continue run so its segment
	// files (and playlist entries) start past the prefix the earlier run
	// already wrote. Honored only without `-hls_flags append_list`, which
	// is why the continuation writes a side playlist the worker stitches in.
	if a.StartNumber > 0 {
		args = append(args, "-start_number", fmt.Sprint(a.StartNumber))
	}
	// Mid-stream -ss + AC3→AAC re-encode leaves seg 0 declared with
	// "0 channels" and no audio packets — the AAC encoder's priming
	// samples come in with negative DTS after the seek reset and get
	// dropped by the default avoid_negative_ts=make_non_negative
	// behavior. "make_zero" shifts the whole timeline instead of
	// dropping, so the priming frames survive and seg 0 carries a
	// valid audio stream with proper channel info.
	if a.StartOffset > 0 && a.AudioCodec == "aac" {
		args = append(args, "-avoid_negative_ts", "make_zero")
	}
	if needsFMP4 {
		// Single muxed init segment alongside the segs. cmd.Dir is set
		// to the session dir on the worker side; bare filename resolves
		// there.
		args = append(args, "-hls_fmp4_init_filename", "init.mp4")
	}
	// Mark remux sessions as EVENT so HLS.js starts from segment 0 rather than
	// jumping to the live edge. For video-copy, FFmpeg runs 10-100x real-time,
	// so by the time HLS.js loads the playlist it sees many segments and would
	// otherwise skip ahead (liveSyncDurationCount=3 × targetDuration behind
	// the last segment), causing a stall waiting for segments past the live edge.
	// Audio-only encodes run at a similar multiple of real-time, so they get
	// the same treatment.
	if videoCopy || a.AudioOnly {
		args = append(args, "-hls_playlist_type", "event")
	}
	args = append(args, playlistPath)

	// ── Subtitle WebVTT extraction (separate output contexts) ────────────────
	// Each subtitle stream extracts to its own .vtt file as a SECOND ffmpeg
	// output. Has to come AFTER the HLS playlist positional — otherwise the
	// preceding -c:v / -b:v / etc. accumulate onto the first positional
	// output (the .vtt) and the webvtt muxer rejects it with
	// "webvtt muxer does not support any stream of type video".
	for i, streamIdx := range a.SubtitleStreams {
		vttPath := filepath.Join(a.SessionDir, fmt.Sprintf("sub%d.vtt", i))
		args = append(args,
			"-map", fmt.Sprintf("0:s:%d", streamIdx),
			"-c:s", "webvtt",
			vttPath,
		)
	}

	return args
}

// BuildDirectStream constructs FFmpeg argv for a container-remux (no video transcode).
func BuildDirectStream(inputPath, sessionDir string, startOffset float64) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
	}
	if startOffset > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", startOffset))
	}
	// Real-time read pacing — see BuildHLS for rationale. Container
	// remux runs at 50-100× real-time which would race the worker's
	// post-completion cleanup just as hard as the transcode path.
	args = append(args, "-readrate", "1.0", "-readrate_initial_burst", "60")
	// HTTP source resilience for a remote worker pulling the source over the
	// LAN (see BuildHLS). No-op for a local file input.
	if isHTTPURL(inputPath) {
		args = append(args,
			"-reconnect", "1",
			"-reconnect_streamed", "1",
			"-reconnect_delay_max", "2",
			"-multiple_requests", "1",
		)
	}
	args = append(args,
		"-i", inputPath,
		"-c", "copy", // copy all streams
		"-f", "hls",
		"-hls_time", fmt.Sprint(SegmentDuration),
		"-hls_list_size", "0",
		"-hls_segment_type", "mpegts",
		// temp_file: same complete-on-rename contract as BuildHLS — see there.
		"-hls_flags", "independent_segments+delete_segments+temp_file",
		"-hls_delete_threshold", "5", // keep last 5 segments on disk
		"-hls_segment_filename", filepath.Join(sessionDir, "seg%05d.ts"),
		filepath.Join(sessionDir, "index.m3u8"),
	)
	return args
}

// buildVideoFilter constructs the -vf filter chain for the given args.
func buildVideoFilter(a BuildArgs) string {
	var filters []string

	isNVENC := a.Encoder == EncoderNVENC || a.Encoder == EncoderHEVCNVENC
	isAMF := a.Encoder == EncoderAMF || a.Encoder == EncoderHEVCAMF || a.Encoder == EncoderAV1AMF
	isQSV := a.Encoder == EncoderQSV || a.Encoder == EncoderHEVCQSV || a.Encoder == EncoderAV1QSV
	// Must stay in lockstep with the decode-side useCUDADecode in BuildHLS: when
	// the input decodes into CUDA memory, the scale filter must be scale_cuda so
	// frames stay in VRAM. AV1 always; HEVC/H.264 SDR only when CudaVRAM is set.
	useCUDADecode := IsNVENCEncoder(a.Encoder) && !a.NeedsToneMap &&
		(a.IsAV1 || ((a.IsHEVC || a.IsH264) && a.CudaVRAM))
	// Intel full-VRAM scale paths — must stay in lockstep with the decode-side
	// QSVVRAM/VAAPIVRAM branches in BuildHLS. When the input decodes into QSV/VA
	// surfaces, scale with the matching GPU filter (vpp_qsv / scale_vaapi) so
	// frames never leave VRAM. SDR-only (HDR keeps the software/libplacebo path).
	useQSVVRAM := isQSV && a.QSVVRAM && !a.NeedsToneMap
	useVAAPIVRAM := a.IsVAAPI && a.VAAPIVRAM && !a.NeedsToneMap

	// HDR→SDR tone mapping (zscale + tonemap=hable on the CPU). zscale operates
	// on CPU frames and can't reach hardware surfaces, so it runs before any
	// hwupload; its output is 8-bit bt709 yuv420p in CPU memory, ready for any
	// downstream encoder (software, NVENC, AMF, QSV, VAAPI) to pass through or
	// upload to a hardware surface.
	//
	// AMF + QSV + VAAPI included unconditionally because none has a vendor
	// tonemap filter we trust in mainline ffmpeg (`tonemap_amf` / `tonemap_qsv`
	// don't exist; `tonemap_vaapi`'s HDR-metadata handling varies by driver).
	// The all-CUDA pipeline (-hwaccel cuda → scale_cuda → tonemap_opencl) was
	// also retired for fragility on mainline ffmpeg 8.x + recent NVIDIA drivers
	// (see the BuildArgs comment) — NVENC/AMF/QSV all share this software path.
	// zscale is slower than a true GPU pipeline but correct across every family.
	// Preferred GPU tonemap: libplacebo (Vulkan). Vendor-agnostic and
	// GPU-resident, so it sustains 4K HDR where software zscale can't (a 4K
	// HDR transcode tonemaps at full resolution — the scale-before-tonemap
	// trick can't help when output is also 4K — and a laptop falls below
	// realtime and times out). libplacebo also does the downscale and the
	// 8-bit downconvert, so it replaces BOTH the zscale chain and the software
	// scale; -init_hw_device vulkan is added in BuildHLS. Skipped on the VAAPI
	// encode path (that stays on VAAPI surfaces). Software zscale is the
	// fallback when libplacebo/Vulkan isn't available on the host.
	lpTonemap := a.NeedsToneMap && a.HasLibplacebo && !a.IsVAAPI

	// All-VRAM VAAPI HDR via tonemap_vaapi: scale_vaapi downscale (keeps the HDR
	// signal) + tonemap_vaapi HDR→SDR, both on VA surfaces, straight to the VAAPI
	// encoder — no CPU zscale, no hwdownload. The VAAPI analogue of cudaTonemap;
	// probe-gated (VAAPITonemap) because the iHD driver's HDR handling varies.
	vaapiTonemap := a.VAAPITonemap && a.NeedsToneMap && a.IsVAAPI &&
		(a.Encoder == EncoderVAAPI || a.Encoder == EncoderHEVCVAAPI || a.Encoder == EncoderAV1VAAPI)

	needsSoftwareTonemap := !lpTonemap && !vaapiTonemap && a.NeedsToneMap && a.HasZscale &&
		(a.Encoder == EncoderSoftware || a.Encoder == EncoderHEVCSoftware ||
			isAMF || isQSV || isNVENC || a.IsVAAPI)

	// zscale-based HDR→SDR chain (libzimg required in the FFmpeg build).
	toneMap := strings.Join([]string{
		"zscale=t=linear:npl=100",
		"format=gbrpf32le",
		"zscale=p=bt709",
		"tonemap=tonemap=hable:desat=0",
		"zscale=t=bt709:m=bt709:r=tv",
		"format=yuv420p",
	}, ",")

	// Software scale shared by the NVENC / AMF / QSV / software encoders.
	swScale := ""
	if a.Width > 0 && a.Height > 0 {
		swScale = fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", a.Width, a.Height, a.Width, a.Height)
	}

	// On the pure software-scale path, scale BEFORE tonemapping so the
	// expensive zscale chain processes OUTPUT-resolution frames (e.g. 1080p)
	// instead of the source's (e.g. 4K) — for a 4K→1080p HDR transcode that's
	// ~4× fewer pixels through the dominant CPU stage. The `scale` filter
	// preserves the input's transfer/primaries metadata, so the subsequent
	// zscale chain still linearizes the HDR signal correctly. VAAPI keeps
	// tonemap → hwupload → scale_vaapi (scale_vaapi needs hardware frames),
	// and the AV1 cuda-decode path carries no tonemap.
	swTonemapScaleFirst := needsSoftwareTonemap && swScale != "" && !a.IsVAAPI && !useCUDADecode

	// All-VRAM NVIDIA HDR via tonemap_cuda: scale_cuda downscale + tonemap_cuda
	// HDR→SDR, both in CUDA memory, straight to NVENC — no CPU, no hwdownload, no
	// Vulkan. The preferred HDR path on NVIDIA (works on TrueNAS where libplacebo
	// can't init); decode is in CUDA memory (CudaTonemap → full-VRAM decode).
	cudaTonemap := a.CudaTonemap && a.NeedsToneMap && !a.IsVAAPI && (a.IsHEVC || a.IsH264)

	// GPU-assisted HDR fallback (no tonemap_cuda, no libplacebo): scale_cuda does
	// the expensive downscale in VRAM keeping 10-bit (p010 — nv12 would clip the
	// HDR signal to 8-bit before tonemapping), then hwdownload hands the small
	// frame to the same zscale tonemap the software path uses.
	cudaHDRTonemap := a.CudaHDRTonemap && a.NeedsToneMap && a.HasZscale &&
		!cudaTonemap && !lpTonemap && !a.IsVAAPI && (a.IsHEVC || a.IsH264)

	switch {
	case cudaTonemap:
		// scale_cuda keeps 10-bit (no format=), tonemap_cuda does HDR→SDR to nv12;
		// the CUDA nv12 frame feeds NVENC directly.
		tm := "tonemap_cuda=tonemap=bt2390:t=bt709:m=bt709:p=bt709:r=tv:format=nv12"
		if a.Width > 0 && a.Height > 0 {
			filters = append(filters,
				fmt.Sprintf("scale_cuda=w=%d:h=%d:force_original_aspect_ratio=decrease", a.Width, a.Height), tm)
		} else {
			filters = append(filters, tm)
		}
	case lpTonemap:
		// libplacebo (Vulkan): tonemap + scale + 8-bit downconvert in one GPU
		// pass, then hwdownload to CPU yuv420p for the encoder. a.Width/Height
		// are already aspect-correct (computed by the API / ladder), so no pad.
		lp := "libplacebo=tonemapping=bt.2390:colorspace=bt709:color_primaries=bt709:color_trc=bt709:range=tv:format=yuv420p"
		if a.Width > 0 && a.Height > 0 {
			lp = fmt.Sprintf("libplacebo=w=%d:h=%d:tonemapping=bt.2390:colorspace=bt709:color_primaries=bt709:color_trc=bt709:range=tv:format=yuv420p", a.Width, a.Height)
		}
		filters = append(filters, lp, "hwdownload", "format=yuv420p")
	case cudaHDRTonemap:
		// scale_cuda downscale in VRAM at 10-bit, then download + zscale tonemap
		// on the smaller frame. format=p010le keeps the HDR signal through the
		// resize; scale_cuda propagates the source's transfer/primaries metadata
		// so the zscale chain linearizes the PQ/HLG signal correctly (same
		// assumption as the software `scale` path). No pad — Width/Height are
		// already aspect-correct.
		sc := "scale_cuda=format=p010le"
		if a.Width > 0 && a.Height > 0 {
			sc = fmt.Sprintf("scale_cuda=w=%d:h=%d:force_original_aspect_ratio=decrease:format=p010le", a.Width, a.Height)
		}
		filters = append(filters, sc, "hwdownload", "format=p010le", toneMap)
	case vaapiTonemap:
		// All-VRAM VAAPI HDR: scale_vaapi downscales on VA surfaces keeping the HDR
		// signal (no format=, so 10-bit p010 survives the resize), then tonemap_vaapi
		// does HDR→SDR to nv12 on the GPU. Both stay in VRAM and feed the VAAPI
		// encoder zero-copy — no CPU zscale, no hwdownload. No pad (scale_vaapi's
		// decrease flag preserves aspect; Width/Height are already correct).
		tm := "tonemap_vaapi=format=nv12:matrix=bt709:primaries=bt709:transfer=bt709"
		if a.Width > 0 && a.Height > 0 {
			filters = append(filters,
				fmt.Sprintf("scale_vaapi=w=%d:h=%d:force_original_aspect_ratio=decrease", a.Width, a.Height), tm)
		} else {
			filters = append(filters, tm)
		}
	case swTonemapScaleFirst:
		filters = append(filters, swScale, toneMap)
	default:
		if needsSoftwareTonemap {
			filters = append(filters, toneMap)
		}
		// VAAPI hwupload prep — after the tonemap chain so HDR sources are
		// downconverted to bt709 yuv420p in CPU first, then re-formatted to
		// nv12 and uploaded to a VAAPI surface for scale_vaapi + encode. Skipped
		// on the full-VRAM VAAPI path: the input was hardware-decoded straight
		// into VA surfaces, so there's nothing to upload.
		if a.IsVAAPI && !useVAAPIVRAM {
			filters = append(filters, "format=nv12", "hwupload")
		}
		// Scale to target resolution, maintaining aspect ratio.
		if a.Width > 0 && a.Height > 0 {
			switch {
			case useQSVVRAM:
				// Full-VRAM Intel QSV: vpp_qsv scales in QSV (VA) memory and outputs
				// nv12 (8-bit), downconverting 10-bit sources on the GPU — the Intel
				// analogue of scale_cuda. No pad (vpp_qsv has none); the API's
				// Width/Height are already aspect-correct.
				filters = append(filters, fmt.Sprintf("vpp_qsv=w=%d:h=%d:format=nv12", a.Width, a.Height))
			case a.Encoder == EncoderVAAPI || a.Encoder == EncoderHEVCVAAPI || a.Encoder == EncoderAV1VAAPI:
				// scale_vaapi runs on VA surfaces — fed by the hwupload above on the
				// default path, or directly by the hardware decode on VAAPIVRAM.
				// format=nv12 forces 8-bit (a no-op after the nv12 hwupload; on the
				// VRAM path it downconverts a 10-bit hardware-decoded surface).
				filters = append(filters, fmt.Sprintf("scale_vaapi=w=%d:h=%d:force_original_aspect_ratio=decrease:format=nv12", a.Width, a.Height))
			case useCUDADecode:
				// Full-VRAM NVDEC path (AV1 always; HEVC/H.264 SDR when probed
				// OK): keep frames in VRAM through scaling. scale_cuda outputs nv12
				// (8-bit) so 10-bit sources are downconverted in GPU memory — no
				// CPU touch, no pad filter (no pad_cuda exists; the decrease flag
				// preserves aspect).
				filters = append(filters, fmt.Sprintf("scale_cuda=w=%d:h=%d:force_original_aspect_ratio=decrease:format=nv12", a.Width, a.Height))
			default:
				filters = append(filters, swScale)
			}
		}
	}

	h264Family := a.Encoder == EncoderAMF || a.Encoder == EncoderQSV || a.Encoder == EncoderNVENC || a.Encoder == EncoderSoftware
	// Force8Bit widens the strip to the HEVC/AV1 encoders on the
	// software-frame paths. Those encoders accept 10-bit (Main 10) — which is
	// exactly the problem when the client declared an 8-bit decoder and the
	// bit-depth REDUCTION is why this transcode exists: without the strip the
	// output carried the same 10-bit property that forced the transcode, and
	// libx265 with its Main profile pin refused the 10-bit input outright.
	// The VRAM chains are excluded for the same reason as ever (their filters
	// already emit 8-bit nv12 on-GPU; a CPU format filter would force a
	// round-trip), and they land at 8-bit anyway.
	force8 := a.Force8Bit && (IsHEVCEncoder(a.Encoder) || IsAV1Encoder(a.Encoder))
	if !needsSoftwareTonemap && !lpTonemap && !useCUDADecode && !useQSVVRAM && !useVAAPIVRAM && !cudaTonemap && !cudaHDRTonemap && (h264Family || force8) {
		// h264_amf / h264_qsv / h264_nvenc all reject 10-bit input
		// ("10-bit input video is not supported"). libx264 *accepts*
		// 10-bit input but emits 10-bit High 10 profile H.264 — valid
		// bitstream, but most browsers can't decode it (Chromium has
		// no 10-bit H.264 decoder). The tonemap chain above ends in
		// format=yuv420p and covers HDR sources; this branch handles
		// the rare 10-bit SDR source (some anime, AV1 10-bit archival
		// masters) and is a no-op on 8-bit input. HEVC variants of
		// these encoders accept 10-bit (Main10), so we only strip for
		// them under Force8Bit (above). Skipped on the CUDA-decode path:
		// scale_cuda already emits nv12 (8-bit) in VRAM, and inserting a
		// CPU format filter would force a hwdownload that defeats the
		// purpose of keeping the pipeline in GPU memory.
		filters = append(filters, "format=yuv420p")
	}

	return strings.Join(filters, ",")
}

// IsNVENCEncoder reports whether enc is any NVENC encoder. All three (H.264,
// HEVC, AV1) can consume NVDEC cuda frames directly, so they share the AV1
// cuda-decode fast path — `av1_nvenc` must be included or an AV1→AV1 ladder
// silently falls back to software dav1d decode (too slow for 4K).
func IsNVENCEncoder(enc Encoder) bool {
	switch enc {
	case EncoderNVENC, EncoderHEVCNVENC, EncoderAV1NVENC:
		return true
	default:
		return false
	}
}

// IsQSVEncoder reports whether enc is any Intel Quick Sync encoder. All three
// read QSV (VA) surfaces directly, so they share the full-VRAM QSVVRAM path.
func IsQSVEncoder(enc Encoder) bool {
	switch enc {
	case EncoderQSV, EncoderHEVCQSV, EncoderAV1QSV:
		return true
	default:
		return false
	}
}

// IsVAAPIEncoder reports whether enc is any VAAPI encoder. All three read VA
// surfaces directly, so they share the full-VRAM VAAPIVRAM path.
func IsVAAPIEncoder(enc Encoder) bool {
	switch enc {
	case EncoderVAAPI, EncoderHEVCVAAPI, EncoderAV1VAAPI:
		return true
	default:
		return false
	}
}

// IsHEVCEncoder returns true if the encoder produces HEVC (H.265) output.
func IsHEVCEncoder(enc Encoder) bool {
	switch enc {
	case EncoderHEVCNVENC, EncoderHEVCQSV, EncoderHEVCVAAPI,
		EncoderHEVCAMF, EncoderHEVCSoftware:
		return true
	default:
		return false
	}
}

// IsAV1Encoder returns true if the encoder produces AV1 output.
// AV1 needs the same fMP4 segment treatment as HEVC for HLS — the
// MPEG-TS muxer doesn't carry AV1 cleanly across all browsers.
func IsAV1Encoder(enc Encoder) bool {
	switch enc {
	case EncoderAV1Software, EncoderAV1NVENC, EncoderAV1QSV,
		EncoderAV1VAAPI, EncoderAV1AMF:
		return true
	default:
		return false
	}
}
