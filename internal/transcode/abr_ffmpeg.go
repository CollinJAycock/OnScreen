package transcode

import (
	"fmt"
	"path/filepath"
	"strings"
)

// BuildABRHLS builds the ffmpeg argv for a SINGLE-PROCESS adaptive-bitrate
// HLS ladder: decode once, split into N branches, scale + encode each
// rung, and let ffmpeg's HLS muxer emit the master playlist, the
// per-variant media playlists, and the segments via -var_stream_map.
// This is the "shared decode" orchestration chosen for Track A — one
// ffmpeg per session, not N.
//
// SCOPE: software H.264 (libx264) + AAC + MPEG-TS only — the
// universally-compatible, driver-independent path. Hardware-encoder
// ladders (NVENC/QSV/VAAPI/AMF: split → per-branch hwupload/scale_* →
// per-branch encode) and HEVC/AV1 fMP4 renditions are a deliberate
// follow-up: their filtergraphs are exactly the part the roadmap flags
// for real-hardware benchmarking.
//
// ⚠ NOT YET VALIDATED against a live ffmpeg. The filtergraph wiring, the
// -var_stream_map arity, and the %v per-variant segment paths need an
// on-box soak with real media before this is wired into the worker. The
// unit tests guard the arg STRUCTURE (map/var-map arity, master name,
// per-variant paths), not playability. The worker must also pre-create
// the N variant subdirs (ffmpeg's %v does not mkdir them).
//
// ladder must be non-empty (use BuildLadder). a.SessionDir is the
// absolute session directory; segments land in {SessionDir}/{i}/ and the
// master at {SessionDir}/master.m3u8.
func BuildABRHLS(a BuildArgs, ladder []Rendition) []string {
	opts := a.EncoderOpts
	if opts.MaxrateRatio <= 0 {
		opts.MaxrateRatio = 1.5
	}

	args := []string{"-hide_banner", "-loglevel", "warning"}
	if a.StartOffset > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", a.StartOffset))
	}
	if a.ReadRate > 0 {
		args = append(args, "-readrate", fmt.Sprintf("%.2f", a.ReadRate))
		if a.ReadRateInitialBurst > 0 {
			args = append(args, "-readrate_initial_burst", fmt.Sprint(a.ReadRateInitialBurst))
		}
	}
	args = append(args, "-i", a.InputPath)

	// ── Filtergraph: (optional tonemap) → split → per-rung scale ──────────────
	src := "0:v"
	var chains []string
	if a.NeedsToneMap {
		// Same software HDR→SDR chain the single-rendition path uses,
		// applied once before the split so every rung inherits SDR.
		chains = append(chains, "["+src+"]"+softwareTonemapChain+"[tm]")
		src = "tm"
	}
	labels := make([]string, len(ladder))
	for i := range ladder {
		labels[i] = fmt.Sprintf("s%d", i)
	}
	chains = append(chains, fmt.Sprintf("[%s]split=%d[%s]", src, len(ladder), strings.Join(labels, "][")))
	for i, r := range ladder {
		chains = append(chains, fmt.Sprintf("[%s]scale=%d:%d:flags=lanczos,format=yuv420p[vo%d]", labels[i], r.Width, r.Height, i))
	}
	args = append(args, "-filter_complex", strings.Join(chains, ";"))

	// ── Per-rung stream mapping (video out i + the chosen audio) ──────────────
	audioSel := "0:a:0"
	if a.AudioStreamIndex >= 0 {
		audioSel = fmt.Sprintf("0:a:%d", a.AudioStreamIndex)
	}
	for i := range ladder {
		args = append(args, "-map", fmt.Sprintf("[vo%d]", i), "-map", audioSel)
	}

	// ── Video encode (libx264, per-output bitrate) ────────────────────────────
	args = append(args, "-c:v", "libx264", "-preset", "veryfast")
	for i, r := range ladder {
		maxrate := int(float64(r.BitrateKbps) * opts.MaxrateRatio)
		args = append(args,
			fmt.Sprintf("-b:v:%d", i), fmt.Sprintf("%dk", r.BitrateKbps),
			fmt.Sprintf("-maxrate:v:%d", i), fmt.Sprintf("%dk", maxrate),
			fmt.Sprintf("-bufsize:v:%d", i), fmt.Sprintf("%dk", r.BitrateKbps*2),
		)
	}
	// 4 s keyframe cadence so segments cut cleanly on every rung.
	args = append(args,
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", SegmentDuration),
		"-sc_threshold", "0",
		"-g", fmt.Sprint(SegmentDuration*30),
	)

	// ── Audio (AAC, shared settings across rungs) ─────────────────────────────
	channels := a.AudioChannels
	if channels <= 0 {
		channels = 2
	}
	audioBitrate := a.AudioBitrateKbps
	if audioBitrate <= 0 {
		audioBitrate = 128
	}
	args = append(args, "-c:a", "aac", "-ac", fmt.Sprint(channels), "-b:a", fmt.Sprintf("%dk", audioBitrate))

	// ── HLS muxer with var_stream_map ─────────────────────────────────────────
	// One "v:i,a:i" group per rung → ffmpeg writes {i}/index.m3u8 +
	// {i}/seg*.ts and the master listing all variants.
	groups := make([]string, len(ladder))
	for i := range ladder {
		groups[i] = fmt.Sprintf("v:%d,a:%d", i, i)
	}
	args = append(args,
		"-f", "hls",
		"-hls_time", fmt.Sprint(SegmentDuration),
		"-hls_list_size", "0",
		"-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments+delete_segments",
		"-hls_delete_threshold", "30",
		"-master_pl_name", "master.m3u8",
		"-var_stream_map", strings.Join(groups, " "),
		"-hls_segment_filename", filepath.Join(a.SessionDir, "%v", "seg%05d.ts"),
		filepath.Join(a.SessionDir, "%v", "index.m3u8"),
	)
	return args
}

// softwareTonemapChain is the libavfilter HDR→SDR chain (zscale-linear →
// hable tonemap → bt709 → yuv420p) shared with the single-rendition
// software path. Applied once before the ABR split.
const softwareTonemapChain = "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"
