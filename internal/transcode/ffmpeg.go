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
	Encoder          Encoder
	Width            int
	Height           int
	BitrateKbps      int
	NeedsToneMap     bool // HDR→SDR tone mapping (ADR-030)
	HasTonemapCuda   bool // tonemap_cuda filter available in FFmpeg
	HasTonemapOpenCL bool // tonemap_opencl filter available in FFmpeg
	HasZscale        bool // zscale filter available (libzimg) for software tonemap
	IsVAAPI          bool // VAAPI needs hwupload filter
	IsHEVC           bool // source is HEVC (informational, NVDEC auto-selects decoder)

	// Audio (ADR-018)
	AudioCodec       string // "copy" | "aac"
	AudioChannels    int    // 0 = keep source
	AudioBitrateKbps int    // 0 = auto
	AudioStreamIndex int    // -1 = default (first); >= 0 = specific stream index

	// Subtitles
	ExtractSubtitles bool
	SubtitleStreams  []int // stream indices to extract as WebVTT
	// BurnSubtitleStream, when set, hard-burns the named subtitle
	// stream into the video. Used by clients that can't render
	// external WebVTT (older smart-TV browsers, some embedded
	// devices). Forces a full re-encode — no video-copy. The value
	// is the source's subtitle stream index (e.g. 0 for the first
	// subtitle track), and Encoder must be a real encoder
	// (libx264 / NVENC / etc.), not "copy".
	BurnSubtitleStream *int

	// Encoder tuning
	EncoderOpts EncoderOpts

	// Output
	SessionDir    string // abs path, e.g. /tmp/onscreen/sessions/{id}
	SegmentPrefix string // relative prefix for .ts files, e.g. "seg"
}

