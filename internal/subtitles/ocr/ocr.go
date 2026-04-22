// Package ocr converts image-based subtitle streams (PGS, VOBSUB, DVB) to
// WebVTT by rendering each cue to a PNG with FFmpeg and OCRing the result
// with Tesseract.
//
// The pipeline runs as a single ffprobe to enumerate cue timings, a single
// ffmpeg invocation to render every unique cue as a PNG (overlay onto a
// black canvas + mpdecimate dedup), and one tesseract call per PNG. It is
// CPU-bound and slow — minutes per movie — and is intended to be invoked
// from background workers, never from a request hot path.
package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// IsImageBased reports whether a subtitle codec is bitmap-based and thus
// requires OCR. Text-based formats (subrip/srt, ass, mov_text, webvtt) are
// already decodable directly and do not need this pipeline.
func IsImageBased(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub", "pgssub":
		return true
	}
	return false
}

// LangToTesseract maps an ISO-639-1 or ISO-639-2 language code to the
// tesseract trained-data code. Returns "eng" for unknown / empty input so
// OCR still produces output (English will misrecognize but won't crash).
func LangToTesseract(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "en", "eng":
		return "eng"
	case "es", "spa":
		return "spa"
	case "fr", "fre", "fra":
		return "fra"
	case "de", "ger", "deu":
		return "deu"
	case "it", "ita":
		return "ita"
	case "pt", "por":
		return "por"
	case "ja", "jpn":
		return "jpn"
	case "zh", "chi", "chi_sim", "zho":
		return "chi_sim"
	case "zh-tw", "chi_tra":
		return "chi_tra"
	case "ko", "kor":
		return "kor"
	case "ru", "rus":
		return "rus"
	case "ar", "ara":
		return "ara"
	}
	return "eng"
}

// Cue is one OCR'd subtitle event ready for VTT serialization.
type Cue struct {
	StartMS int64
	EndMS   int64
	Text    string
}

// Engine orchestrates ffprobe + ffmpeg + tesseract. Empty paths default to
// the bare command names, looked up via $PATH.
type Engine struct {
	FFmpegPath    string
	FFprobePath   string
	TesseractPath string
	// CanvasW/H bound the rendered overlay; defaults to 1920x1080. PGS rarely
	// exceeds 1920 wide, but 4K Blu-rays sometimes ship 3840x2160 PGS — set
	// explicitly if you've seen those.
	CanvasW int
	CanvasH int
	Logger  *slog.Logger
}

func (e *Engine) ffmpeg() string    { return def(e.FFmpegPath, "ffmpeg") }
func (e *Engine) ffprobe() string   { return def(e.FFprobePath, "ffprobe") }
func (e *Engine) tesseract() string { return def(e.TesseractPath, "tesseract") }
func (e *Engine) canvasW() int      { return defInt(e.CanvasW, 1920) }
func (e *Engine) canvasH() int      { return defInt(e.CanvasH, 1080) }
func (e *Engine) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

// Available reports whether the binaries this engine needs are on $PATH.
// Use to fail-fast at startup or surface a clear "OCR not installed" error.
func (e *Engine) Available() error {
	for _, bin := range []string{e.ffmpeg(), e.ffprobe(), e.tesseract()} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("ocr: %s not found on PATH", bin)
		}
	}
	return nil
}

// Run extracts the subtitle stream at absStreamIndex from inputPath, OCRs
// each cue, and returns the time-ordered cues. workDir is used for
// intermediate PNGs; it is created if missing and is NOT deleted — caller
// owns cleanup.
func (e *Engine) Run(ctx context.Context, inputPath string, absStreamIndex int, lang string, workDir string) ([]Cue, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, fmt.Errorf("ocr: mkdir workdir: %w", err)
	}

	events, err := e.probeEvents(ctx, inputPath, absStreamIndex)
	if err != nil {
		return nil, fmt.Errorf("ocr: probe events: %w", err)
	}
	if len(events) == 0 {
		return nil, nil
	}
	e.logger().DebugContext(ctx, "ocr: probed events", "count", len(events), "stream", absStreamIndex)

	if err := e.renderFrames(ctx, inputPath, absStreamIndex, workDir); err != nil {
		return nil, fmt.Errorf("ocr: render frames: %w", err)
	}

	pngs, err := filepath.Glob(filepath.Join(workDir, "*.png"))
	if err != nil {
		return nil, fmt.Errorf("ocr: glob pngs: %w", err)
	}
	sort.Strings(pngs)
	if len(pngs) == 0 {
		return nil, nil
	}
	e.logger().DebugContext(ctx, "ocr: rendered frames", "count", len(pngs), "events", len(events))

	tessLang := LangToTesseract(lang)
	cues := make([]Cue, 0, len(pngs))
	maxN := len(pngs)
	if len(events) < maxN {
		// Trust event count — mpdecimate occasionally lets a small ringing
		// frame slip through; chopping the tail avoids skewing every cue
		// after the divergence.
		maxN = len(events)
	}
	for i := 0; i < maxN; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		text, err := e.runTesseract(ctx, pngs[i], tessLang)
		if err != nil {
			e.logger().WarnContext(ctx, "ocr: tesseract failed", "png", pngs[i], "err", err)
			continue
		}
		text = cleanText(text)
		if text == "" {
			continue
		}
		ev := events[i]
		cues = append(cues, Cue{StartMS: ev.StartMS, EndMS: ev.EndMS, Text: text})
	}

	return cues, nil
}

