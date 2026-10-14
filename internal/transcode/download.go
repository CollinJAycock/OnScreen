package transcode

import (
	"fmt"
	"strings"

	"github.com/onscreen/onscreen/internal/ffsafe"
)

// ── Converted downloads ───────────────────────────────────────────────────────
//
// A converted download is the whole movie or episode encoded once into a
// single progressive MP4 (H.264 + stereo AAC, moov atom up front) at one rung
// of the Profiles ladder, for a user who wants to take it offline at a size
// their connection or device can handle. Unlike a playback session there is
// no real-time pacing, no segmenting and no seeking: ffmpeg runs start to
// finish as fast as the encoder allows, and the file is served only once it
// is complete. internal/downloads runs these; this file builds the argv.
//
// Decode is always software and frames stay in system memory: the zero-copy
// VRAM chains BuildHLS uses are probe-gated per worker and tuned for live
// segment latency, which a one-shot file encode doesn't need. The encoder can
// still be hardware — the server's configured H.264 encoder — with a software
// libx264 retry when it fails (see internal/downloads).

// ProgressiveArgs describes one converted-download encode.
type ProgressiveArgs struct {
	// InputPath is the source file, or for a stacked movie the server-written
	// concat list naming ConcatParts (opened through ffsafe.ConcatInputArgs).
	InputPath   string
	ConcatParts []ffsafe.ConcatPart
	// OutputPath is where the MP4 is written. ffmpeg is told the container
	// explicitly (-f mp4), so the name needn't end in .mp4.
	OutputPath string

	Encoder     Encoder
	BitrateKbps int
	// MaxWidth × MaxHeight is the rung's box. The output fits inside it at
	// the source's aspect ratio and is never larger than the source.
	MaxWidth, MaxHeight int
	// SourceWidth × SourceHeight are the probed source dimensions; 0 when
	// unknown, in which case ffmpeg works the size out itself.
	SourceWidth, SourceHeight int
	// NeedsToneMap converts an HDR source to SDR (zscale + hable, on the
	// CPU) when HasZscale says the filter exists; without zscale an HDR
	// source is encoded as-is (washed out, but playable).
	NeedsToneMap bool
	HasZscale    bool
	// NoAudio drops the audio map for a source with no audio stream. When
	// false the first audio stream is mapped optionally, so a source whose
	// stream list is unknown still converts.
	NoAudio bool
	// TextSubtitleStreams are ABSOLUTE ffprobe stream indexes of text
	// subtitle streams to carry as mov_text (MP4's text subtitle format).
	// Image subtitles (PGS, VobSub) have no MP4 text form and are skipped by
	// the caller; nothing is ever burned in.
	TextSubtitleStreams []int

	EncoderOpts EncoderOpts
}

// DownloadAudioBitrateKbps is the stereo AAC bitrate of a converted download.
const DownloadAudioBitrateKbps = 160

// FitWithin returns the largest even dimensions that fit srcW × srcH inside
// maxW × maxH at the source's aspect ratio without upscaling. Encoders need
// even dimensions, so an odd source is rounded down a pixel. Returns 0, 0
// when the source size is unknown.
func FitWithin(srcW, srcH, maxW, maxH int) (int, int) {
	if srcW <= 0 || srcH <= 0 {
		return 0, 0
	}
	w, h := float64(srcW), float64(srcH)
	scale := 1.0
	if maxW > 0 && w*scale > float64(maxW) {
		scale = float64(maxW) / w
	}
	if maxH > 0 && h*scale > float64(maxH) {
		scale = float64(maxH) / h
	}
	ow := int(w*scale + 0.5)
	oh := int(h*scale + 0.5)
	if maxW > 0 && ow > maxW {
		ow = maxW
	}
	if maxH > 0 && oh > maxH {
		oh = maxH
	}
	ow -= ow % 2
	oh -= oh % 2
	if ow < 2 {
		ow = 2
	}
	if oh < 2 {
		oh = 2
	}
	return ow, oh
}

