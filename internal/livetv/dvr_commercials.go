package livetv

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CommercialMode is the DVR setting for commercial handling.
type CommercialMode string

const (
	// CommercialsOff never looks for commercials (the default).
	CommercialsOff CommercialMode = "off"
	// CommercialsMark stores the breaks so players can offer "Skip ad".
	CommercialsMark CommercialMode = "mark"
	// CommercialsCut removes the breaks from the file during post-processing.
	// Only effective when the file is being rewritten anyway (remux or
	// transcode); otherwise it behaves like mark.
	CommercialsCut CommercialMode = "cut"
)

// Commercial marker sources, as stored in commercial_markers.source.
const (
	CommercialSourceComskip   = "comskip"
	CommercialSourceHeuristic = "heuristic"
)

// CommercialBreak is one detected ad break, in milliseconds from the start of
// the file it was detected in.
type CommercialBreak struct {
	StartMS int64
	EndMS   int64
}

// HeuristicParams tunes the built-in black + silence detector. The defaults
// are deliberately conservative: skipping a slice of the programme is a worse
// failure than leaving an ad in, so every rule below errs toward "not an ad".
type HeuristicParams struct {
	// MinOverlap is how long black video and silent audio must coincide for
	// the moment to count as a break between two pieces of content.
	MinOverlap time.Duration
	// MergeGap joins break points closer than this into one (a flicker of
	// picture between two black runs is still one transition).
	MergeGap time.Duration
	// MaxAdLen is the longest a single segment between breaks may be and
	// still be an advert.
	MaxAdLen time.Duration
	// MinPodLen / MaxPodLen bound a whole ad break (the run of adverts).
	MinPodLen time.Duration
	MaxPodLen time.Duration
	// MinAdsPerPod: a lone short segment between two breaks is as likely a
	// title sequence or a fade-out scene as an advert, so a pod needs at
	// least this many segments.
	MinAdsPerPod int
	// MinProgramLen is how long the segments either side of a pod must be.
	// A pod at the very start or end of the recording (no programme on one
	// side) is never marked: the padding there is as likely the neighbouring
	// show as an ad.
	MinProgramLen time.Duration
	// BlackPixelThreshold / BlackPictureRatio / SilenceNoiseDB feed ffmpeg's
	// blackdetect (pix_th, pic_th) and silencedetect (noise).
	BlackPixelThreshold float64
	BlackPictureRatio   float64
	SilenceNoiseDB      float64
}

// DefaultHeuristicParams are the production settings: adverts of up to two
// minutes, breaks of 30 s to 6 min, at least two adverts per break, and three
// minutes of programme on each side.
func DefaultHeuristicParams() HeuristicParams {
	return HeuristicParams{
		MinOverlap:          100 * time.Millisecond,
		MergeGap:            time.Second,
		MaxAdLen:            2 * time.Minute,
		MinPodLen:           30 * time.Second,
		MaxPodLen:           6 * time.Minute,
		MinAdsPerPod:        2,
		MinProgramLen:       3 * time.Minute,
		BlackPixelThreshold: 0.10,
		BlackPictureRatio:   0.98,
		SilenceNoiseDB:      -50,
	}
}

// timeSpan is a [start, end) interval in seconds.
type timeSpan struct{ start, end float64 }

// detectCommercials runs comskip when it is configured, otherwise the
// built-in heuristic. Returns the breaks and the source label to store them
// under.
func (p *PostProcessor) detectCommercials(ctx context.Context, path string, info mediaInfo) ([]CommercialBreak, string, error) {
	if p.cfg.ComskipPath != "" {
		breaks, err := runComskip(ctx, p.cfg.ComskipPath, p.cfg.ComskipINI, path, info.DurationSec)
		return breaks, CommercialSourceComskip, err
	}
	breaks, err := p.heuristicCommercials(ctx, path, info)
	return breaks, CommercialSourceHeuristic, err
}

