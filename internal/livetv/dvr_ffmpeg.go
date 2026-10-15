package livetv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onscreen/onscreen/internal/ffsafe"
)

// mediaInfo is the slice of ffprobe output post-processing needs: enough to
// verify an output against its source and to refresh the media_files row.
type mediaInfo struct {
	DurationSec float64
	FormatName  string // first token of format_name ("matroska", "mov", …)
	VideoCodec  string
	AudioCodec  string
	HasVideo    bool
	HasAudio    bool
}

// inputFormatFor names the demuxer for one of the DVR's own files, so ffmpeg
// never content-sniffs it into a reference-following format. "" = let ffmpeg
// probe (an extension we don't write).
func inputFormatFor(path string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSuffix(path, partSuffix))) {
	case ".mp4", ".m4v", ".mov":
		return "mov"
	case ".mkv":
		return "matroska"
	case ".ts", ".m2ts":
		return "mpegts"
	}
	return ""
}

// inputArgs returns the guarded input options + "-i path": the forced demuxer
// when known and the ffsafe protocol allowlist, both immediately before -i.
func inputArgs(path string) []string {
	var args []string
	if f := inputFormatFor(path); f != "" {
		args = append(args, "-f", f)
	}
	args = append(args, ffsafe.InputProtocolArgs(path)...)
	return append(args, "-i", path)
}

// probeMedia runs ffprobe for duration, container and the first video/audio
// codecs.
func (p *PostProcessor) probeMedia(ctx context.Context, path string) (mediaInfo, error) {
	args := []string{"-v", "error"}
	if f := inputFormatFor(path); f != "" {
		args = append(args, "-f", f)
	}
	args = append(args, ffsafe.InputProtocolArgs(path)...)
	args = append(args,
		"-show_entries", "format=duration,format_name:stream=codec_type,codec_name",
		"-of", "json", "-i", path)
	cmd := exec.CommandContext(ctx, p.cfg.FFprobeBin, args...)
	var stderr cappedBuffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return mediaInfo{}, fmt.Errorf("ffprobe: %w: %s", err, lastLine(stderr.String()))
	}
	return parseProbeJSON(out)
}

