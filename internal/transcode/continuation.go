package transcode

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ── Auto-continue past a premature ffmpeg EOF ────────────────────────────────
//
// Some sources have a defect partway through — a corrupt matroska cluster, a
// non-monotonic DTS — that makes a linear `-c:v copy` demux return a *clean*
// EOF early (ffmpeg exits 0 well short of the real duration). The player then
// stalls at the buffer edge with no more segments coming. Seeking *past* the
// bad spot with a fresh `-ss` reads the remainder fine, so the worker
// transparently restarts ffmpeg from the stall point and stitches the new
// segments onto the same HLS playlist — the client never sees the seam beyond
// a single EXT-X-DISCONTINUITY.

const (
	// shortCompletionMarginSec: a clean ffmpeg exit whose produced content is
	// within this margin of the source duration counts as a legitimate end of
	// file, not a premature stop. Covers the last partial segment + rounding.
	shortCompletionMarginSec = 12.0
	// maxContinuations bounds how many times we restart ffmpeg past a defect
	// for a single session — a backstop against a pathological source that
	// stops every few seconds.
	maxContinuations = 24
	// continuationSkipStepSec is how far past the stall point we jump when a
	// continuation makes no forward progress (the defect sits on the seek
	// keyframe so we keep landing before it). Grows each stuck retry.
	continuationSkipStepSec = 4.0
	// maxContinuationSkipSec caps the cumulative forward skip before we give
	// up — a corrupt region larger than this is pathological, and skipping
	// further would discard too much real content.
	maxContinuationSkipSec = 60.0
)

// shortOfSource reports whether the content produced so far (seconds from the
// source's start) falls materially short of the source duration — i.e. ffmpeg
// stopped before the real EOF and we should try to continue past a defect.
// Returns false when the source duration is unknown (<= 0), so an unprobeable
// source is never mistaken for a premature stop.
func shortOfSource(producedEndSec, sourceDurSec float64) bool {
	return sourceDurSec > 0 && producedEndSec < sourceDurSec-shortCompletionMarginSec
}

// continuationSkip computes the forward-skip for the next continuation attempt.
// progressed=true (the last run advanced the produced position) resets the skip
// to 0 — we're moving, keep going from where we are. progressed=false (the
// defect sits on the seek keyframe so the run re-stopped immediately) grows the
// skip by one step to jump past it; giveUp becomes true once the cumulative
// skip would exceed the cap, at which point a defect that large is pathological
// and we finalize the playlist where it stands.
func continuationSkip(progressed bool, currentSkip float64) (next float64, giveUp bool) {
	if progressed {
		return 0, false
	}
	next = currentSkip + continuationSkipStepSec
	return next, next > maxContinuationSkipSec
}

// playlistGlobalTags are the m3u8 header / control tags that describe a
// playlist as a whole rather than an individual segment. When stitching a
// continuation onto an existing playlist we keep the original's globals (so
// EXT-X-MEDIA-SEQUENCE stays put and the client doesn't re-download) and
// append only the continuation's per-segment lines after a DISCONTINUITY.
var playlistGlobalTags = map[string]bool{
	"#EXTM3U":                     true,
	"#EXT-X-VERSION":              true,
	"#EXT-X-TARGETDURATION":       true,
	"#EXT-X-MEDIA-SEQUENCE":       true,
	"#EXT-X-PLAYLIST-TYPE":        true,
	"#EXT-X-INDEPENDENT-SEGMENTS": true,
	"#EXT-X-ENDLIST":              true,
}

// playlistTag returns the tag portion of an m3u8 line (the part before the
// first colon), or the whole line for colon-less tags like #EXTM3U. Returns
// "" for non-tag lines (segment URIs).
func playlistTag(line string) string {
	if !strings.HasPrefix(line, "#") {
		return ""
	}
	if i := strings.IndexByte(line, ':'); i >= 0 {
		return line[:i]
	}
	return line
}