// heuristicCommercials decodes the file once with blackdetect + silencedetect
// and classifies the result. A file without audio is skipped — silence is half
// of the signal and guessing from black frames alone is too eager.
func (p *PostProcessor) heuristicCommercials(ctx context.Context, path string, info mediaInfo) ([]CommercialBreak, error) {
	if !info.HasVideo || !info.HasAudio || info.DurationSec <= 0 {
		return nil, nil
	}
	hp := p.heuristic()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "info"}
	args = append(args, inputArgs(path)...)
	args = append(args,
		"-map", "0:v:0", "-map", "0:a:0",
		// Detection doesn't need full resolution; scaling first makes the
		// filter cheap on HD captures (decode still dominates).
		"-vf", fmt.Sprintf("scale=320:-2,blackdetect=d=%.2f:pix_th=%.2f:pic_th=%.2f",
			hp.MinOverlap.Seconds(), hp.BlackPixelThreshold, hp.BlackPictureRatio),
		"-af", fmt.Sprintf("silencedetect=noise=%gdB:d=%.2f", hp.SilenceNoiseDB, hp.MinOverlap.Seconds()),
		"-f", "null", "-",
	)
	cmd := exec.CommandContext(ctx, p.cfg.FFmpegBin, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("detect stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("detect start: %w", err)
	}
	black, silence := parseDetectOutput(stderr, info.DurationSec)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("detect run: %w", err)
	}
	return classifyBreaks(black, silence, info.DurationSec, hp), nil
}

func (p *PostProcessor) heuristic() HeuristicParams {
	if p.cfg.Heuristic != nil {
		return *p.cfg.Heuristic
	}
	return DefaultHeuristicParams()
}

// parseDetectOutput reads ffmpeg's stderr for blackdetect and silencedetect
// lines. A silence still open at EOF runs to the end of the file.
func parseDetectOutput(r io.Reader, durationSec float64) (black, silence []timeSpan) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	openSilence := -1.0
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.Contains(line, "black_start:"):
			s, ok1 := floatAfter(line, "black_start:")
			e, ok2 := floatAfter(line, "black_end:")
			if ok1 && ok2 && e > s {
				black = append(black, timeSpan{s, e})
			}
		case strings.Contains(line, "silence_start:"):
			if s, ok := floatAfter(line, "silence_start:"); ok {
				openSilence = math.Max(s, 0)
			}
		case strings.Contains(line, "silence_end:"):
			if e, ok := floatAfter(line, "silence_end:"); ok && openSilence >= 0 && e > openSilence {
				silence = append(silence, timeSpan{openSilence, e})
			}
			openSilence = -1
		}
	}
	if openSilence >= 0 && durationSec > openSilence {
		silence = append(silence, timeSpan{openSilence, durationSec})
	}
	return black, silence
}

