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
	// OpenCL platform.device index for `-init_hw_device opencl=ocl:N.M`.
	// Empty falls back to `0.0`. Probed once per worker startup so we
	// pick the platform that matches the active encoder's vendor —
	// hardcoded `0.0` is wrong on hosts where the iGPU registers an
	// OpenCL platform before the dGPU (Intel + NVIDIA on a Windows
	// laptop, AMD APP + Intel iGPU on a Ryzen workstation, etc.).
	OpenCLDevice string
	IsVAAPI          bool // VAAPI needs hwupload filter
	IsHEVC           bool // source is HEVC (informational, NVDEC auto-selects decoder)
	// IsAV1 marks an AV1 source. Required so video_copy remux switches
	// the HLS container to fMP4 + av01 tag — mpegts has no AV1 stream
	// type, so an `-c:v copy` into mpegts segments crashes the muxer
	// (Could not find tag for codec av1 in stream #0).
	IsAV1            bool

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
	_ = isNVENC // retained for tonemap-gate / filter-graph branches below

	if !videoCopy {
		// VAAPI init filter (must come before input for hardware decode).
		if a.IsVAAPI {
			args = append(args, "-vaapi_device", "/dev/dri/renderD128")
		}

		// NVENC: software decode + NVENC encode. Setting `-hwaccel cuda
		// -hwaccel_output_format cuda` here is the textbook Jellyfin /
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
		// NVENC reliable across every source / driver / mainline build.
		// Software HEVC decode runs at 17× real-time on 1080p sources
		// and 3-4× on 4K on the CPUs that ship with NVENC-capable
		// boxes — plenty of headroom for live transcoding. NVENC encode
		// itself is unaffected (it gets nv12 frames over PCIe instead
		// of straight from VRAM; trivial copy on modern PCIe-4/5).

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
			)
			args = append(args, forceKey...)
			// HEVC: main profile, let NVENC auto-select the level from resolution.
			if a.Encoder == EncoderHEVCNVENC {
				args = append(args, "-profile:v", "main")
			}
			// AV1 NVENC requires RTX 40-series; on older cards FFmpeg
			// fails fast with "No NVENC capable device found" — that's
			// the operator's GPU detection job, not ours.
		case EncoderAMF, EncoderHEVCAMF:
			args = append(args,
				"-quality", "balanced", "-rc", "cbr",
				"-g", gopUpperBound,
				"-sc_threshold:v:0", "0",
			)
			args = append(args, forceKey...)
			if a.Encoder == EncoderHEVCAMF {
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
		case EncoderHEVCVAAPI:
			args = append(args,
				"-g", gopUpperBound,
				"-sc_threshold:v:0", "0",
				"-profile:v", "main",
			)
			args = append(args, forceKey...)
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
			// HEVC software: constrain to Main profile only. `-level-idc` is
			// an hevc_nvenc-specific option name; libx265 takes its level
			// hint via `-x265-params level=5.0` and accepts the muxer's
			// auto-derived level when unset, so we just leave it alone.
			if a.Encoder == EncoderHEVCSoftware {
				args = append(args, "-profile:v", "main")
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

	// ── HLS output ───────────────────────────────────────────────────────────
	// HEVC and AV1 output require fMP4 segments — HLS.js's MPEG-TS
	// transmuxer doesn't support either codec. fMP4 segments are passed
	// directly to MSE without transmuxing. AV1 also has no MPEG-TS
	// stream type at all, so `-c:v copy` of an AV1 source into mpegts
	// segments crashes the muxer ("Could not find tag for codec av1");
	// AV1 source remux must therefore also route through fMP4.
	isHEVCOutput := IsHEVCEncoder(a.Encoder) && !videoCopy
	isAV1Output := IsAV1Encoder(a.Encoder) && !videoCopy
	isAV1Remux := videoCopy && a.IsAV1
	needsFMP4 := isHEVCOutput || isAV1Output || isAV1Remux
	segExt := ".ts"
	segType := "mpegts"
	if needsFMP4 {
		segExt = ".m4s"
		segType = "fmp4"
	}

	segPattern := filepath.Join(a.SessionDir, a.SegmentPrefix+"%05d"+segExt)
	playlistPath := filepath.Join(a.SessionDir, "index.m3u8")
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
	switch {
	case isHEVCOutput:
		args = append(args, "-tag:v", "hvc1")
	case isAV1Output, isAV1Remux:
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
	if videoCopy {
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

	// NVENC falls through to the software-scale + zscale-tonemap path
	// below alongside AMF/QSV. The previous all-CUDA pipeline
	// (-hwaccel cuda → scale_cuda → tonemap_opencl → hevc_nvenc) was
	// fragile on mainline ffmpeg 8.x + recent NVIDIA drivers — see
	// the BuildArgs comment for the full failure-mode rundown.
	// Uniform software-decode pipeline trades a small efficiency hit
	// (NVDEC was decoding 10× cheaper than libhevc on 4K) for
	// reliability across every source file, ffmpeg version, and
	// driver release the project sees.

	// ── Software / vendor-encoder paths (NVENC + AMF + QSV + libx264) ──────
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
	//
	// AMF + QSV included here unconditionally on HDR sources: neither
	// has a vendor-specific tonemap filter in mainline ffmpeg
	// (`tonemap_amf` / `tonemap_qsv` don't exist), so the only way to
	// feed HDR HEVC into these encoders without a colorspace failure
	// is the zscale software path. Slower than the GPU pipeline but
	// the iGPUs these encoders run on aren't ABR-grade workhorses
	// anyway, and an HDR-to-SDR transcode is the rarer case.
	isAMF := a.Encoder == EncoderAMF || a.Encoder == EncoderHEVCAMF
	isQSV := a.Encoder == EncoderQSV || a.Encoder == EncoderHEVCQSV || a.Encoder == EncoderAV1QSV
	needsSoftwareTonemap := a.NeedsToneMap && a.HasZscale && (a.Encoder == EncoderSoftware || a.Encoder == EncoderHEVCSoftware || isAMF || isQSV || isNVENC)
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
	} else if a.Encoder == EncoderAMF || a.Encoder == EncoderQSV || a.Encoder == EncoderNVENC || a.Encoder == EncoderSoftware {
		// h264_amf / h264_qsv / h264_nvenc all reject 10-bit input
		// ("10-bit input video is not supported"). libx264 *accepts*
		// 10-bit input but emits 10-bit High 10 profile H.264 — valid
		// bitstream, but most browsers can't decode it (Chromium has
		// no 10-bit H.264 decoder). The tonemap chain above ends in
		// format=yuv420p and covers HDR sources; this branch handles
		// the rare 10-bit SDR source (some anime, AV1 10-bit archival
		// masters) and is a no-op on 8-bit input. HEVC variants of
		// these encoders accept 10-bit (Main10), so we don't strip
		// for them.
		filters = append(filters, "format=yuv420p")
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
//
// Windows paths (`C:\movies\...`) need an extra step: the filter parser
// treats `:` as a key=value separator inside filter args (so `C:` is
// otherwise parsed as a key), and backslashes are interpreted as escape
// introducers and stripped. Convert backslashes to forward slashes
// (ffmpeg accepts mixed separators on Windows) and escape every colon
// in the path so the parser doesn't try to interpret `C` as a key whose
// value is the rest of the path.
func subtitleBurnFilter(input string, si int) string {
	path := strings.ReplaceAll(input, `\`, `/`)
	path = strings.ReplaceAll(path, `:`, `\:`)
	path = strings.ReplaceAll(path, `'`, `\'`)
	return fmt.Sprintf("subtitles='%s':si=%d", path, si)
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
