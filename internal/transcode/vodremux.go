package transcode

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/onscreen/onscreen/internal/ffsafe"
)

// VOD remux: a stream-copy session served as one complete VOD playlist over
// the whole file, instead of the growing EVENT playlist a remux run writes.
//
// Why: an EVENT playlist is still "live" until ffmpeg writes ENDLIST, and
// Roku OS 16 labels a live stream "Rewind live TV" on its system trickplay
// bar (content.Live / Length don't change it). A full playlist also gives
// every player the real duration and lets a seek go anywhere at once.
//
// How (all ffmpeg behaviour measured on 8.1 against MKV sources):
//
//   - Segment boundaries are the source's INDEXED video keyframes (the MKV
//     Cues, mkvcues.go), grouped to at least RemuxSegmentTarget seconds. A
//     stream copy can only cut at keyframes, and a seek can only land on an
//     indexed one (matroska seeks through the Cues).
//   - Every run is `-copyts` with a constant -output_ts_offset (RemuxTSOffset):
//     a piece's timestamps are its source timestamps plus the offset in every
//     run, so pieces from a run started at 0 and from a restart at a later
//     boundary splice cleanly. (Without copyts the run from 0 alone gets
//     shifted by the B-frame delay — measured +0.083 s — and restarts don't.)
//   - A restart seeks to boundary+RemuxSeekLead: fftools seeks 3/23 s early
//     when the input has B-frames, so `-ss K` lands one GOP before K (-ss 30
//     -> 27.5 s measured); K+0.132 lands on K with or without B-frames.
//   - ffmpeg cuts a PIECE at every keyframe (-hls_time far below any GOP),
//     so every boundary is a piece boundary in every run. A segment is the
//     run of pieces between two boundaries, found from the run's own
//     playlist (RemuxPieceSpan) and served back to back — MPEG-TS and fMP4
//     fragments both concatenate.

// RemuxSegmentTarget is the shortest segment the plan groups keyframes into.
const RemuxSegmentTarget = float64(SegmentDuration)

// RemuxSeekLead is added to a boundary to make ffmpeg's input seek land on
// it. fftools subtracts 3*AV_TIME_BASE/23 (0.1304 s) from -ss when the input
// has B-frames; 0.132 clears that while staying far below any GOP.
const RemuxSeekLead = 0.132

// remuxMinGap is the minimum distance from a boundary to the next indexed
// keyframe: a seek to boundary+RemuxSeekLead without the B-frame correction
// must not reach the next one.
const remuxMinGap = RemuxSeekLead + 0.02

// remuxMinTail keeps the last segment from being a sliver.
const remuxMinTail = 1.0

// remuxMaxFirstKeyframe: a file whose video starts later than this (a TS
// capture with a non-zero start time remuxed to MKV) has a timeline the plan
// can't line up with the playlist's 0; such files stay on the EVENT remux.
const remuxMaxFirstKeyframe = 0.5

// RemuxPieceHLSTime is the -hls_time for a piece-cutting run: far below any
// real GOP, so the HLS muxer cuts at every keyframe.
const RemuxPieceHLSTime = "0.001"

// pieceTolerance is how far a piece start may sit from a boundary and still
// be that boundary (EXTINF rounding, the first run's first-packet offset).
const pieceTolerance = 0.1

// RemuxPlan is a VOD remux's segment layout over the whole file.
type RemuxPlan struct {
	// Starts[i] is segment i's start time in seconds; Starts[0] is 0.
	Starts []float64
	// Duration is the file's duration in seconds (the last segment's end).
	Duration float64
}

// PlanRemux groups indexed keyframes (seconds, ascending) into segments of at
// least RemuxSegmentTarget seconds. ok=false when the file can't be served as
// a VOD remux (no usable keyframes, a late first keyframe, an unknown
// duration, or a single segment's worth of boundaries for a long file — an
// intra-only or near-intra source whose keyframes are all within the seek
// lead of each other).
func PlanRemux(keyframes []float64, durationSec float64) (RemuxPlan, bool) {
	if durationSec <= 0 || len(keyframes) == 0 || keyframes[0] > remuxMaxFirstKeyframe {
		return RemuxPlan{}, false
	}
	starts := []float64{0}
	last := 0.0
	for i, k := range keyframes {
		if k <= 0 {
			continue
		}
		if k-last < RemuxSegmentTarget || durationSec-k < remuxMinTail {
			continue
		}
		if i+1 < len(keyframes) && keyframes[i+1]-k <= remuxMinGap {
			continue // a seek to k+lead could land on the next keyframe
		}
		starts = append(starts, k)
		last = k
	}
	// A long file that produced only one boundary has nothing a seek can
	// use; the EVENT remux handles it better.
	if len(starts) < 2 && durationSec > 2*RemuxSegmentTarget {
		return RemuxPlan{}, false
	}
	return RemuxPlan{Starts: starts, Duration: durationSec}, true
}

// Count is the number of segments.
func (p RemuxPlan) Count() int { return len(p.Starts) }

// Span returns segment i's [start, end) in seconds.
func (p RemuxPlan) Span(i int) (float64, float64) {
	start := p.Starts[i]
	end := p.Duration
	if i+1 < len(p.Starts) {
		end = p.Starts[i+1]
	}
	return start, end
}