// floatAfter parses the number following key in line ("black_end:12.5 ...",
// "silence_end: 13.2 | ...").
func floatAfter(line, key string) (float64, bool) {
	i := strings.Index(line, key)
	if i < 0 {
		return 0, false
	}
	rest := strings.TrimLeft(line[i+len(key):], " \t")
	end := strings.IndexAny(rest, " \t|")
	if end >= 0 {
		rest = rest[:end]
	}
	v, err := strconv.ParseFloat(rest, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// classifyBreaks turns black/silence spans into commercial breaks.
//
//  1. A "transition" is where black video and silence overlap by at least
//     MinOverlap. Transitions closer than MergeGap merge.
//  2. The transitions cut the recording into segments.
//  3. A pod is a maximal run of consecutive segments each no longer than
//     MaxAdLen, with at least MinAdsPerPod segments, a total length within
//     [MinPodLen, MaxPodLen], and a segment of at least MinProgramLen on BOTH
//     sides.
//
// The marked break runs from the end of the transition that opens the pod to
// the start of the one that closes it, so a skip never jumps past programme.
func classifyBreaks(black, silence []timeSpan, durationSec float64, hp HeuristicParams) []CommercialBreak {
	if durationSec <= 0 {
		return nil
	}
	sort.Slice(black, func(i, j int) bool { return black[i].start < black[j].start })
	sort.Slice(silence, func(i, j int) bool { return silence[i].start < silence[j].start })

	minOverlap := hp.MinOverlap.Seconds()
	var trans []timeSpan
	for _, b := range black {
		for _, s := range silence {
			if s.start >= b.end {
				break
			}
			lo, hi := math.Max(b.start, s.start), math.Min(b.end, s.end)
			if hi-lo >= minOverlap {
				trans = append(trans, timeSpan{lo, hi})
			}
		}
	}
	if len(trans) < 2 {
		return nil
	}
	sort.Slice(trans, func(i, j int) bool { return trans[i].start < trans[j].start })
	merged := []timeSpan{trans[0]}
	for _, t := range trans[1:] {
		last := &merged[len(merged)-1]
		if t.start-last.end <= hp.MergeGap.Seconds() {
			last.end = math.Max(last.end, t.end)
			continue
		}
		merged = append(merged, t)
	}

	// Segment i lies between transition i-1 and transition i; segment 0
	// starts at the beginning of the file and the last ends at its end.
	type segment struct{ start, end float64 }
	segs := make([]segment, 0, len(merged)+1)
	prev := 0.0
	for _, t := range merged {
		segs = append(segs, segment{prev, t.start})
		prev = t.end
	}
	segs = append(segs, segment{prev, durationSec})
	segLen := func(i int) float64 { return segs[i].end - segs[i].start }

	maxAd := hp.MaxAdLen.Seconds()
	minProg := hp.MinProgramLen.Seconds()
	var out []CommercialBreak
	for i := 0; i < len(segs); {
		if segLen(i) > maxAd {
			i++
			continue
		}
		j := i
		for j+1 < len(segs) && segLen(j+1) <= maxAd {
			j++
		}
		// Run of short segments i..j. Needs programme on both sides: segment
		// 0 and the last segment touch the file edges and never qualify.
		if i > 0 && j < len(segs)-1 &&
			segLen(i-1) >= minProg && segLen(j+1) >= minProg &&
			j-i+1 >= hp.MinAdsPerPod {
			start, end := segs[i].start, segs[j].end
			podLen := end - start
			if podLen >= hp.MinPodLen.Seconds() && podLen <= hp.MaxPodLen.Seconds() {
				out = append(out, CommercialBreak{
					StartMS: int64(math.Round(start * 1000)),
					EndMS:   int64(math.Round(end * 1000)),
				})
			}
		}
		i = j + 1
	}
	return out
}

// ── comskip ─────────────────────────────────────────────────────────────────

// comskipTimeout bounds one comskip run; it decodes the whole recording.
const comskipTimeout = 3 * time.Hour

// runComskip runs the operator-configured comskip binary (argv only — never a
// shell) with its output directed to a private temp dir, then reads the EDL
// (or, failing that, the frame-numbered .txt) it wrote. comskip exits 1 when
// it found no commercials, which is a success here.
func runComskip(ctx context.Context, bin, ini, path string, durationSec float64) ([]CommercialBreak, error) {
	dir, err := os.MkdirTemp("", "onscreen-comskip-")
	if err != nil {
		return nil, fmt.Errorf("comskip temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	cctx, cancel := context.WithTimeout(ctx, comskipTimeout)
	defer cancel()
	args := []string{}
	if ini != "" {
		args = append(args, "--ini="+ini)
	}
	args = append(args, "--output="+dir, path)
	cmd := exec.CommandContext(cctx, bin, args...)
	var out cappedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) || ee.ExitCode() != 1 {
			return nil, fmt.Errorf("comskip: %w: %s", runErr, lastLine(out.String()))
		}
	}

	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if b, err := os.ReadFile(filepath.Join(dir, base+".edl")); err == nil {
		return clampBreaks(parseEDL(string(b)), durationSec), nil
	}
	if b, err := os.ReadFile(filepath.Join(dir, base+".txt")); err == nil {
		breaks, err := parseComskipTxt(string(b))
		if err != nil {
			return nil, err
		}
		return clampBreaks(breaks, durationSec), nil
	}
	if runErr != nil {
		// Exit 1 with no output file: nothing found.
		return nil, nil
	}
	return nil, errors.New("comskip wrote neither an .edl nor a .txt file (check output_edl / output_txt in the ini)")
}

// parseEDL reads a MPlayer/Kodi EDL: "start end action" per line, seconds.
// Actions 0 (cut) and 3 (commercial break) are adverts; scene markers and
// mutes are not.
func parseEDL(s string) []CommercialBreak {
	var out []CommercialBreak
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		start, err1 := strconv.ParseFloat(f[0], 64)
		end, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		if len(f) >= 3 && f[2] != "0" && f[2] != "3" {
			continue
		}
		out = append(out, CommercialBreak{
			StartMS: int64(math.Round(start * 1000)),
			EndMS:   int64(math.Round(end * 1000)),
		})
	}
	return out
}

// parseComskipTxt reads comskip's native output: a header naming the frame
// rate ("FILE PROCESSING COMPLETE 53999 FRAMES AT 2997" — fps x 100), a
// dashed rule, then "startframe endframe" lines.
func parseComskipTxt(s string) ([]CommercialBreak, error) {
	lines := strings.Split(s, "\n")
	fps := 0.0
	var out []CommercialBreak
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "FILE PROCESSING COMPLETE") {
			if i := strings.LastIndex(line, " AT "); i >= 0 {
				if v, err := strconv.ParseFloat(strings.TrimSpace(line[i+4:]), 64); err == nil && v > 0 {
					fps = v / 100
				}
			}
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || fps <= 0 {
			continue
		}
		a, err1 := strconv.ParseFloat(f[0], 64)
		b, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, CommercialBreak{
			StartMS: int64(math.Round(a / fps * 1000)),
			EndMS:   int64(math.Round(b / fps * 1000)),
		})
	}
	if fps <= 0 {
		return nil, errors.New("comskip .txt has no frame-rate header")
	}
	return out, nil
}