func parseProbeJSON(b []byte) (mediaInfo, error) {
	var raw struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
		Format struct {
			Duration   string `json:"duration"`
			FormatName string `json:"format_name"`
		} `json:"format"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return mediaInfo{}, fmt.Errorf("parse ffprobe: %w", err)
	}
	var mi mediaInfo
	if d, err := strconv.ParseFloat(raw.Format.Duration, 64); err == nil && !math.IsNaN(d) {
		mi.DurationSec = d
	}
	mi.FormatName = strings.SplitN(raw.Format.FormatName, ",", 2)[0]
	for _, s := range raw.Streams {
		switch s.CodecType {
		case "video":
			if !mi.HasVideo {
				mi.HasVideo, mi.VideoCodec = true, s.CodecName
			}
		case "audio":
			if !mi.HasAudio {
				mi.HasAudio, mi.AudioCodec = true, s.CodecName
			}
		}
	}
	return mi, nil
}

// durationTolerance is how far an output's duration may drift from what it
// should be before the output is rejected (2%).
const durationTolerance = 0.02

// verifyOutput checks a processed file before anything is swapped or deleted:
// its duration is within 2% of expectedSec, and every stream kind the source
// had is still there.
func verifyOutput(src, out mediaInfo, expectedSec float64) error {
	if out.DurationSec <= 0 {
		return fmt.Errorf("output has no duration")
	}
	if expectedSec > 0 && math.Abs(out.DurationSec-expectedSec) > expectedSec*durationTolerance {
		return fmt.Errorf("output runs %.1fs, expected %.1fs", out.DurationSec, expectedSec)
	}
	if src.HasVideo && !out.HasVideo {
		return fmt.Errorf("output lost its video stream")
	}
	if src.HasAudio && !out.HasAudio {
		return fmt.Errorf("output lost its audio stream")
	}
	return nil
}

// containerFormat maps the setting to ffmpeg's muxer name and the file
// extension. Unknown values fall back to Matroska, the default.
func containerFormat(c string) (muxer, ext string) {
	if c == "mp4" {
		return "mp4", ".mp4"
	}
	return "matroska", ".mkv"
}

// outputArgs are the muxer options for the chosen container.
func outputArgs(container, out string) []string {
	muxer, _ := containerFormat(container)
	args := []string{"-avoid_negative_ts", "make_zero"}
	if muxer == "mp4" {
		args = append(args, "-movflags", "+faststart")
	}
	return append(args, "-f", muxer, out)
}

// remuxArgs: stream copy into the new container, regenerating timestamps so
// the result seeks cleanly.
func remuxArgs(src, out, container string) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-fflags", "+genpts"}
	args = append(args, inputArgs(src)...)
	args = append(args, "-map", "0:v:0", "-map", "0:a?", "-dn", "-sn", "-c", "copy")
	return append(args, outputArgs(container, out)...)
}

// crfFor maps the quality preset to a constant-quality value for the codec.
// HEVC reaches the same visual quality at a higher number.
func crfFor(codec, quality string) int {
	base := map[string]int{"high": 20, "balanced": 23, "small": 26}[quality]
	if base == 0 {
		base = 23
	}
	if codec == "hevc" {
		base += 2
	}
	return base
}

// softwareEncoder is the always-available fallback for a codec.
func softwareEncoder(codec string) string {
	if codec == "hevc" {
		return "libx265"
	}
	return "libx264"
}

// encoderCandidates orders the encoders to try for codec: the server's active
// hardware encoders for that codec first (in the server's own order), then
// the software encoder. VAAPI is skipped: it needs a device opened up front,
// which the plain one-file pipeline here doesn't set up.
func encoderCandidates(codec string, active []string) []string {
	prefix := "h264_"
	if codec == "hevc" {
		prefix = "hevc_"
	}
	var out []string
	for _, e := range active {
		if !strings.HasPrefix(e, prefix) || strings.HasSuffix(e, "_vaapi") {
			continue
		}
		switch strings.TrimPrefix(e, prefix) {
		case "nvenc", "qsv", "amf":
			out = append(out, e)
		}
	}
	return append(out, softwareEncoder(codec))
}

// videoEncodeArgs are the per-encoder constant-quality flags.
func videoEncodeArgs(enc, codec, quality string) []string {
	q := strconv.Itoa(crfFor(codec, quality))
	switch {
	case strings.HasSuffix(enc, "_nvenc"):
		return []string{"-c:v", enc, "-preset", "p5", "-rc", "vbr", "-cq", q, "-b:v", "0", "-pix_fmt", "yuv420p"}
	case strings.HasSuffix(enc, "_qsv"):
		return []string{"-c:v", enc, "-preset", "medium", "-global_quality", q, "-pix_fmt", "nv12"}
	case strings.HasSuffix(enc, "_amf"):
		args := []string{"-c:v", enc, "-quality", "balanced", "-rc", "cqp", "-qp_i", q, "-qp_p", q}
		if codec == "h264" {
			args = append(args, "-qp_b", q)
		}
		return append(args, "-pix_fmt", "yuv420p")
	default:
		return []string{"-c:v", enc, "-preset", "medium", "-crf", q, "-pix_fmt", "yuv420p"}
	}
}

// transcodeArgs re-encodes video (deinterlacing only frames flagged as
// interlaced, which broadcast 1080i is) and copies audio.
func transcodeArgs(src, out, container, enc, codec, quality string) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-fflags", "+genpts"}
	args = append(args, inputArgs(src)...)
	args = append(args, "-map", "0:v:0", "-map", "0:a?", "-dn", "-sn",
		"-vf", "bwdif=mode=send_frame:deint=interlaced")
	args = append(args, videoEncodeArgs(enc, codec, quality)...)
	if muxer, _ := containerFormat(container); muxer == "mp4" && codec == "hevc" {
		// Apple players only accept HEVC in MP4 under the hvc1 tag.
		args = append(args, "-tag:v", "hvc1")
	}
	args = append(args, "-c:a", "copy")
	return append(args, outputArgs(container, out)...)
}

// cutArgs stream-copies the keep segments of src (listed in a server-written
// concat script at listPath) into out. With stream copy each segment starts at
// the keyframe at or before its in-point, so a cut errs toward keeping a
// moment of the ad rather than losing programme.
func cutArgs(parts []ffsafe.ConcatPart, listPath, out, container string) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}
	args = append(args, ffsafe.ConcatInputArgs(parts)...)
	args = append(args, "-i", listPath, "-map", "0:v:0", "-map", "0:a?", "-dn", "-sn", "-c", "copy")
	return append(args, outputArgs(container, out)...)
}

// runFFmpeg runs ffmpeg with argv (never a shell) and folds the tail of its
// stderr into the error.
func (p *PostProcessor) runFFmpeg(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, p.cfg.FFmpegBin, args...)
	var stderr cappedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, lastLine(stderr.String()))
	}
	return nil
}

// writeCutList writes the concat script for a cut into a private temp dir.
// Returns the parts (for ConcatInputArgs), the list path and a cleanup func.
func writeCutList(src string, keep []timeSpan) ([]ffsafe.ConcatPart, string, func(), error) {
	dir, err := os.MkdirTemp("", "onscreen-dvrcut-")
	if err != nil {
		return nil, "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	abs, err := filepath.Abs(src)
	if err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	parts := make([]ffsafe.ConcatPart, 0, len(keep))
	for _, k := range keep {
		parts = append(parts, ffsafe.ConcatPart{Input: abs, InPointSec: k.start, OutPointSec: k.end})
	}
	listPath := filepath.Join(dir, "cut.ffconcat")
	if err := ffsafe.WriteConcatList(listPath, parts); err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	return parts, listPath, cleanup, nil
}

// cappedBuffer keeps the first 64 KiB written to it — enough for an error
// tail or a script's log without letting a chatty process grow memory.
type cappedBuffer struct{ bytes.Buffer }

const cappedBufferMax = 64 * 1024

func (c *cappedBuffer) Write(b []byte) (int, error) {
	if room := cappedBufferMax - c.Len(); room > 0 {
		if len(b) > room {
			c.Buffer.Write(b[:room])
		} else {
			c.Buffer.Write(b)
		}
	}
	return len(b), nil
}

// lastLine returns the last non-empty line of s, trimmed to 300 bytes, for
// error messages shown on the recordings page.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := strings.TrimSpace(lines[len(lines)-1])
	if len(l) > 300 {
		l = l[:300]
	}
	return l
}