// playlistDurationSec sums the #EXTINF durations in an HLS media playlist —
// the wall-clock length of the content it currently lists.
func playlistDurationSec(playlist []byte) float64 {
	var total float64
	sc := bufio.NewScanner(bytes.NewReader(playlist))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		v := strings.TrimPrefix(line, "#EXTINF:")
		if c := strings.IndexByte(v, ','); c >= 0 {
			v = v[:c]
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			total += f
		}
	}
	return total
}

// extractSegmentBody returns the per-segment lines of a media playlist —
// everything except the global header tags and ENDLIST. These are the lines
// appended (after a DISCONTINUITY) when stitching a continuation in. For fMP4
// the continuation's own EXT-X-MAP is preserved, which is exactly what a
// discontinuity boundary needs.
func extractSegmentBody(playlist []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(playlist))
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if playlistGlobalTags[playlistTag(strings.TrimSpace(line))] {
			continue
		}
		out = append(out, line)
	}
	return out
}

// stitchPlaylist builds a media playlist = the original prefix (any trailing
// ENDLIST stripped) + an EXT-X-DISCONTINUITY + the continuation's segment
// lines, terminated with ENDLIST only when the stream is complete. An empty
// contBody yields the prefix alone (no dangling DISCONTINUITY), so a
// zero-progress continuation can't corrupt the playlist.
func stitchPlaylist(prefix []byte, contBody []string, ended bool) []byte {
	lines := strings.Split(strings.TrimRight(string(prefix), "\n"), "\n")
	// Drop a trailing ENDLIST (and any blank tail) so we can append more.
	kept := lines[:0]
	for _, l := range lines {
		if strings.TrimSpace(l) == "#EXT-X-ENDLIST" {
			continue
		}
		kept = append(kept, l)
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	if len(contBody) > 0 {
		kept = append(kept, "#EXT-X-DISCONTINUITY")
		kept = append(kept, contBody...)
	}
	if ended {
		kept = append(kept, "#EXT-X-ENDLIST")
	}
	return []byte(strings.Join(kept, "\n") + "\n")
}

// writeFileAtomic writes data via a temp file + rename so a client polling
// the playlist never reads a half-written file mid-stitch.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readFileOrNil(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

// probeSourceDurationSec returns input's container duration in seconds, or 0
// if ffprobe can't determine it. Used to decide whether a clean ffmpeg exit
// reached the real end of the source or stopped short on a defect. Read from
// the container header, so a mid-file demux defect doesn't perturb it.
func probeSourceDurationSec(ctx context.Context, input string) float64 {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1",
		input,
	).Output()
	if err != nil {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return f
}

// probeSourceDuration indirects probeSourceDurationSec so the ENDLIST
// lifecycle of continueShortSession is testable without shelling to ffprobe.
var probeSourceDuration = probeSourceDurationSec

// continueShortSession transparently extends an HLS session whose ffmpeg
// exited cleanly but short of the source duration. It seeks past the bad spot,
// transcodes the remainder into a side playlist, and stitches the new segments
// onto index.m3u8 in near-real-time so the player keeps reading without a
// stall.
//
// buildArgs rebuilds the ffmpeg argv with a new start offset / segment number
// / playlist filename; runFFmpeg runs it under the same heartbeat + idle-kill
// supervision as the primary run (so a client that leaves still gets reaped).
// Only the BuildHLS path (remux + re-encode) is supported — directStream uses
// a different builder.
func (w *Worker) continueShortSession(
	ctx context.Context,
	job TranscodeJob,
	sessionDir, segExt, input string,
	useQSV bool,
	buildArgs func(useQSV bool, startOffset float64, startNumber int, playlistName string) []string,
	runFFmpeg func(ffArgs []string) (exitErr error, selfExited bool),
) {
	indexPath := filepath.Join(sessionDir, "index.m3u8")

	// Strip ENDLIST BEFORE anything slow. The duration probe below shells out
	// to ffprobe — seconds, over HTTP source on a fleet worker — and hls.js
	// stops refreshing a playlist the instant it sees ENDLIST. A player that
	// polled during the probe window latched "VOD ended" and never saw the
	// continuation at all; stripping first keeps it polling while we decide.
	pl0, err := os.ReadFile(indexPath)
	if err != nil {
		return
	}
	if bytes.Contains(pl0, []byte("#EXT-X-ENDLIST")) {
		if err := writeFileAtomic(indexPath, stitchPlaylist(pl0, nil, false)); err != nil {
			return
		}
	}
	// EVERY exit re-terminates the playlist. The success path used to `return`
	// with a comment claiming "ffmpeg's ENDLIST stands" — but after the first
	// fold-in the merged playlist was written WITHOUT one, so every
	// successfully auto-continued session left index.m3u8 permanently
	// unterminated: the player polled a finished VOD forever and never showed
	// "ended". The strip above makes this mandatory on the probe-failure path
	// too.
	defer func() {
		if pl := readFileOrNil(indexPath); pl != nil && !bytes.Contains(pl, []byte("#EXT-X-ENDLIST")) {
			_ = writeFileAtomic(indexPath, append(bytes.TrimRight(pl, "\n"), []byte("\n#EXT-X-ENDLIST\n")...))
		}
	}()

	sourceDur := probeSourceDuration(ctx, input)
	if sourceDur <= 0 {
		return // can't verify completeness — the deferred close restores ENDLIST
	}

	extraSkip := 0.0
	for attempt := 1; attempt <= maxContinuations; attempt++ {
		pl, err := os.ReadFile(indexPath)
		if err != nil {
			return
		}
		producedEnd := job.StartOffsetSec + playlistDurationSec(pl)
		if !shortOfSource(producedEnd, sourceDur) {
			return // reached the real end; the deferred close terminates the playlist
		}
		// Only keep extending while the client still wants the session.
		if _, err := w.store.Get(ctx, job.SessionID); err != nil {
			return
		}

		prefix := stitchPlaylist(pl, nil, false)
		if err := writeFileAtomic(indexPath, prefix); err != nil {
			return
		}

		seekTo := producedEnd + extraSkip
		startNum := HighestSegmentIndex(sessionDir, segExt) + 1
		w.logger.Warn("ffmpeg completed early — continuing transcode past source defect",
			"session_id", job.SessionID,
			"produced_sec", int(producedEnd),
			"source_sec", int(sourceDur),
			"seek_to_sec", int(seekTo),
			"start_number", startNum,
			"attempt", attempt,
		)

		const contName = "cont.m3u8"
		contPath := filepath.Join(sessionDir, contName)
		_ = os.Remove(contPath)

		// Stitch the continuation's growing segment list onto index.m3u8 once
		// per second while ffmpeg runs, so new segments become visible to the
		// client in near-real-time (no ENDLIST yet — the run isn't done).
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			t := time.NewTicker(time.Second)
			defer t.Stop()
			stitch := func() {
				body := readFileOrNil(contPath)
				if body == nil {
					return
				}
				_ = writeFileAtomic(indexPath, stitchPlaylist(prefix, extractSegmentBody(body), false))
			}
			for {
				select {
				case <-stop:
					stitch() // final catch-up stitch
					return
				case <-t.C:
					stitch()
				}
			}
		}()

		_, selfExited := runFFmpeg(buildArgs(useQSV, seekTo, startNum, contName))
		close(stop)
		<-done

		// A kill (client stop / idle / worker shutdown) means the session is
		// done for — stop extending it.
		if !selfExited {
			return
		}

		// Fold the finished continuation into the playlist for the next round.
		body := extractSegmentBody(readFileOrNil(contPath))
		merged := stitchPlaylist(prefix, body, false)
		_ = writeFileAtomic(indexPath, merged)
		_ = os.Remove(contPath)

		newProduced := job.StartOffsetSec + playlistDurationSec(merged)
		progressed := newProduced > producedEnd+1.0
		var giveUp bool
		extraSkip, giveUp = continuationSkip(progressed, extraSkip)
		if giveUp {
			// The defect is larger than maxContinuationSkipSec — pathological.
			// Finalize the playlist where it stands rather than skip further.
			w.logger.Warn("auto-continue stuck past source defect; finalizing",
				"session_id", job.SessionID, "produced_sec", int(newProduced))
			break
		}
	}

	// The deferred close above terminates the playlist on this path too.
}
