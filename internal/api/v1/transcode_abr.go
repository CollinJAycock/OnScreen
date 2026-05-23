package v1

import (
	"context"
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

// startABR creates an ABR parent session (no ffmpeg) and responds with
// the master playlist URL. Called from Start when ABR is enabled, the
// decision is a full re-encode, and the source has a usable ladder.
func (h *NativeTranscodeHandler) startABR(
	w http.ResponseWriter, r *http.Request,
	sessionID, segTok, sourceURL string, userID, itemID uuid.UUID,
	file *media.File, ladder []transcode.Rendition,
	audioStreamIndex int, needsToneMap bool, codec string, positionMS int64,
) {
	ctx := r.Context()
	sess := transcode.Session{
		ID:               sessionID,
		UserID:           userID,
		MediaItemID:      itemID,
		FileID:           file.ID,
		Decision:         "transcode",
		FilePath:         file.FilePath,
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
		NeedsToneMap:     needsToneMap,
		// HEVC/AV1 ladders are fMP4 (.m4s + init.mp4); H.264 stays mpegts
		// .ts. One codec for the whole ladder, chosen by client capability
		// in Start.
		HEVCOutput: codec == transcode.LadderHEVC,
		AV1Output:  codec == transcode.LadderAV1,
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
	if err := h.sessions.Create(ctx, sess); err != nil {
		h.logger.ErrorContext(ctx, "create ABR session", "err", err)
		respond.InternalError(w, r)
		return
	}
	h.logger.InfoContext(ctx, "ABR session created",
		"session_id", sessionID, "rungs", len(ladder),
		"duration_ms", sess.DurationMS, "top_rung", ladder[0].Label)

	respond.Success(w, r, transcodeStartResponse{
		SessionID:      sessionID,
		PlaylistURL:    fmt.Sprintf("/api/v1/transcode/sessions/%s/playlist.m3u8?token=%s", sessionID, segTok),
		Token:          segTok,
		StartOffsetSec: float64(positionMS) / 1000.0,
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
		return fmt.Sprintf("/api/v1/transcode/sessions/%s/abr/%s/index.m3u8?token=%s", sess.ID, rd.Label, token)
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

	if !h.abrTokenOK(ctx, token, sessionID) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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

	playlist := buildPredictedVariantPlaylist(sess.DurationMS, sess.FrameRate, sessionID, rungLabel, token, abrIsFMP4(sess))
	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write([]byte(playlist))
}

// abrIsFMP4 reports whether the ladder uses fMP4 (.m4s + init.mp4) segments.
// Both HEVC and AV1 rungs do; H.264 uses MPEG-TS .ts.
func abrIsFMP4(sess *transcode.Session) bool {
	return sess.HEVCOutput || sess.AV1Output
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
func buildPredictedVariantPlaylist(durationMS int64, fps float64, sid, rung, token string, fmp4 bool) string {
	segDur := transcode.SegmentDuration
	total := float64(durationMS) / 1000.0
	ext := abrSegExt(fmp4)

	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", segDur)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n")
	// fMP4 (HEVC) segments need the shared init segment up front.
	if fmp4 {
		fmt.Fprintf(&b, "#EXT-X-MAP:URI=\"/api/v1/transcode/sessions/%s/abr/%s/seg/init.mp4?token=%s\"\n", sid, rung, token)
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
		fmt.Fprintf(&b, "/api/v1/transcode/sessions/%s/abr/%s/seg/%d%s?token=%s\n", sid, rung, i, ext, token)
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

	if !h.abrTokenOK(ctx, token, sessionID) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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
	// child is running (from the start if none) and serve its init.mp4; any
	// child of this rung has a compatible init.
	if name == "init.mp4" {
		h.ensureRungChild(ctx, parent, *rung, childID, 0, false)
		child, err := h.sessions.Get(ctx, childID)
		if err != nil || !h.waitRungSegment(ctx, child.WorkerAddr, childID, "init.mp4") {
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
	head := segHead(ctx, child.WorkerAddr, childID, ext)
	if !abrReachableSoon(head, localSeg) {
		h.ensureRungChild(ctx, parent, *rung, childID, globalSeg, true) // restart at globalSeg
		if child, err = h.sessions.Get(ctx, childID); err != nil {
			http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
			return
		}
		localSeg = 0
	}
	localName := fmt.Sprintf("seg%05d%s", localSeg, ext)

	// Wait for the child to produce the local segment. A miss here means the
	// child died or stalled spinning up; restart once and wait a final time.
	if !h.waitRungSegment(ctx, child.WorkerAddr, childID, localName) {
		h.ensureRungChild(ctx, parent, *rung, childID, globalSeg, true)
		if child, err = h.sessions.Get(ctx, childID); err != nil {
			http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
			return
		}
		localName = "seg00000" + ext
		if !h.waitRungSegment(ctx, child.WorkerAddr, childID, localName) {
			http.Error(w, "segment not ready", http.StatusServiceUnavailable)
			return
		}
	}

	h.sessions.TouchActivity(ctx, childID)
	h.serveChildFile(w, r, child, childID, localName)
}

// serveChildFile proxies a rung-child file to the owning worker, or serves it
// from local disk (embedded worker), tagging the content type by extension.
func (h *NativeTranscodeHandler) serveChildFile(w http.ResponseWriter, r *http.Request, child *transcode.Session, childID, name string) {
	if child.WorkerAddr != "" {
		proxyWorkerFile(w, r, child.WorkerAddr, childID, name)
		return
	}
	switch {
	case strings.HasSuffix(name, ".ts"):
		w.Header().Set("Content-Type", "video/MP2T")
	case strings.HasSuffix(name, ".m4s"), strings.HasSuffix(name, ".mp4"):
		w.Header().Set("Content-Type", "video/mp4")
	}
	http.ServeFile(w, r, filepath.Join(transcode.SessionDir(childID), name))
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
func segHead(ctx context.Context, workerAddr, childID, ext string) int {
	if workerAddr != "" {
		url := fmt.Sprintf("http://%s/seghead/%s?ext=%s", workerAddr, childID, ext)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return -1
		}
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
	return transcode.HighestSegmentIndex(transcode.SessionDir(childID), ext)
}

// highestLocalSegOnDisk scans the local session dir for the encode head. Thin
// wrapper over transcode.HighestSegmentIndex (embedded-worker path + tests).
func highestLocalSegOnDisk(dir, ext string) int {
	return transcode.HighestSegmentIndex(dir, ext)
}

// waitRungSegment polls (up to ~30s) for the child to produce localName,
// stamping activity so the worker's reaper keeps the child alive while a
// client is blocked here.
func (h *NativeTranscodeHandler) waitRungSegment(ctx context.Context, workerAddr, childID, localName string) bool {
	deadline := time.Now().Add(30 * time.Second)
	local := filepath.Join(transcode.SessionDir(childID), localName)
	for time.Now().Before(deadline) {
		if workerReady(ctx, workerAddr, childID, localName, local) {
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

// ensureRungChild guarantees a child rung session whose StartSeg lets it
// reach globalSeg by encoding forward. It (re)starts the child when none
// exists, when globalSeg is before the child's StartSeg, or when
// forceRestart is set (forward-seek recovery). Serialized per child.
func (h *NativeTranscodeHandler) ensureRungChild(ctx context.Context, parent *transcode.Session, rung transcode.Rendition, childID string, globalSeg int, forceRestart bool) {
	mu := abrChildLock(childID)
	mu.Lock()
	defer mu.Unlock()

	child, _ := h.sessions.Get(ctx, childID)
	if child != nil && !forceRestart && globalSeg >= child.StartSeg {
		return // existing child covers this point (forward-sequential)
	}

	// Tear down any existing child ffmpeg + stale segments before
	// restarting at the new offset (its local seg numbering resets to 0).
	if child != nil {
		if h.killer != nil {
			h.killer.KillSession(childID)
		}
		_ = h.sessions.Delete(ctx, childID)
		_ = os.RemoveAll(transcode.SessionDir(childID))
	}

	childSess := transcode.Session{
		ID:          childID,
		UserID:      parent.UserID,
		MediaItemID: parent.MediaItemID,
		FileID:      parent.FileID,
		Decision:    "transcode",
		FilePath:    parent.FilePath,
		CreatedAt:   time.Now(),
		SegToken:    parent.SegToken,
		StartSeg:    globalSeg,
		ParentID:    parent.ID, // marks this as a rung child; hidden from /sessions
	}
	if err := h.sessions.Create(ctx, childSess); err != nil {
		h.logger.ErrorContext(ctx, "create rung child session", "child", childID, "err", err)
		return
	}
	job := transcode.TranscodeJob{
		SessionID:      childID,
		FilePath:       parent.FilePath,
		SourceURL:      parent.SourceURL, // shared across all rungs; minted once in startABR
		SessionDir:     transcode.SessionDir(childID),
		StartOffsetSec: abrSegmentBoundarySec(globalSeg, parent.FrameRate),
		Decision:       "transcode",
		// Encoder unset → the worker picks the best encoder of the requested
		// family: AV1 (PreferAV1) or HEVC (PreferHEVC) → fMP4 .m4s, else the
		// best H.264 → .ts. One codec for the whole ladder, set in startABR.
		// PreferAV1 wins at the worker when both are set, but startABR only
		// ever sets one.
		Encoder:          "",
		PreferHEVC:       parent.HEVCOutput,
		PreferAV1:        parent.AV1Output,
		Width:            rung.Width,
		Height:           rung.Height,
		BitrateKbps:      rung.BitrateKbps,
		AudioCodec:       "aac",
		AudioChannels:    2,
		AudioStreamIndex: parent.AudioStreamIndex,
		NeedsToneMap:     parent.NeedsToneMap,
		EnqueuedAt:       time.Now(),
	}
	if _, err := h.sessions.DispatchJob(ctx, job); err != nil {
		h.logger.ErrorContext(ctx, "dispatch rung child job", "child", childID, "err", err)
		return
	}
	h.logger.InfoContext(ctx, "ABR rung (re)started",
		"child", childID, "rung", rung.Label, "start_seg", globalSeg,
		"offset_sec", abrSegmentBoundarySec(globalSeg, parent.FrameRate))
}

// cleanupRungChildren tears down every rung child of an ABR parent.
// Called from Stop/supersede so killing the parent reaps its rungs.
func (h *NativeTranscodeHandler) cleanupRungChildren(ctx context.Context, parent *transcode.Session) {
	for _, rd := range parent.ABRRenditions {
		childID := abrChildID(parent.ID, rd.Label)
		if h.killer != nil {
			h.killer.KillSession(childID)
		}
		_ = h.sessions.Delete(ctx, childID)
		dir := transcode.SessionDir(childID)
		go func() { time.Sleep(30 * time.Second); _ = os.RemoveAll(dir) }()
	}
}

// abrTokenOK validates the segment token is bound to this session.
func (h *NativeTranscodeHandler) abrTokenOK(ctx context.Context, token, sessionID string) bool {
	tokSession, _, err := h.segToken.Validate(ctx, token)
	return err == nil && tokSession == sessionID
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
