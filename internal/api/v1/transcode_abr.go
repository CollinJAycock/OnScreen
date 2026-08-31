package v1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

// On-demand adaptive-bitrate HLS (the Jellyfin model). The parent
// session runs no ffmpeg: playlist.m3u8 serves a master listing the
// ladder, each variant's media playlist is SERVER-PREDICTED from the
// source duration (so every rung shares one segment timeline), and a
// segment request transcodes that rung ON DEMAND, seeking ffmpeg to the
// requested segment's offset. At most one rung is actively encoding (the
// one the player is fetching); the others idle out via the worker's
// activity-based reaper. Reuses BuildHLS + the worker + the segment
// proxy — no encode-pipeline changes.
//
// Rungs are addressed by their Label ("1080p"); the child transcode
// session for a rung is keyed "{parentID}-r{label}".

// abrChildLocks serializes create/restart of a given rung child so two
// concurrent segment fetches don't both spawn an ffmpeg. Per-process
// (keyed by child ID) — correct for the embedded worker / single API
// instance; a multi-instance API tier would need a Valkey lock (the
// segment requests for one session aren't pinned to an instance).
var abrChildLocks sync.Map // childID -> *sync.Mutex

func abrChildLock(childID string) *sync.Mutex {
	mu, _ := abrChildLocks.LoadOrStore(childID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// abrLastRestart stamps the last FORCED restart per child ID, so a client
// cannot span-kill encodes by alternating segment indices. Cleared alongside
// the child's lock in abrForget.
var abrLastRestart sync.Map // childID -> time.Time

// abrMinRestartInterval floors the gap between forced restarts of one child.
// Comfortably above human scrub cadence and far below the cost of an encode
// respawn, so legitimate seeking never notices it.
const abrMinRestartInterval = 2 * time.Second

// startABR creates an ABR parent session (no ffmpeg) and responds with
// the master playlist URL. Called from Start when ABR is enabled, the
// decision is a full re-encode, and the source has a usable ladder.
func (h *NativeTranscodeHandler) startABR(
	w http.ResponseWriter, r *http.Request,
	sessionID, segTok, sourceURL string, userID, itemID uuid.UUID,
	file *media.File, ladder []transcode.Rendition,
	audioStreamIndex, audioChannels int, needsToneMap bool, codec string, positionMS int64,
	maxSessionsPerUser int,
) {
	ctx := r.Context()
	sess := transcode.Session{
		ID:          sessionID,
		UserID:      userID,
		MediaItemID: itemID,
		FileID:      file.ID,
		Decision:    "transcode",
		FilePath:    file.FilePath,
		// One HTTP source URL for the whole ladder — every rung child reuses
		// it (all rungs read the same source file). See buildSourceURL.
		SourceURL:        sourceURL,
		PositionMS:       positionMS,
		CreatedAt:        time.Now(),
		ClientName:       "OnScreenWeb",
		SegToken:         segTok,
		ABR:              true,
		ABRRenditions:    ladder,
		DurationMS:       *file.DurationMS,
		AudioStreamIndex: audioStreamIndex,
		AudioChannels:    audioChannels,
		NeedsToneMap:     needsToneMap,
		// HEVC/AV1 ladders are fMP4 (.m4s + init.mp4); H.264 stays mpegts
		// .ts. One codec for the whole ladder, chosen by client capability
		// in Start.
		HEVCOutput: codec == transcode.LadderHEVC,
		AV1Output:  codec == transcode.LadderAV1,
	}
	// Carry the SOURCE codec so rung children can request hardware decode
	// (AV1 NVDEC, opt-in QSV HEVC) — the worker keys decode off the source,
	// and without this the per-rung jobs lose it and fall back to software.
	if file.VideoCodec != nil {
		switch strings.ToLower(*file.VideoCodec) {
		case "hevc", "h265":
			sess.SourceIsHEVC = true
		case "av1":
			sess.SourceIsAV1 = true
		case "h264", "avc", "avc1":
			sess.SourceIsH264 = true
		}
	}
	if file.FrameRate != nil {
		sess.FrameRate = *file.FrameRate
	}
	if h.audit != nil {
		actor := userID
		h.audit.Log(ctx, &actor, audit.ActionTranscodeStart, sessionID, map[string]any{
			"item_id": itemID.String(),
			"file_id": file.ID.String(),
			"abr":     true,
			"rungs":   len(ladder),
		}, audit.ClientIP(r))
	}
	// Authoritative cap enforcement at write time, same as the single-rendition
	// path. This used to be a plain Create, so the per-user concurrent-stream
	// cap (including an admin-set per-user limit) had NO atomic backstop on the
	// ABR path — only Start's cheap pre-check, which is a check-then-act race
	// that concurrent Starts both pass. Since ABR is the default for a full
	// re-encode, that was the common path.
	if err := h.sessions.CreateWithUserCap(ctx, sess, maxSessionsPerUser); err != nil {
		if errors.Is(err, transcode.ErrUserAtCap) {
			h.logger.WarnContext(ctx, "per-user transcode session cap reached (atomic, abr)",
				"user_id", userID, "cap", maxSessionsPerUser)
			respond.Error(w, r, http.StatusTooManyRequests, "TOO_MANY_SESSIONS",
				fmt.Sprintf("you already have %d active streams; stop one before starting another",
					maxSessionsPerUser))
			return
		}
		h.logger.ErrorContext(ctx, "create ABR session", "err", err)
		respond.InternalError(w, r)
		return
	}
	h.logger.InfoContext(ctx, "ABR session created",
		"session_id", sessionID, "rungs", len(ladder),
		"duration_ms", sess.DurationMS, "top_rung", ladder[0].Label)

	// StartOffsetSec is 0, NOT the resume position: it means "the stream's
	// content begins at this offset", and an ABR playlist covers the full
	// timeline from 0 — every rung's predicted playlist starts at the first
	// segment. Reporting positionMS here told the client the whole stream was
	// shifted by the resume point, so the player showed the resume time on the
	// scrubber while actually playing from 0:00, and then saved that phantom
	// position back over the user's real progress. The client resumes on an
	// ABR stream by SEEKING to its requested position, which the full-timeline
	// playlist supports directly.
	respond.Success(w, r, transcodeStartResponse{
		SessionID:      sessionID,
		PlaylistURL:    fmt.Sprintf("/api/v1/transcode/sessions/%s/playlist.m3u8?token=%s", sessionID, segTok),
		Token:          segTok,
		StartOffsetSec: 0,
	})
}

// serveABRMaster writes the master playlist for an ABR parent session.
// Called by Playlist when the session is ABR. Instant — no ffmpeg.
func (h *NativeTranscodeHandler) serveABRMaster(w http.ResponseWriter, r *http.Request, sess *transcode.Session, token string) {
	codecs := ""
	if len(sess.ABRRenditions) > 0 {
		topH := sess.ABRRenditions[0].Height // [0] = tallest rung
		switch {
		case sess.AV1Output:
			codecs = transcode.AV1MasterCodecs(topH)
		case sess.HEVCOutput:
			codecs = transcode.HEVCMasterCodecs(topH)
		}
	}
	master := transcode.BuildMasterPlaylist(sess.ABRRenditions, codecs, func(rd transcode.Rendition) string {
		return publicSeg(h.segBase(), fmt.Sprintf("/api/v1/transcode/sessions/%s/abr/%s/index.m3u8?token=%s", sess.ID, rd.Label, token))
	})
	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write([]byte(master))
}

// ABRVariantPlaylist serves the server-predicted media playlist for one
// rung. GET /sessions/{sid}/abr/{rung}/index.m3u8 — lists every segment
// for the full duration so the player has the whole timeline up front
// and can switch to / seek within this rung at any point. No ffmpeg runs
// until the player actually fetches a segment.
func (h *NativeTranscodeHandler) ABRVariantPlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := chi.URLParam(r, "sid")
	rungLabel := chi.URLParam(r, "rung")
	token := r.URL.Query().Get("token")

	if !h.authorizeSegmentToken(w, r, token, sessionID) {
		return
	}
	sess, err := h.sessions.Get(ctx, sessionID)
	if err != nil || !sess.ABR {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if findRung(sess.ABRRenditions, rungLabel) == nil {
		http.Error(w, "unknown rung", http.StatusNotFound)
		return
	}

	playlist := buildPredictedVariantPlaylist(sess.DurationMS, sess.FrameRate, sessionID, rungLabel, token, abrIsFMP4(sess), h.segBase())
	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write([]byte(playlist))
}

// abrIsFMP4 reports whether the ladder uses fMP4 (.m4s + init.mp4) segments.
// Both HEVC and AV1 rungs do; H.264 uses MPEG-TS .ts.
//
// Delegates so this prediction cannot diverge from what the worker actually
// muxed — see transcode.VideoOutput.
func abrIsFMP4(sess *transcode.Session) bool {
	return sess.VideoOutput().NeedsFMP4()
}

// abrSegExt is the segment file extension for the ladder's codec: fMP4
// (.m4s) for HEVC/AV1, MPEG-TS (.ts) for H.264.
func abrSegExt(fmp4 bool) string {
	if fmp4 {
		return ".m4s"
	}
	return ".ts"
}

// abrSegmentBoundarySec returns the content time (seconds) at which segment
// segIdx begins. ffmpeg forces a keyframe on the first frame whose timestamp
// is >= segIdx*SegmentDuration (force_key_frames expr:gte(t,n_forced*N)), so
// the real boundary is frame-quantized: ceil(segIdx*SegmentDuration*fps)/fps.
// Predicting the same value keeps the advertised timeline and the encoded
// segments aligned to <1 frame. With fps unknown (≤0) we fall back to the
// nominal flat grid.
func abrSegmentBoundarySec(segIdx int, fps float64) float64 {
	segDur := float64(transcode.SegmentDuration)
	if fps <= 0 {
		return float64(segIdx) * segDur
	}
	return math.Ceil(float64(segIdx)*segDur*fps) / fps
}

// buildPredictedVariantPlaylist renders a VOD media playlist covering the
// whole source. Segment boundaries are frame-quantized to match what ffmpeg
// will actually cut (see abrSegmentBoundarySec); the last segment carries the
// remainder. Segment URIs are global indices the segment handler maps to
// on-demand transcode offsets.
// predictedSegmentCount returns how many segments the predicted variant
// playlist advertises for this session, i.e. the exclusive upper bound on a
// legitimate global segment index. Returns 0 when the duration is unknown, in
// which case the caller must not bound (a 0 duration is a probe failure, not
// an empty file, and refusing everything would break playback).
//
// Shares abrSegmentBoundarySec with buildPredictedVariantPlaylist so the
// ceiling can never drift from the playlist the client was handed.
func predictedSegmentCount(sess *transcode.Session) int {
	if sess == nil || sess.DurationMS <= 0 {
		return 0
	}
	total := float64(sess.DurationMS) / 1000.0
	for i := 0; ; i++ {
		start := abrSegmentBoundarySec(i, sess.FrameRate)
		if i > 0 && start >= total {
			return i
		}
		// Hard stop so a nonsense duration can't spin here.
		if i > maxPredictedSegments {
			return i
		}
	}
}

// maxPredictedSegments bounds the loop above: at the 4 s segment duration this
// is well over 24 hours of content, far past any real media file.
const maxPredictedSegments = 50_000

func buildPredictedVariantPlaylist(durationMS int64, fps float64, sid, rung, token string, fmp4 bool, baseURL string) string {
	segDur := transcode.SegmentDuration
	total := float64(durationMS) / 1000.0
	ext := abrSegExt(fmp4)

	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", segDur)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n")
	// fMP4 (HEVC) segments need the shared init segment up front.
	if fmp4 {
		fmt.Fprintf(&b, "#EXT-X-MAP:URI=\"%s\"\n", publicSeg(baseURL, fmt.Sprintf("/api/v1/transcode/sessions/%s/abr/%s/seg/init.mp4?token=%s", sid, rung, token)))
	}
	for i := 0; ; i++ {
		start := abrSegmentBoundarySec(i, fps)
		if i > 0 && start >= total {
			break // boundary at/after EOF — previous segment was the last
		}
		end := abrSegmentBoundarySec(i+1, fps)
		if end > total {
			end = total
		}
		dur := end - start
		if dur <= 0 {
			break
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n", dur)
		fmt.Fprintf(&b, "%s\n", publicSeg(baseURL, fmt.Sprintf("/api/v1/transcode/sessions/%s/abr/%s/seg/%d%s?token=%s", sid, rung, i, ext, token)))
		if end >= total {
			break // this segment reached EOF
		}
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// ABRVariantSegment serves one segment of one rung, transcoding it on
// demand. GET /sessions/{sid}/abr/{rung}/seg/{name} — name is "N.ts"
// (H.264), "N.m4s" (HEVC fMP4), or "init.mp4" (the HEVC init segment).
// Ensures a child transcode session for the rung is producing global
// segment N (starting/seeking ffmpeg to its offset when needed), then
// serves it.
func (h *NativeTranscodeHandler) ABRVariantSegment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := chi.URLParam(r, "sid")
	rungLabel := chi.URLParam(r, "rung")
	name := filepath.Base(chi.URLParam(r, "name"))
	token := r.URL.Query().Get("token")

	if !h.authorizeSegmentToken(w, r, token, sessionID) {
		return
	}
	parent, err := h.sessions.Get(ctx, sessionID)
	if err != nil || !parent.ABR {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rung := findRung(parent.ABRRenditions, rungLabel)
	if rung == nil {
		http.Error(w, "unknown rung", http.StatusNotFound)
		return
	}

	// Telemetry: record which rung the player's ABR logic is pulling, and keep
	// the parent's /sessions card live (playback activity rides on the rung
	// children otherwise). Cheap no-op when the rung is unchanged.
	h.sessions.SetSelectedRendition(ctx, sessionID, rungLabel)

	childID := abrChildID(sessionID, rungLabel)
	ext := abrSegExt(abrIsFMP4(parent))

	// fMP4 init segment is codec config only — position-independent. Ensure a
	// child is running (any offset — a mid-stream child's init is identical)
	// and serve its init.mp4. Passing a real segment index here used to
	// restart a mid-film child from zero on every hls.js quality switch.
	if name == "init.mp4" {
		h.ensureRungChild(ctx, parent, *rung, childID, abrAnySeg, false)
		if !h.waitRungSegment(ctx, childID, "init.mp4", abrColdStartWait) {
			http.Error(w, "init not ready", http.StatusServiceUnavailable)
			return
		}
		// Re-fetch AFTER the wait so WorkerAddr is the now-stamped value — a
		// cold child stamps it ~1 s in, and serveChildFile uses it to decide
		// proxy-to-worker vs. local disk.
		child, err := h.sessions.Get(ctx, childID)
		if err != nil {
			http.Error(w, "init not ready", http.StatusServiceUnavailable)
			return
		}
		h.sessions.TouchActivity(ctx, childID)
		h.serveChildFile(w, r, child, childID, "init.mp4")
		return
	}

	globalSeg, err := strconv.Atoi(strings.TrimSuffix(name, ext))
	if err != nil || globalSeg < 0 {
		http.Error(w, "bad segment", http.StatusBadRequest)
		return
	}
	// Reject indices past the end of the media. Any non-negative integer used
	// to be accepted, and an unreachable one is not a cheap 404 — it routes
	// straight into the forced-restart branch below, which kills the running
	// ffmpeg, wipes its directory and dispatches a new job. Alternating an
	// in-range and an out-of-range index therefore span-killed encodes at
	// whatever rate the client could issue requests, across every rung label
	// (separate mutexes) and every permitted parent session. The predicted
	// playlist length is already computed for the variant playlist; reuse it
	// as the ceiling so a bogus index costs a 404 instead of a respawn.
	if total := predictedSegmentCount(parent); total > 0 && globalSeg >= total {
		http.Error(w, "segment out of range", http.StatusNotFound)
		return
	}

	// Create the rung child if absent (ensureRungChild also restarts it when
	// the request is behind its StartSeg).
	h.ensureRungChild(ctx, parent, *rung, childID, globalSeg, false)

	child, err := h.sessions.Get(ctx, childID)
	if err != nil {
		http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
		return
	}
	localSeg := globalSeg - child.StartSeg

	// Restart the encode at this segment unless the running child can serve it
	// promptly — i.e. it's at/before the encode head (already produced) or one
	// of the next few about to be written. A forward seek past the head becomes
	// a ~1 s restart at the target instead of a 30 s wait for a segment that
	// won't land for many seconds. (Backward to before StartSeg was already
	// handled by ensureRungChild above.)
	head := segHead(ctx, child.WorkerAddr, child.DirName(), ext)
	if !abrReachableSoon(head, localSeg) {
		h.ensureRungChild(ctx, parent, *rung, childID, globalSeg, true) // restart at globalSeg
		if child, err = h.sessions.Get(ctx, childID); err != nil {
			http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
			return
		}
		localSeg = 0
	}
	localName := fmt.Sprintf("seg%05d%s", localSeg, ext)

	// Wait for the child to produce the local segment, with a budget matched
	// to what we're waiting FOR. A segment at/behind the encode head should be
	// on disk already (children keep every segment), so a long wait there just
	// delays the recovery restart when the child is actually dead — the case
	// that used to freeze a resume for the full 30 s: an idle-killed child
	// whose session was still in Valkey looked "about to produce" and the
	// handler waited out the whole deadline before restarting. A short-window
	// segment gets a couple of segment-durations; only a cold start earns the
	// full spin-up budget.
	wait := abrColdStartWait
	if head >= 0 {
		wait = abrImminentWait
		if localSeg <= head {
			wait = abrProducedWait
		}
	}
	// A miss means the child died or stalled spinning up; restart once and
	// wait a final time with the full cold-start budget.
	if !h.waitRungSegment(ctx, childID, localName, wait) {
		h.ensureRungChild(ctx, parent, *rung, childID, globalSeg, true)
		if child, err = h.sessions.Get(ctx, childID); err != nil {
			http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
			return
		}
		localName = "seg00000" + ext
		if !h.waitRungSegment(ctx, childID, localName, abrColdStartWait) {
			http.Error(w, "segment not ready", http.StatusServiceUnavailable)
			return
		}
	}

	// Re-fetch so serveChildFile uses the now-stamped WorkerAddr (a cold child's
	// address lands ~1 s after dispatch, after the earlier Get above).
	if fresh, err := h.sessions.Get(ctx, childID); err == nil {
		child = fresh
	}
	h.sessions.TouchActivity(ctx, childID)
	h.serveChildFile(w, r, child, childID, localName)
}

// serveChildFile proxies a rung-child file to the owning worker, or serves it
// from local disk (embedded worker), tagging the content type by extension.
// Both paths address the child's CURRENT incarnation directory — the segment
// server's URL path element and the on-disk name are the same string.
func (h *NativeTranscodeHandler) serveChildFile(w http.ResponseWriter, r *http.Request, child *transcode.Session, childID, name string) {
	if child.WorkerAddr != "" {
		proxyWorkerFile(w, r, child.WorkerAddr, child.DirName(), name)
		return
	}
	switch {
	case strings.HasSuffix(name, ".ts"):
		w.Header().Set("Content-Type", "video/MP2T")
	case strings.HasSuffix(name, ".m4s"), strings.HasSuffix(name, ".mp4"):
		w.Header().Set("Content-Type", "video/mp4")
	}
	http.ServeFile(w, r, filepath.Join(child.Dir(), name))
}

// abrSeekLookahead is how many not-yet-written segments past the encode head
// a request may be before we treat it as a seek and restart. It absorbs a
// player buffering a few segments ahead of a ~realtime encode; anything
// further ahead would mean a 30 s+ wait for the sequential encode to crawl
// there, so restarting at the target is faster.
const abrSeekLookahead = 6

// abrReachableSoon reports whether the running child can serve localSeg
// without a restart: it's at or before the encode head — already produced, and
// children keep every segment (hls_list_size 0 makes delete_segments a no-op)
// — or within the next abrSeekLookahead the encoder is about to write. A
// forward seek past that restarts at the target rather than waiting out a
// segment that won't land for many seconds. head is the child's current encode
// head (-1 = nothing produced yet).
func abrReachableSoon(head, localSeg int) bool {
	if localSeg < 0 {
		return false
	}
	if head < 0 {
		return localSeg <= abrSeekLookahead // child spinning up; seg 0..lookahead are imminent
	}
	return localSeg <= head+abrSeekLookahead
}

// segHead returns the child's current encode head (highest produced segment
// index for ext, -1 if none). Queries the owning worker's /seghead endpoint,
// or scans local disk for a co-located embedded worker (workerAddr empty) —
// worker-aware so the ABR seek/restart decision is correct across a fleet.
// dirName is the child's incarnation-scoped directory name (Session.DirName).
func segHead(ctx context.Context, workerAddr, dirName, ext string) int {
	if workerAddr != "" {
		url := fmt.Sprintf("http://%s/seghead/%s?ext=%s", workerAddr, dirName, ext)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return -1
		}
		authWorkerReq(req)
		resp, err := workerClient.Do(req)
		if err != nil {
			return -1
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return -1
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 32))
		n, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			return -1
		}
		return n
	}
	return transcode.HighestSegmentIndex(transcode.SessionDir(dirName), ext)
}

// highestLocalSegOnDisk scans the local session dir for the encode head. Thin
// wrapper over transcode.HighestSegmentIndex (embedded-worker path + tests).
func highestLocalSegOnDisk(dir, ext string) int {
	return transcode.HighestSegmentIndex(dir, ext)
}

// Wait budgets for waitRungSegment, matched to what the caller is waiting for
// (vars so tests can shorten them):
var (
	// abrColdStartWait covers dispatch → worker claim → ffmpeg start → first
	// segment. 4K HDR on a cold encoder legitimately takes double-digit
	// seconds to seg 0.
	abrColdStartWait = 30 * time.Second
	// abrImminentWait covers a segment within the encoder's lookahead window —
	// roughly two segment-durations of real-time encode.
	abrImminentWait = 10 * time.Second
	// abrProducedWait covers a segment at/behind the encode head, which should
	// already be on disk — anything longer only postpones the dead-child
	// restart.
	abrProducedWait = 4 * time.Second
)

// waitRungSegment polls (up to the given budget) for the child to produce
// localName, stamping activity so the worker's reaper keeps the child alive
// while a client is blocked here.
func (h *NativeTranscodeHandler) waitRungSegment(ctx context.Context, childID, localName string, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		// Re-read the child every poll, for two fields. WorkerAddr: a
		// freshly-dispatched rung child hasn't stamped its address yet (~1 s
		// until the worker claims the job and calls SetWorkerInfo), and polling
		// with an empty addr falls back to the PRIMARY's local disk — where a
		// REMOTE worker never writes — so we'd spin the whole deadline, 503,
		// and only recover on the player's retry. Incarnation: a concurrent
		// restart moves the child to a new directory mid-wait, and polling the
		// old incarnation's path would miss files the new run is writing.
		workerAddr := ""
		dirName := childID
		local := filepath.Join(transcode.SessionDir(childID), localName)
		if child, err := h.sessions.Get(ctx, childID); err == nil {
			workerAddr = child.WorkerAddr
			dirName = child.DirName()
			local = filepath.Join(child.Dir(), localName)
		}
		if workerReady(ctx, workerAddr, dirName, localName, local) {
			return true
		}
		h.sessions.TouchActivity(ctx, childID)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(150 * time.Millisecond):
		}
	}
	return false
}

