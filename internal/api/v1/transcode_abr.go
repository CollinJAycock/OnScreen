package v1

import (
	"context"
	"fmt"
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
	sessionID, segTok string, userID, itemID uuid.UUID,
	file *media.File, ladder []transcode.Rendition,
	audioStreamIndex int, needsToneMap bool, positionMS int64,
) {
	ctx := r.Context()
	sess := transcode.Session{
		ID:               sessionID,
		UserID:           userID,
		MediaItemID:      itemID,
		FileID:           file.ID,
		Decision:         "transcode",
		FilePath:         file.FilePath,
		PositionMS:       positionMS,
		CreatedAt:        time.Now(),
		ClientName:       "OnScreenWeb",
		SegToken:         segTok,
		ABR:              true,
		ABRRenditions:    ladder,
		DurationMS:       *file.DurationMS,
		AudioStreamIndex: audioStreamIndex,
		NeedsToneMap:     needsToneMap,
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
	master := transcode.BuildMasterPlaylist(sess.ABRRenditions, func(rd transcode.Rendition) string {
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

	playlist := buildPredictedVariantPlaylist(sess.DurationMS, sess.FrameRate, sessionID, rungLabel, token)
	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write([]byte(playlist))
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
func buildPredictedVariantPlaylist(durationMS int64, fps float64, sid, rung, token string) string {
	segDur := transcode.SegmentDuration
	total := float64(durationMS) / 1000.0

	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", segDur)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n")
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
		fmt.Fprintf(&b, "/api/v1/transcode/sessions/%s/abr/%s/seg/%d.ts?token=%s\n", sid, rung, i, token)
		if end >= total {
			break // this segment reached EOF
		}
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// ABRVariantSegment serves one segment of one rung, transcoding it on
// demand. GET /sessions/{sid}/abr/{rung}/seg/{N}.ts — ensures a child
// transcode session for the rung is producing global segment N (starting
// /seeking ffmpeg to N*SegmentDuration when needed), then serves it.
func (h *NativeTranscodeHandler) ABRVariantSegment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := chi.URLParam(r, "sid")
	rungLabel := chi.URLParam(r, "rung")
	name := chi.URLParam(r, "name")
	token := r.URL.Query().Get("token")

	if !h.abrTokenOK(ctx, token, sessionID) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	globalSeg, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(name), ".ts"))
	if err != nil || globalSeg < 0 {
		http.Error(w, "bad segment", http.StatusBadRequest)
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
	// promptly — i.e. it's already on disk or is one of the next few the
	// encoder is about to write. This turns every seek (forward past the
	// encode head, or backward to a segment the muxer already evicted) into a
	// ~1 s restart instead of a 30 s wait for a segment that will never land.
	if !abrReachableSoon(childID, localSeg) {
		h.ensureRungChild(ctx, parent, *rung, childID, globalSeg, true) // restart at globalSeg
		if child, err = h.sessions.Get(ctx, childID); err != nil {
			http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
			return
		}
		localSeg = 0
	}
	localName := fmt.Sprintf("seg%05d.ts", localSeg)

	// Wait for the child to produce the local segment. A miss here means the
	// child died or stalled spinning up; restart once and wait a final time.
	if !h.waitRungSegment(ctx, child.WorkerAddr, childID, localName) {
		h.ensureRungChild(ctx, parent, *rung, childID, globalSeg, true)
		if child, err = h.sessions.Get(ctx, childID); err != nil {
			http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
			return
		}
		localName = "seg00000.ts"
		if !h.waitRungSegment(ctx, child.WorkerAddr, childID, localName) {
			http.Error(w, "segment not ready", http.StatusServiceUnavailable)
			return
		}
	}

	h.sessions.TouchActivity(ctx, childID)
	if child.WorkerAddr != "" {
		proxyWorkerFile(w, r, child.WorkerAddr, childID, localName)
		return
	}
	w.Header().Set("Content-Type", "video/MP2T")
	http.ServeFile(w, r, filepath.Join(transcode.SessionDir(childID), localName))
}

// abrSeekLookahead is how many not-yet-written segments past the encode head
// a request may be before we treat it as a seek and restart. It absorbs a
// player buffering a few segments ahead of a ~realtime encode; anything
// further ahead would mean a 30 s+ wait for the sequential encode to crawl
// there, so restarting at the target is faster.
const abrSeekLookahead = 6

// abrReachableSoon reports whether the running child can serve localSeg
// without a restart: the segment is already on disk, or it's within the next
// abrSeekLookahead segments the encoder is about to write. A request below the
// encode head that's absent (evicted by delete_segments) or far above it
// (forward seek) is NOT reachable and should trigger a restart.
//
// Disk-based: assumes the child's segments are local to this process (the
// embedded-worker / single-instance ABR configuration). A multi-instance
// worker tier would query the worker instead.
func abrReachableSoon(childID string, localSeg int) bool {
	if localSeg < 0 {
		return false
	}
	dir := transcode.SessionDir(childID)
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("seg%05d.ts", localSeg))); err == nil {
		return true // already produced
	}
	hi := highestLocalSegOnDisk(dir)
	return localSeg > hi && localSeg <= hi+abrSeekLookahead
}

// highestLocalSegOnDisk returns the largest segNNNNN.ts index present in dir,
// or -1 if none. This is the child's current encode head (delete_segments
// keeps a trailing window, so absent-below-head means evicted).
func highestLocalSegOnDisk(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return -1
	}
	hi := -1
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, "seg") || !strings.HasSuffix(n, ".ts") {
			continue
		}
		if idx, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(n, "seg"), ".ts")); err == nil && idx > hi {
			hi = idx
		}
	}
	return hi
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
		SessionDir:     transcode.SessionDir(childID),
		StartOffsetSec: abrSegmentBoundarySec(globalSeg, parent.FrameRate),
		Decision:       "transcode",
		Encoder:        "", // worker picks the best H.264 encoder (.ts ladder)
		Width:          rung.Width,
		Height:         rung.Height,
		BitrateKbps:    rung.BitrateKbps,
		AudioCodec:     "aac",
		AudioChannels:  2,
		AudioStreamIndex: parent.AudioStreamIndex,
		NeedsToneMap:   parent.NeedsToneMap,
		EnqueuedAt:     time.Now(),
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