// SegmentAt returns the segment containing time t (clamped to the plan).
func (p RemuxPlan) SegmentAt(t float64) int {
	lo, hi := 0, len(p.Starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if p.Starts[mid] <= t {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// SeekTarget is the -ss for a run starting at segment i (0 for segment 0).
func (p RemuxPlan) SeekTarget(i int) float64 {
	if i <= 0 {
		return 0
	}
	return p.Starts[i] + RemuxSeekLead
}

// TargetDuration is the EXT-X-TARGETDURATION: the longest segment, rounded up.
func (p RemuxPlan) TargetDuration() int {
	longest := 0.0
	for i := range p.Starts {
		s, e := p.Span(i)
		longest = math.Max(longest, e-s)
	}
	return int(math.Ceil(longest))
}

// Playlist renders the VOD media playlist. segURL gives segment i's URI;
// initURL (fMP4 only, "" for MPEG-TS) is the EXT-X-MAP.
func (p RemuxPlan) Playlist(segURL func(i int) string, initURL string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", p.TargetDuration())
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	if initURL != "" {
		fmt.Fprintf(&b, "#EXT-X-MAP:URI=%q\n", initURL)
	}
	for i := range p.Starts {
		s, e := p.Span(i)
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n%s\n", e-s, segURL(i))
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// RemuxTSOffset is the constant -output_ts_offset every run of a file gets:
// enough to keep the first run's earliest DTS (the B-frame reorder delay,
// and the audio encoder's priming) non-negative, so ffmpeg never shifts one
// run and not another. hasBFrames/fps come from the source probe; unknown
// values get a generous 1 s.
func RemuxTSOffset(hasBFrames int, fps float64) float64 {
	if fps <= 0 || hasBFrames < 0 {
		return 1.0
	}
	d := float64(hasBFrames+1)/fps + 0.05
	return math.Min(math.Max(math.Ceil(d*1000)/1000, 0.1), 2.0)
}

// ParseRunPieces reads a run's own (EVENT) playlist: the EXTINF duration of
// each piece in order, and whether the run has finished (ENDLIST).
func ParseRunPieces(m3u8 string) (durs []float64, ended bool) {
	sc := bufio.NewScanner(strings.NewReader(m3u8))
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXTINF:"):
			v := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			if d, err := strconv.ParseFloat(v, 64); err == nil {
				durs = append(durs, d)
			}
		case line == "#EXT-X-ENDLIST":
			ended = true
		}
	}
	return durs, ended
}

// PieceSpan is what RemuxPieceSpan found for one segment.
type PieceSpan struct {
	First, Last int  // inclusive local piece indices
	Ready       bool // every piece of the segment is written
	Mismatch    bool // the run's pieces don't line up with the plan
}

// RemuxPieceSpan maps segment [segStart, segEnd) onto the pieces of a run
// that began at runStart (content time of its first piece) and has written
// pieces with durations durs so far. lastSeg says segEnd is the end of the
// file, which only ENDLIST (ended) can confirm.
//
// Not Ready means "wait": the run hasn't reached the segment's end yet.
// Mismatch means no piece starts at a boundary where the plan put one —
// the run can never serve this segment, and the caller restarts it there.
func RemuxPieceSpan(runStart float64, durs []float64, ended bool, segStart, segEnd float64, lastSeg bool) PieceSpan {
	// starts[j] = content time piece j begins; starts[len] = where the
	// written pieces end.
	starts := make([]float64, len(durs)+1)
	starts[0] = runStart
	for j, d := range durs {
		starts[j+1] = starts[j] + d
	}
	written := len(durs)
	find := func(t float64) (int, bool) {
		for j := 0; j <= written; j++ {
			if math.Abs(starts[j]-t) <= pieceTolerance {
				return j, true
			}
			if starts[j] > t+pieceTolerance {
				break
			}
		}
		return 0, false
	}
	first, ok := find(segStart)
	if !ok {
		if starts[written] < segStart-pieceTolerance && !ended {
			return PieceSpan{} // not there yet
		}
		return PieceSpan{Mismatch: true}
	}
	if first == written {
		if ended {
			return PieceSpan{Mismatch: true} // the run stopped at the segment's start
		}
		return PieceSpan{} // segment's first piece not finished yet
	}
	if lastSeg {
		if !ended {
			return PieceSpan{First: first}
		}
		return PieceSpan{First: first, Last: written - 1, Ready: true}
	}
	end, ok := find(segEnd)
	if !ok {
		if starts[written] < segEnd-pieceTolerance && !ended {
			return PieceSpan{First: first} // still writing this segment
		}
		return PieceSpan{Mismatch: true}
	}
	if end <= first {
		return PieceSpan{Mismatch: true}
	}
	return PieceSpan{First: first, Last: end - 1, Ready: true}
}

// ProbeVideoDelay reads the first video stream's B-frame reorder depth and
// frame rate for RemuxTSOffset. A failure returns (-1, 0), which
// RemuxTSOffset turns into its safe 1 s.
func ProbeVideoDelay(ctx context.Context, path string) (hasBFrames int, fps float64) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=has_b_frames,avg_frame_rate,r_frame_rate",
		"-of", "default=noprint_wrappers=1",
		"-protocol_whitelist", ffsafe.Whitelist(path),
		path,
	).Output()
	if err != nil {
		return -1, 0
	}
	hasBFrames = -1
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "has_b_frames":
			if n, err := strconv.Atoi(v); err == nil {
				hasBFrames = n
			}
		case "avg_frame_rate", "r_frame_rate":
			if fps == 0 {
				fps = parseRate(v)
			}
		}
	}
	return hasBFrames, fps
}

// parseRate parses an ffprobe rate ("24000/1001", "25/1", "0/0").
func parseRate(v string) float64 {
	num, den, ok := strings.Cut(v, "/")
	if !ok {
		f, _ := strconv.ParseFloat(v, 64)
		return f
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return 0
	}
	return n / d
}