// abrAnySeg is the ensureRungChild sentinel for "any running child will do" —
// used by the init.mp4 path. The init segment is codec configuration with no
// position, and EVERY incarnation writes one at startup, so a mid-stream child
// serves it as well as a fresh one. Before this sentinel existed the init path
// passed globalSeg=0, which read as "restart unless the child already starts at
// 0" — so an hls.js quality switch mid-film (which refetches the new rung's
// init) KILLED the rung child that was busily encoding at the current position
// and restarted it at the beginning.
const abrAnySeg = -1

// ensureRungChild guarantees a child rung session whose StartSeg lets it
// reach globalSeg by encoding forward. It (re)starts the child when none
// exists, when globalSeg is before the child's StartSeg, or when
// forceRestart is set (forward-seek recovery). globalSeg abrAnySeg accepts
// any existing child (see above). Serialized per child.
func (h *NativeTranscodeHandler) ensureRungChild(ctx context.Context, parent *transcode.Session, rung transcode.Rendition, childID string, globalSeg int, forceRestart bool) {
	mu := abrChildLock(childID)
	mu.Lock()
	defer mu.Unlock()

	child, _ := h.sessions.Get(ctx, childID)
	if child != nil && globalSeg == abrAnySeg {
		return // caller needs an init segment; any live incarnation has one
	}
	if child != nil && !forceRestart && globalSeg >= child.StartSeg {
		return // existing child covers this point (forward-sequential)
	}
	// Minimum interval between FORCED restarts of the same child.
	//
	// A restart is expensive and destructive — kill ffmpeg, os.RemoveAll the
	// directory, dispatch a fresh job — and it happens before the blocking
	// wait, so a client that disconnects immediately still pays the server the
	// full cost. Without a floor, alternating segment requests span-killed
	// encodes as fast as HTTP allowed, and the queued jobs outlived the
	// attacker. A seek-happy player reaches this non-maliciously too.
	//
	// Held under the same per-child mutex as the restart itself, so the check
	// and the stamp cannot race. Legitimate seeking is unaffected: a human
	// cannot scrub faster than this, and a rejected restart simply serves from
	// the existing child (possibly with a longer wait) rather than erroring.
	if forceRestart && child != nil {
		if last, ok := abrLastRestart.Load(childID); ok {
			if t, _ := last.(time.Time); time.Since(t) < abrMinRestartInterval {
				return
			}
		}
		abrLastRestart.Store(childID, time.Now())
	}
	startSeg := globalSeg
	if startSeg == abrAnySeg {
		startSeg = 0
	}

	// Tear down any existing child before restarting at the new offset. The
	// successor takes the SAME session ID under a NEW incarnation: the old
	// run's ffmpeg, its deferred directory wipe, and a remote worker's
	// watchdog all key off the incarnation, so nothing the dying run does can
	// touch the replacement's files — reusing the bare ID here is what used to
	// let the superseded job's delayed cleanup delete the live successor's
	// segments, and let a remote worker keep the old encoder running against
	// the recreated session forever.
	incarnation := 0
	if child != nil {
		incarnation = child.Incarnation + 1
		if h.killer != nil {
			h.killer.KillSession(childID)
		}
		_ = h.sessions.Delete(ctx, childID)
		// Best-effort immediate reclaim of the old incarnation's dir on the
		// embedded box; the worker's session-aware deferred wipe is the
		// backstop (and the only cleanup on a remote worker).
		_ = os.RemoveAll(transcode.SessionDirFor(childID, child.Incarnation))
	}

	startOffset := abrSegmentBoundarySec(startSeg, parent.FrameRate)
	childSess := transcode.Session{
		ID:          childID,
		UserID:      parent.UserID,
		MediaItemID: parent.MediaItemID,
		FileID:      parent.FileID,
		Decision:    "transcode",
		FilePath:    parent.FilePath,
		CreatedAt:   time.Now(),
		SegToken:    parent.SegToken,
		StartSeg:    startSeg,
		Incarnation: incarnation,
		ParentID:    parent.ID, // marks this as a rung child; hidden from /sessions
	}
	if err := h.sessions.Create(ctx, childSess); err != nil {
		h.logger.ErrorContext(ctx, "create rung child session", "child", childID, "err", err)
		return
	}
	// Resurrection guard: if the parent was torn down while we held the child
	// lock (Stop, supersede, or the progress-beacon stop), the cleanup pass may
	// have already run and missed the child we are about to start — leaving an
	// orphan encoder burning a GPU with no parent to stop it. Re-checking AFTER
	// creating the child closes the order: either cleanup saw our child and
	// killed it, or we see the parent gone and undo ourselves. The remaining
	// sliver (parent dies between this check and DispatchJob) self-heals — the
	// dispatched job finds its session deleted on the first watchdog tick and
	// exits within seconds.
	if _, err := h.sessions.Get(ctx, parent.ID); err != nil {
		h.logger.InfoContext(ctx, "parent gone during rung child start; aborting",
			"child", childID, "parent", parent.ID)
		_ = h.sessions.Delete(ctx, childID)
		return
	}
	job := transcode.TranscodeJob{
		SessionID:      childID,
		Incarnation:    incarnation,
		FilePath:       parent.FilePath,
		SourceURL:      parent.SourceURL, // shared across all rungs; minted once in startABR
		SessionDir:     transcode.SessionDirFor(childID, incarnation),
		StartOffsetSec: startOffset,
		// Rebase this run's media timestamps to its true content time. The
		// predicted variant playlist advertises every segment at its global
		// position, but `-ss` alone restarts ffmpeg's timeline at zero — so a
		// child restarted at segment 300 wrote segment 300 carrying segment
		// 0's timestamps, and the player lurched to 0:00 (or stalled) on
		// every rung switch and every post-seek splice.
		OutputTSOffsetSec: startOffset,
		Decision:          "transcode",
		// Encoder unset → the worker picks the best encoder of the requested
		// family: AV1 (PreferAV1) or HEVC (PreferHEVC) → fMP4 .m4s, else the
		// best H.264 → .ts. One codec for the whole ladder, set in startABR.
		// PreferAV1 wins at the worker when both are set, but startABR only
		// ever sets one.
		Encoder:    "",
		PreferHEVC: parent.HEVCOutput,
		PreferAV1:  parent.AV1Output,
		// The variant playlist was already served with .m4s URIs and an
		// EXT-X-MAP init segment whenever the ladder is HEVC/AV1. Pin the
		// container so a worker that falls back to H.264 — no HEVC encoder on
		// the node, or a rotated source forcing libx264 — still writes the
		// files the client is asking for.
		ForceFMP4: abrIsFMP4(parent),
		// Source codec — drives hardware decode on the worker (AV1 NVDEC,
		// opt-in QSV HEVC). Distinct from Prefer* (the output codec).
		IsHEVC:           parent.SourceIsHEVC,
		IsAV1:            parent.SourceIsAV1,
		IsH264:           parent.SourceIsH264,
		Width:            rung.Width,
		Height:           rung.Height,
		BitrateKbps:      rung.BitrateKbps,
		AudioCodec:       "aac",
		AudioChannels:    parent.AudioChannels,
		AudioStreamIndex: parent.AudioStreamIndex,
		NeedsToneMap:     parent.NeedsToneMap,
		EnqueuedAt:       time.Now(),
	}
	if _, err := h.sessions.DispatchJob(ctx, job); err != nil {
		h.logger.ErrorContext(ctx, "dispatch rung child job", "child", childID, "err", err)
		return
	}
	h.logger.InfoContext(ctx, "ABR rung (re)started",
		"child", childID, "rung", rung.Label, "start_seg", startSeg,
		"incarnation", incarnation, "offset_sec", startOffset)
}