// SegmentDuration is the HLS segment duration in seconds (ADR-007).
const SegmentDuration = 4

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

	videoCopy := a.Encoder == "copy"

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
	}

	// Seek to start position (fast input seek for keyframe alignment).
	if a.StartOffset > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", a.StartOffset))
	}

	isNVENC := !videoCopy && (a.Encoder == EncoderNVENC || a.Encoder == EncoderHEVCNVENC)

	// Tonemap strategy for NVENC with HDR content:
	//   1. tonemap_cuda  — all-GPU pipeline, fastest (not in mainline FFmpeg)
	//   2. tonemap_opencl — CUDA decode → OpenCL tonemap → NVENC, 2 PCIe round-trips
	//   3. zscale         — full software decode + CPU tonemap, slowest
	//   4. skip           — no tonemapping, washed-out output but plays
	useOpenCLTonemap := isNVENC && a.NeedsToneMap && !a.HasTonemapCuda && a.HasTonemapOpenCL

	// Use CUDA hwaccel when:
	//   - NVENC is selected AND
	//   - either no tonemapping is needed, or we have tonemap_cuda, or we have tonemap_opencl
	// Without any GPU-capable tonemap, fall back to software decode + zscale.
	useCudaHwaccel := isNVENC && !(a.NeedsToneMap && !a.HasTonemapCuda && !a.HasTonemapOpenCL)

	if !videoCopy {
		// VAAPI init filter (must come before input for hardware decode).
		if a.IsVAAPI {
			args = append(args, "-vaapi_device", "/dev/dri/renderD128")
		}

		// NVENC: full GPU pipeline — CUDA hardware decode (NVDEC) + GPU filters
		// + NVENC encode. Frames never leave the GPU.
		//
		// Key flags from Jellyfin's proven NVENC pipeline:
		//   -hwaccel_flags +unsafe_output  — skips internal frame copies that
		//     can deadlock on HEVC+PGS with certain driver versions
		//   -threads 1  — prevents multi-threaded decode contention with the GPU
		//   -hwaccel_output_format cuda  — keeps decoded frames in CUDA memory
		//     so scale_cuda / tonemap_cuda can process them without CPU roundtrip
		if useCudaHwaccel {
			args = append(args,
				"-hwaccel", "cuda",
				"-hwaccel_output_format", "cuda",
				"-hwaccel_flags", "+unsafe_output",
				"-threads", "1",
			)
		}

		// OpenCL tonemap: initialize the OpenCL device so hwupload/tonemap_opencl
		// can use it. Must come before -i.
		if useOpenCLTonemap {
			args = append(args,
				"-init_hw_device", "opencl=ocl",
				"-filter_hw_device", "ocl",
			)
		}

		// AMF: use D3D11VA hardware decode to keep the pipeline on the GPU.
		if a.Encoder == EncoderAMF {
			args = append(args, "-hwaccel", "d3d11va")
		}
	}

	// Speed up container probing for files with many streams (e.g. Blu-ray
	// rips with 10+ PGS subtitle tracks). FFmpeg wastes up to analyzeduration
	// per subtitle stream trying to find codec parameters it can't determine.
	// Keep both values low — we only need video+audio params, not subtitles.
	args = append(args, "-analyzeduration", "3000000", "-probesize", "5000000")

	args = append(args, "-i", a.InputPath)

	// ── Video ────────────────────────────────────────────────────────────────
	if videoCopy {
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
			"-maxrate", fmt.Sprintf("%dk", maxrate),
			"-bufsize", fmt.Sprintf("%dk", maxrate*2),
		)

		// Encoder-specific flags. NVENC preset/tune/rc are configurable via
		// TRANSCODE_NVENC_PRESET, TRANSCODE_NVENC_TUNE, TRANSCODE_NVENC_RC.
		switch a.Encoder {
		case EncoderNVENC, EncoderHEVCNVENC, EncoderAV1NVENC:
			// Fixed GOP matching segment duration (assume ~30fps max → 120 frames
			// for 4s segments). More reliable than -force_key_frames with NVENC.
			gopSize := fmt.Sprint(SegmentDuration * 30)
			args = append(args,
				"-preset", opts.NVENCPreset, "-tune", opts.NVENCTune,
				"-rc", opts.NVENCRC,
				"-g", gopSize, "-keyint_min", gopSize,
				"-sc_threshold:v:0", "0",
			)
			// HEVC: main profile, let NVENC auto-select the level from resolution.
			if a.Encoder == EncoderHEVCNVENC {
				args = append(args, "-profile:v", "main")
			}
			// AV1 NVENC requires RTX 40-series; on older cards FFmpeg
			// fails fast with "No NVENC capable device found" — that's
			// the operator's GPU detection job, not ours.
		case EncoderAMF, EncoderHEVCAMF:
			gopSize := fmt.Sprint(SegmentDuration * 30)
			args = append(args,
				"-quality", "balanced", "-rc", "cbr",
				"-g", gopSize, "-keyint_min", gopSize,
				"-sc_threshold:v:0", "0",
			)
			if a.Encoder == EncoderHEVCAMF {
				args = append(args, "-profile:v", "main")
			}
		case EncoderHEVCQSV, EncoderAV1QSV:
			// Quick Sync uses its own preset names. Default to "medium"
			// for the bitrate-vs-speed sweet spot; "veryfast" cuts
			// quality noticeably on 4K HEVC, "slow" eats the realtime
			// budget on a NUC-class CPU.
			gopSize := fmt.Sprint(SegmentDuration * 30)
			args = append(args,
				"-preset", "medium",
				"-g", gopSize, "-keyint_min", gopSize,
				"-sc_threshold:v:0", "0",
			)
			if a.Encoder == EncoderHEVCQSV {
				args = append(args, "-profile:v", "main")
			}
		case EncoderHEVCVAAPI:
			gopSize := fmt.Sprint(SegmentDuration * 30)
			args = append(args,
				"-g", gopSize, "-keyint_min", gopSize,
				"-sc_threshold:v:0", "0",
				"-profile:v", "main",
			)
		case EncoderAV1Software:
			// libsvtav1 is the only realtime-capable AV1 software
			// encoder. preset 8 is the live-streaming sweet spot per
			// the SVT-AV1 maintainer guidance — preset 4 is film-
			// archival quality but ~6× slower, preset 12 strips too
			// much detail for the bitrate.
			gopSize := fmt.Sprint(SegmentDuration * 30)
			args = append(args,
				"-preset", "8",
				"-g", gopSize, "-keyint_min", gopSize,
				"-sc_threshold", "0",
			)
		default:
			// Software / VAAPI / QSV — expression-based keyframes work fine.
			args = append(args,
				"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", SegmentDuration),
				"-sc_threshold", "0",
			)
			// HEVC software: constrain to Main profile, Level 5.0 for 4K.
			if a.Encoder == EncoderHEVCSoftware {
				args = append(args, "-profile:v", "main", "-level-idc", "150")
			}
		}
	}

	// ── Stream mapping ───────────────────────────────────────────────────────
	// Map video stream explicitly so we can independently select an audio stream.
	args = append(args, "-map", "0:v:0")
	if a.AudioStreamIndex >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", a.AudioStreamIndex))
	} else {
		args = append(args, "-map", "0:a:0")
	}

	// ── Audio ────────────────────────────────────────────────────────────────
	args = append(args, "-c:a", a.AudioCodec)
	if a.AudioCodec == "aac" {
		channels := a.AudioChannels
		if channels <= 0 {
			channels = 2 // default stereo
		}
		args = append(args, "-ac", fmt.Sprint(channels))
		if a.AudioBitrateKbps > 0 {
			args = append(args, "-b:a", fmt.Sprintf("%dk", a.AudioBitrateKbps))
		} else {
			args = append(args, "-b:a", "128k")
		}
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

	// ── Subtitles ────────────────────────────────────────────────────────────
	if len(a.SubtitleStreams) > 0 {
		// Extract each text-based subtitle stream to a separate WebVTT file.
		// These are output as additional -map outputs, not part of the HLS playlist.
		for i, streamIdx := range a.SubtitleStreams {
			vttPath := filepath.Join(a.SessionDir, fmt.Sprintf("sub%d.vtt", i))
			args = append(args,
				"-map", fmt.Sprintf("0:s:%d", streamIdx),
				"-c:s", "webvtt",
				vttPath,
			)
		}
	}

	// ── HLS output ───────────────────────────────────────────────────────────
	// HEVC and AV1 output require fMP4 segments — HLS.js's MPEG-TS
	// transmuxer doesn't support either codec. fMP4 segments are passed
	// directly to MSE without transmuxing.
	isHEVCOutput := IsHEVCEncoder(a.Encoder) && !videoCopy
	isAV1Output := IsAV1Encoder(a.Encoder) && !videoCopy
	needsFMP4 := isHEVCOutput || isAV1Output
	segExt := ".ts"
	segType := "mpegts"
	if needsFMP4 {
		segExt = ".m4s"
		segType = "fmp4"
	}

	segPattern := filepath.Join(a.SessionDir, a.SegmentPrefix+"%05d"+segExt)
	playlistPath := filepath.Join(a.SessionDir, "index.m3u8")

	// Codec tag: HEVC → hvc1, AV1 → av01. Browser MSE requires the
	// modern fourCC tag rather than the codec's native one to
	// recognize the stream.
	switch {
	case isHEVCOutput:
		args = append(args, "-tag:v", "hvc1")
	case isAV1Output:
		args = append(args, "-tag:v", "av01")
	}

	hlsFlags := "independent_segments+delete_segments"
	// For video-copy (remux), FFmpeg runs 10-100× real-time producing segments
	// almost instantly. Use a generous delete threshold (150 segments ≈ 10 min at
	// 4 s/segment) so backward seeks rarely hit a deleted file, while still
	// bounding disk usage. For full re-encode, 30 segments (≈ 2 min) suffices.
	deleteThreshold := 30
	if videoCopy {
		deleteThreshold = 150
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
		args = append(args, "-hls_fmp4_init_filename", "init.mp4")
	}
	// Mark remux sessions as EVENT so HLS.js starts from segment 0 rather than
	// jumping to the live edge. For video-copy, FFmpeg runs 10-100x real-time,
	// so by the time HLS.js loads the playlist it sees many segments and would
	// otherwise skip ahead (liveSyncDurationCount=3 × targetDuration behind
	// the last segment), causing a stall waiting for segments past the live edge.
	if videoCopy {
		args = append(args, "-hls_playlist_type", "event")
	}
	args = append(args, playlistPath)

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
	args = append(args,
		"-i", inputPath,
		"-c", "copy", // copy all streams
		"-f", "hls",
		"-hls_time", fmt.Sprint(SegmentDuration),
		"-hls_list_size", "0",
		"-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments+delete_segments",
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

	if a.IsVAAPI {
		filters = append(filters, "format=nv12", "hwupload")
	}

	// ── NVENC: full GPU filter pipeline ─────────────────────────────────────
	// With -hwaccel cuda -hwaccel_output_format cuda, decoded frames are already
	// in CUDA memory. All filters operate in VRAM — no CPU roundtrip.
	//
	// Priority for HDR tonemapping on NVENC:
	//   1. tonemap_cuda  — all-CUDA, fastest (jellyfin-ffmpeg fork only)
	//   2. tonemap_opencl — CUDA decode, OpenCL tonemap, NVENC encode
	//   3. zscale         — software decode + CPU tonemap (handled below)
	useCudaPipeline := isNVENC && !(a.NeedsToneMap && !a.HasTonemapCuda && !a.HasTonemapOpenCL)
	useOpenCLTonemap := isNVENC && a.NeedsToneMap && !a.HasTonemapCuda && a.HasTonemapOpenCL

	if isNVENC && useCudaPipeline {
		if a.NeedsToneMap && !useOpenCLTonemap {
			// tonemap_cuda: all-CUDA pipeline, frames never leave VRAM.
			filters = append(filters, "tonemap_cuda=tonemap=hable:desat=0:peak=100:format=nv12")
		}

		if useOpenCLTonemap {
			// tonemap_opencl: CUDA decode → scale in CUDA → download → OpenCL tonemap → download.
			// NVENC accepts CPU-side NV12 frames (implicit upload).
			scaleFilter := "scale_cuda=format=p010"
			if a.Width > 0 && a.Height > 0 {
				scaleFilter = fmt.Sprintf("scale_cuda=w=%d:h=%d:force_original_aspect_ratio=decrease:format=p010", a.Width, a.Height)
			}
			filters = append(filters,
				scaleFilter,
				"hwdownload",
				"format=p010",
				"hwupload",
				"tonemap_opencl=tonemap=hable:desat=0:peak=100:format=nv12:primaries=bt709:transfer=bt709:matrix=bt709",
				"hwdownload",
				"format=nv12",
			)
			return strings.Join(filters, ",")
		}

		// GPU-side scaling + 10-bit → 8-bit via format=nv12.
		if a.Width > 0 && a.Height > 0 {
			filters = append(filters, fmt.Sprintf("scale_cuda=w=%d:h=%d:force_original_aspect_ratio=decrease:format=nv12", a.Width, a.Height))
		} else if !a.NeedsToneMap {
			// No scale + no tonemap: still need format conversion for 10-bit sources.
			filters = append(filters, "scale_cuda=format=nv12")
		}
		return strings.Join(filters, ",")
	}

	// ── Non-NVENC paths ─────────────────────────────────────────────────────
	// Scale to target resolution, maintaining aspect ratio.
	if a.Width > 0 && a.Height > 0 {
		switch {
		case a.Encoder == EncoderVAAPI || a.Encoder == EncoderHEVCVAAPI:
			filters = append(filters, fmt.Sprintf("scale_vaapi=w=%d:h=%d:force_original_aspect_ratio=decrease", a.Width, a.Height))
		default:
			filters = append(filters, fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", a.Width, a.Height, a.Width, a.Height))
		}
	}

	// HDR→SDR tone mapping — CPU-based fallback when tonemap_cuda is unavailable.
	// Requires zscale (libzimg). If neither tonemap_cuda nor zscale is available,
	// tonemapping is skipped entirely (HDR content will look washed out but will play).
	needsSoftwareTonemap := a.NeedsToneMap && a.HasZscale && (a.Encoder == EncoderSoftware || a.Encoder == EncoderHEVCSoftware || (isNVENC && !a.HasTonemapCuda && !a.HasTonemapOpenCL))
	if needsSoftwareTonemap {
		// zscale-based tonemapping (libzimg required in FFmpeg build).
		toneMap := strings.Join([]string{
			"zscale=t=linear:npl=100",
			"format=gbrpf32le",
			"zscale=p=bt709",
			"tonemap=tonemap=hable:desat=0",
			"zscale=t=bt709:m=bt709:r=tv",
			"format=yuv420p",
		}, ",")
		filters = append(filters, toneMap)
	}

	// Subtitle burn-in. Appended last so the overlay sits on top of any
	// scale + tonemap output. Only valid on the software path: the
	// subtitles filter requires the frame in CPU memory (yuv420p), and
	// inserting a hwdownload+hwupload around it on a hardware pipeline
	// trashes the throughput win that justified picking GPU encoding
	// in the first place. Callers that need burn-in should pick
	// EncoderSoftware up front; the caller's job, not ours, to enforce.
	if a.BurnSubtitleStream != nil {
		filters = append(filters, subtitleBurnFilter(a.InputPath, *a.BurnSubtitleStream))
	}

	return strings.Join(filters, ",")
}

// subtitleBurnFilter constructs the FFmpeg `subtitles` filter expression
// that burns stream `si` from `input` into the video. The single
// quotes around the path let FFmpeg's filter parser handle paths
// with colons + spaces; the backslash escape protects single quotes
// inside the path itself.
func subtitleBurnFilter(input string, si int) string {
	escaped := strings.ReplaceAll(input, `'`, `\'`)
	return fmt.Sprintf("subtitles='%s':si=%d", escaped, si)
}

// IsHEVCEncoder returns true if the encoder produces HEVC (H.265) output.
func IsHEVCEncoder(enc Encoder) bool {
	switch enc {
	case EncoderHEVCNVENC, EncoderHEVCQSV, EncoderHEVCVAAPI,
		EncoderHEVCAMF, EncoderHEVCSoftware:
		return true
	}
	return false
}

// IsAV1Encoder returns true if the encoder produces AV1 output.
// AV1 needs the same fMP4 segment treatment as HEVC for HLS — the
// MPEG-TS muxer doesn't carry AV1 cleanly across all browsers.
func IsAV1Encoder(enc Encoder) bool {
	switch enc {
	case EncoderAV1Software, EncoderAV1NVENC, EncoderAV1QSV:
		return true
	}
	return false
}

// HEVCVariant returns the HEVC counterpart for a given H.264 encoder.
// Returns the encoder unchanged if no HEVC variant exists.
func HEVCVariant(enc Encoder) Encoder {
	switch enc {
	case EncoderNVENC:
		return EncoderHEVCNVENC
	case EncoderSoftware:
		return EncoderHEVCSoftware
	default:
		return enc // AMF/VAAPI/QSV: no HEVC variant implemented yet
	}
}