// progressiveVideoFilter is the -vf chain: scale to the rung (CPU), tonemap
// HDR when it can, then hand the encoder 8-bit 4:2:0 — uploaded to a VA
// surface for VAAPI, plain yuv420p for every other encoder (the hardware
// H.264 encoders reject 10-bit input, and browsers can't play High 10).
func progressiveVideoFilter(a ProgressiveArgs) string {
	var f []string
	w, h := FitWithin(a.SourceWidth, a.SourceHeight, a.MaxWidth, a.MaxHeight)
	switch {
	case w > 0 && h > 0:
		if w != a.SourceWidth || h != a.SourceHeight {
			f = append(f, fmt.Sprintf("scale=%d:%d", w, h))
		}
	case a.MaxWidth > 0 && a.MaxHeight > 0:
		f = append(f, fmt.Sprintf(
			"scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease:force_divisible_by=2",
			a.MaxWidth, a.MaxHeight))
	}
	tonemap := a.NeedsToneMap && a.HasZscale
	if tonemap {
		f = append(f,
			"zscale=t=linear:npl=100",
			"format=gbrpf32le",
			"zscale=p=bt709",
			"tonemap=tonemap=hable:desat=0",
			"zscale=t=bt709:m=bt709:r=tv",
		)
	}
	if IsVAAPIEncoder(a.Encoder) {
		f = append(f, "format=nv12", "hwupload")
	} else {
		f = append(f, "format=yuv420p")
	}
	return strings.Join(f, ",")
}

// BuildProgressiveMP4 constructs the ffmpeg argv for a converted download.
// Progress is written to stdout (-progress pipe:1) as key=value lines; the
// caller reads out_time_us against the source duration.
func BuildProgressiveMP4(a ProgressiveArgs) []string {
	opts := a.EncoderOpts
	if opts.NVENCPreset == "" {
		opts.NVENCPreset = "p4"
	}
	if opts.NVENCTune == "" {
		opts.NVENCTune = "hq"
	}
	if opts.MaxrateRatio <= 0 {
		opts.MaxrateRatio = 1.5
	}
	enc := a.Encoder
	if enc == "" || IsVideoCopy(enc) || IsHEVCEncoder(enc) || IsAV1Encoder(enc) {
		enc = EncoderSoftware
	}
	a.Encoder = enc

	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}
	if IsVAAPIEncoder(enc) {
		args = append(args, "-vaapi_device", "/dev/dri/renderD128")
	}
	// Input options guarding -i: the protocol allowlist, or the forced concat
	// demuxer for a stacked movie's server-written list. See internal/ffsafe.
	args = append(args, inputArgs(a.InputPath, a.ConcatParts)...)
	args = append(args, "-i", a.InputPath)

	args = append(args, "-map", "0:v:0")
	if !a.NoAudio {
		args = append(args, "-map", "0:a:0?")
	}
	for _, idx := range a.TextSubtitleStreams {
		args = append(args, "-map", fmt.Sprintf("0:%d", idx))
	}

	if vf := progressiveVideoFilter(a); vf != "" {
		args = append(args, "-vf", vf)
	}
	maxrate := int(float64(a.BitrateKbps) * opts.MaxrateRatio)
	args = append(args,
		"-c:v", string(enc),
		"-b:v", fmt.Sprintf("%dk", a.BitrateKbps),
		"-maxrate", fmt.Sprintf("%dk", maxrate),
		"-bufsize", fmt.Sprintf("%dk", maxrate*2),
	)
	switch enc {
	case EncoderNVENC:
		args = append(args, "-preset", opts.NVENCPreset, "-tune", opts.NVENCTune, "-rc", "vbr", "-profile:v", "high")
	case EncoderQSV:
		args = append(args, "-preset", "medium", "-profile:v", "high")
	case EncoderAMF:
		args = append(args, "-quality", "balanced", "-rc", "vbr_peak", "-profile:v", "high")
	case EncoderVAAPI:
		args = append(args, "-profile:v", "high")
	default:
		// libx264: "fast" is the usual offline trade — noticeably better
		// than the live path's veryfast/ultrafast at the same bitrate, still
		// several times real time at 1080p on a desktop CPU.
		args = append(args, "-preset", "fast", "-profile:v", "high")
	}

	if !a.NoAudio {
		args = append(args,
			"-c:a", "aac",
			"-ac", "2",
			"-b:a", fmt.Sprintf("%dk", DownloadAudioBitrateKbps),
		)
	}
	if len(a.TextSubtitleStreams) > 0 {
		args = append(args, "-c:s", "mov_text")
	}

	args = append(args,
		"-movflags", "+faststart",
		"-progress", "pipe:1",
		"-nostats",
		"-f", "mp4",
		a.OutputPath,
	)
	return args
}

// IsTextSubtitleCodec reports whether an ffprobe subtitle codec name is a
// text format ffmpeg can convert to mov_text. Image formats (PGS, VobSub,
// DVB) are not.
func IsTextSubtitleCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "subrip", "srt", "ass", "ssa", "mov_text", "webvtt", "text":
		return true
	}
	return false
}
