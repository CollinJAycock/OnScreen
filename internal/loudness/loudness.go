// Package loudness measures EBU R128 loudness with ffmpeg and turns it into
// ReplayGain 2.0 values, for music files that carry no ReplayGain tags (the
// opt-in loudness_analysis scheduler task).
//
// ReplayGain 2.0 is defined on EBU R128: a track's gain is the difference
// between the −18 LUFS reference and its integrated loudness, and its peak is
// the true peak as a linear amplitude (1.0 = full scale). An album's gain
// uses the album's loudness, which this package combines from the tracks'
// integrated loudness weighted by their duration in the energy domain —
// averaging the dB values would let a quiet interlude pull the whole album
// up. That approximates measuring the album as one programme; the gating of
// a single measurement is per track instead of over the whole album.
package loudness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onscreen/onscreen/internal/ffsafe"
)

// ReferenceLUFS is ReplayGain 2.0's reference loudness.
const ReferenceLUFS = -18.0

// silenceLUFS is the loudness ebur128 reports when nothing in the programme
// rises above its absolute gate (−70 LUFS): digital silence, a hidden-track
// gap. Such a file has no meaningful gain.
const silenceLUFS = -70.0

// Measurement is one file's EBU R128 measurement.
type Measurement struct {
	// IntegratedLUFS is the integrated (programme) loudness; nil when the
	// file is silent.
	IntegratedLUFS *float64
	// TruePeak is the true peak (inter-sample peaks included) as a linear
	// amplitude, 1.0 = full scale; nil when ffmpeg reported none (−inf).
	TruePeak *float64
	// Error, when set, is why ffmpeg could not measure the file (it ran
	// and failed on this input). Not retried until the file changes.
	Error string
}

// ReplayGain is the set of values written to a file.
type ReplayGain struct {
	TrackGain *float64 // dB
	TrackPeak *float64 // linear
	AlbumGain *float64 // dB
	AlbumPeak *float64 // linear
}

// TrackGain returns the ReplayGain 2.0 track gain for an integrated
// loudness, rounded to the 0.01 dB the database stores.
func TrackGain(integratedLUFS float64) float64 {
	return round(ReferenceLUFS-integratedLUFS, 2)
}

// LinearPeak converts a true peak in dBTP to the linear amplitude
// REPLAYGAIN_*_PEAK holds (1.0 = full scale; above 1.0 is legal and must
// not be clamped), rounded to the 6 decimals the database stores.
func LinearPeak(dbtp float64) float64 {
	return round(math.Pow(10, dbtp/20), 6)
}

// Track is one track's contribution to its album's loudness.
type Track struct {
	IntegratedLUFS float64
	DurationMS     int64
}

// AlbumLoudness combines tracks' integrated loudness into the album's:
// each track's loudness is turned back into mean energy, weighted by its
// duration, and the weighted mean energy turned into LUFS again. ok is false
// when no track has a positive duration.
func AlbumLoudness(tracks []Track) (lufs float64, ok bool) {
	var energy, total float64
	for _, t := range tracks {
		if t.DurationMS <= 0 || math.IsNaN(t.IntegratedLUFS) || math.IsInf(t.IntegratedLUFS, 0) {
			continue
		}
		d := float64(t.DurationMS)
		energy += d * math.Pow(10, t.IntegratedLUFS/10)
		total += d
	}
	if total == 0 || energy == 0 {
		return 0, false
	}
	return 10 * math.Log10(energy/total), true
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

// MeasureTimeout bounds one file's measurement. ebur128 decodes the whole
// file; even a long hi-res track decodes in well under a minute locally, so
// this only stops a stalled read.
const MeasureTimeout = 10 * time.Minute

// ErrToolFailed wraps a measurement that couldn't run (ffmpeg missing,
// timeout, cancellation). The file is not to blame and stays queued.
var ErrToolFailed = errors.New("loudness: measurement could not run")

// Measure runs ffmpeg's ebur128 filter (true peak on) over the first audio
// stream of input — a local path or an http(s) URL; the protocol whitelist
// from ffsafe confines it either way. An error wraps ErrToolFailed; a file
// ffmpeg ran on but could not measure comes back as Measurement.Error.
func Measure(ctx context.Context, input string) (Measurement, error) {
	ctx, cancel := context.WithTimeout(ctx, MeasureTimeout)
	defer cancel()
	args := []string{"-hide_banner", "-nostats", "-nostdin"}
	args = append(args, ffsafe.InputProtocolArgs(input)...)
	args = append(args,
		"-i", input,
		"-map", "0:a:0", "-vn", "-sn", "-dn",
		"-af", "ebur128=peak=true",
		"-f", "null", "-")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	// ebur128 logs a line per 100 ms of audio before its summary; only the
	// tail is needed.
	stderr := &tailBuffer{max: 64 << 10}
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return Measurement{}, fmt.Errorf("%w: %v", ErrToolFailed, ctx.Err())
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return Measurement{}, fmt.Errorf("%w: %v", ErrToolFailed, runErr)
		}
		return Measurement{Error: lastLine(stderr.String(), "ffmpeg exited "+exitErr.Error())}, nil
	}
	m, ok := ParseSummary(stderr.String())
	if !ok {
		return Measurement{Error: "no loudness summary in ffmpeg output"}, nil
	}
	return m, nil
}

var (
	summaryRE  = regexp.MustCompile(`(?s)Summary:.*`)
	integralRE = regexp.MustCompile(`(?m)^\s*I:\s*(-?[0-9.]+|-?inf|nan)\s*LUFS`)
	truePeakRE = regexp.MustCompile(`(?s)True peak:\s*Peak:\s*(-?[0-9.]+|-?inf|nan)\s*dBFS`)
)

// ParseSummary reads integrated loudness and true peak from the summary
// block ebur128 logs at the end of a run. ok is false when there is no
// summary or no integrated loudness in it.
func ParseSummary(out string) (Measurement, bool) {
	sum := summaryRE.FindString(out)
	if sum == "" {
		return Measurement{}, false
	}
	im := integralRE.FindStringSubmatch(sum)
	if im == nil {
		return Measurement{}, false
	}
	var m Measurement
	if v, err := strconv.ParseFloat(im[1], 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v > silenceLUFS {
		v = round(v, 2)
		m.IntegratedLUFS = &v
	}
	if pm := truePeakRE.FindStringSubmatch(sum); pm != nil {
		if v, err := strconv.ParseFloat(pm[1], 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
			p := LinearPeak(v)
			m.TruePeak = &p
		}
	}
	return m, true
}

// lastLine returns the last non-empty line of s, or fallback.
func lastLine(s, fallback string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 300 {
				l = l[:300]
			}
			return l
		}
	}
	return fallback
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	buf bytes.Buffer
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= t.max {
		t.buf.Reset()
		t.buf.Write(p[len(p)-t.max:])
		return n, nil
	}
	if over := t.buf.Len() + len(p) - t.max; over > 0 {
		t.buf.Next(over)
	}
	t.buf.Write(p)
	return n, nil
}

func (t *tailBuffer) String() string { return t.buf.String() }