// cleanupRungChildren tears down every rung child of an ABR parent.
// Called from Stop/supersede so killing the parent reaps its rungs.
func (h *NativeTranscodeHandler) cleanupRungChildren(ctx context.Context, parent *transcode.Session) {
	for _, rd := range parent.ABRRenditions {
		childID := abrChildID(parent.ID, rd.Label)
		// Read the child BEFORE deleting it: its incarnation names the
		// directory the current run is writing to.
		dir := transcode.SessionDir(childID)
		if child, err := h.sessions.Get(ctx, childID); err == nil {
			dir = child.Dir()
		}
		if h.killer != nil {
			h.killer.KillSession(childID)
		}
		_ = h.sessions.Delete(ctx, childID)
		// Drop the per-child mutex. abrChildLocks is a process-lifetime
		// sync.Map that was only ever inserted into — one entry per playback
		// session per ladder rung, never removed — so a long-running server
		// accumulated a mutex for every rung of every stream it had ever
		// served. This is the natural reap point: the child session is being
		// torn down, so nothing can be waiting on its lock.
		abrChildLocks.Delete(childID)
		abrLastRestart.Delete(childID)
		go func(d string) { time.Sleep(30 * time.Second); _ = os.RemoveAll(d) }(dir)
	}
}

// releaseABRChildLocks drops the per-rung mutexes for a parent whose sessions
// were deleted OUTSIDE cleanupRungChildren — today that's the progress-beacon
// stop, which removes parent and children straight from the store
// (DeleteByMedia matches them by user+item). The processes die via the
// worker's watchdog and the dirs via its session-aware wipe, but the lock map
// is API-process state that only this side can reap; before this, every
// beacon-stopped ABR playback leaked one mutex per rung forever.
func releaseABRChildLocks(parent *transcode.Session) {
	for _, rd := range parent.ABRRenditions {
		childID := abrChildID(parent.ID, rd.Label)
		abrChildLocks.Delete(childID)
		abrLastRestart.Delete(childID)
	}
}

