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
	EncoderVAAPI    Encoder = "h264_vaapi"
	EncoderQSV      Encoder = "h264_qsv"
	EncoderSoftware Encoder = "libx264"
)

// BuildArgs holds the inputs needed to construct an FFmpeg HLS transcode command.
type BuildArgs struct {
	// Input
	InputPath   string
	StartOffset float64 // seconds (seek to this position)

	// Video
	Encoder     Encoder
	Width       int
	Height      int
	BitrateKbps int
	NeedsToneMap bool // HDR→SDR tone mapping (ADR-030)
	IsVAAPI      bool // VAAPI needs hwupload filter

	// Audio (ADR-018)
	AudioCodec       string // "copy" | "aac"
	AudioChannels    int    // 0 = keep source
	AudioBitrateKbps int    // 0 = auto
	AudioStreamIndex int    // -1 = default (first); >= 0 = specific stream index

	// Subtitles
	ExtractSubtitles bool
	SubtitleStreams   []int // stream indices to extract as WebVTT

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
	videoCopy := a.Encoder == "copy"

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
	}

	// Seek to start position (fast input seek for keyframe alignment).
	if a.StartOffset > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", a.StartOffset))
	}

	if !videoCopy {
		// VAAPI init filter (must come before input for hardware decode).
		if a.IsVAAPI {
			args = append(args, "-vaapi_device", "/dev/dri/renderD128")
		}

		// NVENC: enable CUDA hardware decode (CUVID) when available; falls back to
		// software decode transparently. hwupload_cuda in the filter chain handles
		// the CPU→GPU copy when software decode is used.
		if a.Encoder == EncoderNVENC {
			args = append(args, "-hwaccel", "cuda")
		}
	}

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
		args = append(args,
			"-c:v", string(a.Encoder),
			"-b:v", fmt.Sprintf("%dk", a.BitrateKbps),
			"-maxrate", fmt.Sprintf("%dk", a.BitrateKbps),
			"-bufsize", fmt.Sprintf("%dk", a.BitrateKbps*2),
		)

		// NVENC-specific flags for streaming quality.
		if a.Encoder == EncoderNVENC {
			args = append(args, "-preset", "p4", "-tune", "ll")
		}

		// Force keyframes at segment boundaries for correct HLS seeking.
		args = append(args,
			"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", SegmentDuration),
			"-sc_threshold", "0",
		)
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
	segPattern := filepath.Join(a.SessionDir, a.SegmentPrefix+"%05d.ts")
	playlistPath := filepath.Join(a.SessionDir, "index.m3u8")

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
		"-f", "hls",
		"-hls_time", fmt.Sprint(SegmentDuration),
		"-hls_list_size", "0", // keep all segments in playlist
		"-hls_segment_type", "mpegts",
		"-hls_flags", hlsFlags,
		"-hls_segment_filename", segPattern,
		"-hls_delete_threshold", fmt.Sprint(deleteThreshold),
	)
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

	if a.IsVAAPI {
		filters = append(filters, "format=nv12", "hwupload")
	}

	// Scale to target resolution, maintaining aspect ratio.
	if a.Width > 0 && a.Height > 0 {
		switch {
		case a.Encoder == EncoderNVENC:
			// GPU-side scaling via NPP: upload frames to CUDA memory, then scale.
			// Caller pre-calculates AR-correct dimensions so no pad is needed.
			filters = append(filters, "hwupload_cuda",
				fmt.Sprintf("scale_npp=%d:%d:force_original_aspect_ratio=decrease", a.Width, a.Height))
		case a.Encoder == EncoderVAAPI:
			filters = append(filters, fmt.Sprintf("scale_vaapi=w=%d:h=%d:force_original_aspect_ratio=decrease", a.Width, a.Height))
		default:
			filters = append(filters, fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", a.Width, a.Height, a.Width, a.Height))
		}
	}

	// HDR→SDR tone mapping (ADR-030). Applied before scale for software, after for VAAPI.
	if a.NeedsToneMap && a.Encoder == EncoderSoftware {
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

	return strings.Join(filters, ",")
}