// subEvent is one entry from ffprobe -show_packets for the subtitle stream.
type subEvent struct {
	StartMS int64
	EndMS   int64
}

func (e *Engine) probeEvents(ctx context.Context, input string, absIdx int) ([]subEvent, error) {
	cmd := exec.CommandContext(ctx, e.ffprobe(),
		"-v", "quiet",
		"-of", "json",
		"-show_packets",
		"-select_streams", strconv.Itoa(absIdx),
		input,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var doc struct {
		Packets []struct {
			PTSTime      string `json:"pts_time"`
			DurationTime string `json:"duration_time"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("parse ffprobe: %w", err)
	}
	events := make([]subEvent, 0, len(doc.Packets))
	for _, p := range doc.Packets {
		startSec, err := strconv.ParseFloat(p.PTSTime, 64)
		if err != nil || startSec < 0 {
			continue
		}
		durSec, _ := strconv.ParseFloat(p.DurationTime, 64)
		if durSec <= 0 {
			// PGS often lacks duration; default to 4 s, which is a
			// typical sub-event length. It will be clamped below by the
			// next event's start so this only affects the final cue.
			durSec = 4.0
		}
		events = append(events, subEvent{
			StartMS: int64(startSec * 1000),
			EndMS:   int64((startSec + durSec) * 1000),
		})
	}
	// Sort by start (already in order from ffprobe but be defensive) and
	// clamp each event's end to the next event's start so cues never overlap.
	sort.Slice(events, func(i, j int) bool { return events[i].StartMS < events[j].StartMS })
	for i := 0; i < len(events)-1; i++ {
		if events[i].EndMS > events[i+1].StartMS {
			events[i].EndMS = events[i+1].StartMS
		}
	}
	return events, nil
}

func (e *Engine) renderFrames(ctx context.Context, input string, absIdx int, outDir string) error {
	// Render the subtitle stream onto a fixed-size black canvas, then use
	// mpdecimate to drop near-identical frames. The strict thresholds
	// (hi/lo/frac) cull anything but a real subtitle change. -fix_sub_duration
	// helps PGS where end timestamps are encoded out-of-band.
	filter := fmt.Sprintf(
		"color=c=black:s=%dx%d,format=yuv420p[bg];[bg][0:%d]overlay,mpdecimate=hi=64*32:lo=8*32:frac=0.001",
		e.canvasW(), e.canvasH(), absIdx,
	)
	pattern := filepath.Join(outDir, "frame_%05d.png")

	args := []string{
		"-nostdin", "-y",
		"-hide_banner", "-loglevel", "warning",
		"-fix_sub_duration",
		"-i", input,
		"-filter_complex", filter,
		"-vsync", "vfr",
		"-an", "-sn",
		pattern,
	}
	cmd := exec.CommandContext(ctx, e.ffmpeg(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg render: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (e *Engine) runTesseract(ctx context.Context, pngPath, lang string) (string, error) {
	// `tesseract input output -l lang` writes output.txt; using "stdout" as
	// the output sink avoids the temp file dance.
	cmd := exec.CommandContext(ctx, e.tesseract(), pngPath, "stdout", "-l", lang)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// cleanText normalizes Tesseract output: collapse whitespace, drop empty
// lines, fold to at most a handful of lines per cue (PGS rarely shows more
// than 3). Bilingual / multi-line subs survive intact.
func cleanText(raw string) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// CuesToVTT serializes cues to a WebVTT byte slice. Cues with empty text
// or non-positive duration are dropped silently.
func CuesToVTT(cues []Cue) []byte {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for i, c := range cues {
		if c.Text == "" || c.EndMS <= c.StartMS {
			continue
		}
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n",
			i+1,
			vttTime(c.StartMS),
			vttTime(c.EndMS),
			c.Text,
		)
	}
	return []byte(b.String())
}

func vttTime(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	d := time.Duration(ms) * time.Millisecond
	h := int(d / time.Hour)
	d -= time.Duration(h) * time.Hour
	m := int(d / time.Minute)
	d -= time.Duration(m) * time.Minute
	s := int(d / time.Second)
	d -= time.Duration(s) * time.Second
	mmm := int(d / time.Millisecond)
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, mmm)
}

// ErrNoStream is returned when the requested stream index is missing or
// not bitmap-based.
var ErrNoStream = errors.New("ocr: subtitle stream not found or not image-based")

func def(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func defInt(n, fallback int) int {
	if n == 0 {
		return fallback
	}
	return n
}