// abrTokenOK validates the segment token is bound to this session, WITHOUT
// the parental gate or usage accrual. Only for internal checks that are not a
// delivery path; request handlers must use authorizeSegmentToken so no live
// entry point can exist without the gate.
func (h *NativeTranscodeHandler) abrTokenOK(ctx context.Context, token, sessionID string) bool {
	tokSession, _, err := h.segToken.Validate(ctx, token)
	return err == nil && tokSession == sessionID
}

// abrLadderCap resolves the ABR ladder height ceiling. An explicit client
// quality pick (requestedHeight>0) wins; otherwise Auto uses autoMax — the soft
// default that keeps Auto off a thrash-prone 4K rung. The operator hard cap
// (hardCap, 0 = none) always applies on top. A 0 return means no cap (the
// ladder runs to source height). autoMax of 0 leaves Auto uncapped.
func abrLadderCap(requestedHeight, autoMax, hardCap int) int {
	ceiling := requestedHeight
	if ceiling == 0 {
		ceiling = autoMax
	}
	if hardCap > 0 && (ceiling == 0 || hardCap < ceiling) {
		ceiling = hardCap
	}
	return ceiling
}

func abrChildID(parentID, rungLabel string) string {
	return filepath.Base(parentID) + "-r" + rungLabel
}

func findRung(rends []transcode.Rendition, label string) *transcode.Rendition {
	for i := range rends {
		if rends[i].Label == label {
			return &rends[i]
		}
	}
	return nil
}