// clampBreaks drops empty or inverted breaks, clamps them to the file and
// sorts them. Overlapping breaks merge.
func clampBreaks(in []CommercialBreak, durationSec float64) []CommercialBreak {
	durMS := int64(durationSec * 1000)
	var out []CommercialBreak
	for _, b := range in {
		if b.StartMS < 0 {
			b.StartMS = 0
		}
		if durMS > 0 && b.EndMS > durMS {
			b.EndMS = durMS
		}
		if b.EndMS-b.StartMS < 1000 {
			continue
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartMS < out[j].StartMS })
	merged := out[:0]
	for _, b := range out {
		if n := len(merged); n > 0 && b.StartMS <= merged[n-1].EndMS {
			if b.EndMS > merged[n-1].EndMS {
				merged[n-1].EndMS = b.EndMS
			}
			continue
		}
		merged = append(merged, b)
	}
	return merged
}

// keepSegments is the complement of breaks over [0, duration]: the programme
// parts a cut keeps, in seconds. Breaks must be sorted and non-overlapping
// (clampBreaks / classifyBreaks guarantee it).
func keepSegments(breaks []CommercialBreak, durationSec float64) []timeSpan {
	var out []timeSpan
	pos := 0.0
	for _, b := range breaks {
		s, e := float64(b.StartMS)/1000, float64(b.EndMS)/1000
		if s > pos {
			out = append(out, timeSpan{pos, s})
		}
		if e > pos {
			pos = e
		}
	}
	if durationSec > pos {
		out = append(out, timeSpan{pos, durationSec})
	}
	return out
}
