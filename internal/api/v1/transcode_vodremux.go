package v1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
	"github.com/onscreen/onscreen/internal/valkey"
)

// VOD remux: a stream-copy session served as one complete VOD playlist
// (internal/transcode/vodremux.go has the why and the ffmpeg details).
//
// The PARENT session (VODRemux=true) runs nothing; playlist.m3u8 renders
// the plan. One CHILD session, {parent}-vr, runs the remux from a segment
// boundary and writes a piece per keyframe. A segment request maps the
// segment onto the child's pieces through the child's own playlist and
// serves them back to back; a request the child can't reach soon (before
// its start, or far past its head) restarts it at that boundary. The child
// reuses the ABR rung machinery: per-child lock, forced-restart floor,
// incarnation-scoped directories, worker proxying.

const vodRemuxChildSuffix = "-vr"

func vodRemuxChildID(parentID string) string { return parentID + vodRemuxChildSuffix }

// vodRemuxSeekAhead: a segment starting this far past the running child's
// head is a seek — restart there rather than wait for the paced remux (1x
// after its 60 s burst) to crawl to it.
const vodRemuxSeekAhead = 30.0

// vodRemuxPoll is how often a waiting segment request re-reads the run.
var vodRemuxPoll = 200 * time.Millisecond

// vodRemuxUnreadableAfter: a run this old whose playlist can't be read is
// treated as dead and restarted (a cold start writes its first piece in a
// second or two; the worker only wipes a directory whose session is gone).
var vodRemuxUnreadableAfter = 15 * time.Second

// vodRemuxPlan returns the plan when this Start can be a VOD remux: the
// server allows it, the client declared vodRemux=1, the video is being
// copied from a single MKV whose keyframe index plans, nothing else
// (burn-in, multi-part, audio-only) needs the ordinary path, and a worker
// can run it. Without such a worker (only v2.7 remote workers) the client
// gets the ordinary EVENT remux, which every worker can serve.
func (h *NativeTranscodeHandler) vodRemuxPlan(ctx context.Context, caps transcode.ClientCapabilities, file *media.File,
	videoCopy, multiPart, burnIn, audioOnly bool) (transcode.RemuxPlan, float64, bool) {
	if !h.cfg.TranscodeVODRemux || !caps.AcceptsVODRemux || !videoCopy || multiPart || burnIn || audioOnly ||
		file == nil || file.DurationMS == nil || *file.DurationMS <= 0 {
		return transcode.RemuxPlan{}, 0, false
	}
	if h.sessions == nil || !h.sessions.FleetHasFeature(ctx, transcode.WorkerFeatureVODRemux) {
		return transcode.RemuxPlan{}, 0, false
	}
	return transcode.LoadRemuxPlan(ctx, file.FilePath, *file.DurationMS)
}

// startVODRemux turns the session Start already created (cap-checked) into
// a VOD-remux parent, starts the child at the resume position's segment so
// the first fetch finds it warm, and answers Start: the playlist covers the
// whole file, so StartOffsetSec is 0 and the client resumes by seeking (the
// ABR contract).
func (h *NativeTranscodeHandler) startVODRemux(w http.ResponseWriter, r *http.Request, sess transcode.Session,
	job transcode.TranscodeJob, plan transcode.RemuxPlan, offset float64, durationMS, positionMS int64) {
	ctx := r.Context()
	sess.VODRemux = true
	sess.RemuxTSOffset = offset
	sess.DurationMS = durationMS
	tmpl := job
	tmpl.StartOffsetSec = 0 // per child run
	sess.RemuxJob = &tmpl
	if err := h.sessions.Create(ctx, sess); err != nil {
		h.logger.ErrorContext(ctx, "store VOD remux session", "session_id", sess.ID, "err", err)
		h.tearDown(context.WithoutCancel(ctx), &sess, sess.SegToken)
		respond.InternalError(w, r)
		return
	}
	startSeg := plan.SegmentAt(float64(positionMS) / 1000)
	// Best effort: a warm start only. The first segment fetch retries the
	// dispatch, and answers 503 if the store is still failing.
	_ = h.ensureVODRemuxChild(ctx, &sess, plan, startSeg, false)
	h.logger.InfoContext(ctx, "VOD remux session created",
		"session_id", sess.ID, "segments", plan.Count(), "start_seg", startSeg,
		"ts_offset", offset, "duration_ms", durationMS)
	respond.Success(w, r, transcodeStartResponse{
		SessionID:      sess.ID,
		PlaylistURL:    fmt.Sprintf("/api/v1/transcode/sessions/%s/playlist.m3u8?token=%s", sess.ID, sess.SegToken),
		Token:          sess.SegToken,
		StartOffsetSec: 0,
		SegmentFormat:  segmentFormatOf(sess.VideoOutput()),
		FullTimeline:   true,
	})
}

// serveVODRemuxPlaylist writes the plan's VOD playlist for a parent.
func (h *NativeTranscodeHandler) serveVODRemuxPlaylist(w http.ResponseWriter, r *http.Request, sess *transcode.Session, token string) {
	plan, _, ok := transcode.LoadRemuxPlan(r.Context(), sess.FilePath, sess.DurationMS)
	if !ok {
		// The file changed under the session (replaced, re-muxed): the plan
		// its segments were numbered by is gone.
		http.Error(w, "remux plan unavailable", http.StatusGone)
		return
	}
	vout := sess.VideoOutput()
	ext := vout.SegExt()
	base := h.segBase()
	initURL := ""
	if vout.NeedsFMP4() {
		initURL = publicSeg(base, fmt.Sprintf("/api/v1/transcode/sessions/%s/vr/seg/init.mp4?token=%s", sess.ID, token))
	}
	body := plan.Playlist(func(i int) string {
		return publicSeg(base, fmt.Sprintf("/api/v1/transcode/sessions/%s/vr/seg/%d%s?token=%s", sess.ID, i, ext, token))
	}, initURL)
	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write([]byte(body))
}

// ensureVODRemuxChild guarantees a child run that can reach segment seg by
// remuxing forward: it starts one when none exists, when seg is before the
// running child's start, or when forceRestart is set (a seek past the head,
// or pieces that don't match the plan). seg abrAnySeg (the fMP4 init) takes
// any running child, and starts a missing one at the parent's resume point.
//
// It returns an error only when the session store can't say whether a child
// exists; the caller answers 503 rather than wait on a run nobody started.
func (h *NativeTranscodeHandler) ensureVODRemuxChild(ctx context.Context, parent *transcode.Session, plan transcode.RemuxPlan, seg int, forceRestart bool) error {
	childID := vodRemuxChildID(parent.ID)
	unlock, ok := h.lockRungChild(ctx, childID)
	if !ok {
		return nil // another instance holds this child's lock and is (re)starting it
	}
	defer unlock()

	child, err := h.sessions.Get(ctx, childID)
	if err != nil && !errors.Is(err, valkey.ErrNotFound) {
		// A store error isn't "no child": dispatching on it would start a
		// second run beside a live one, which then fight over the directory.
		h.logger.WarnContext(ctx, "VOD remux: child lookup failed", "child", childID, "err", err)
		return fmt.Errorf("look up VOD remux child: %w", err)
	}
	if child != nil && seg == abrAnySeg {
		return nil
	}
	if child != nil && !forceRestart && seg >= child.StartSeg {
		return nil
	}
	if forceRestart && child != nil && !h.claimForcedRestart(ctx, childID) {
		return nil // same restart floor as an ABR rung
	}
	startSeg := seg
	if startSeg == abrAnySeg {
		startSeg = plan.SegmentAt(float64(parent.PositionMS) / 1000)
	}
	if startSeg < 0 || startSeg >= plan.Count() {
		startSeg = 0
	}

	incarnation := 0
	if child != nil {
		incarnation = child.Incarnation + 1
		if h.killer != nil {
			h.killer.KillSession(childID)
		}
		_ = h.sessions.Delete(ctx, childID)
		_ = os.RemoveAll(transcode.SessionDirFor(childID, child.Incarnation))
	}
	childSess := transcode.Session{
		ID:          childID,
		UserID:      parent.UserID,
		MediaItemID: parent.MediaItemID,
		FileID:      parent.FileID,
		Decision:    "remux",
		FilePath:    parent.FilePath,
		CreatedAt:   time.Now(),
		SegToken:    parent.SegToken,
		StartSeg:    startSeg,
		Incarnation: incarnation,
		ParentID:    parent.ID, // hidden from /sessions and the stream caps
	}
	if err := h.sessions.Create(ctx, childSess); err != nil {
		h.logger.ErrorContext(ctx, "create VOD remux child", "child", childID, "err", err)
		return fmt.Errorf("create VOD remux child: %w", err)
	}
	// Resurrection guard, as for ABR rungs: a parent torn down while we held
	// the lock must not be left with an orphan run.
	if _, err := h.sessions.Get(ctx, parent.ID); err != nil {
		_ = h.sessions.Delete(ctx, childID)
		return nil
	}
	job := *parent.RemuxJob
	job.SessionID = childID
	job.Incarnation = incarnation
	job.SessionDir = transcode.SessionDirFor(childID, incarnation)
	job.StartOffsetSec = plan.SeekTarget(startSeg)
	job.OutputTSOffsetSec = parent.RemuxTSOffset
	job.CopyTS = true
	job.KeyframePieces = true
	job.EnqueuedAt = time.Now()
	if _, err := h.sessions.DispatchJob(ctx, job); err != nil {
		h.logger.ErrorContext(ctx, "dispatch VOD remux child", "child", childID, "err", err)
		return fmt.Errorf("dispatch VOD remux child: %w", err)
	}
	h.logger.InfoContext(ctx, "VOD remux run (re)started",
		"child", childID, "start_seg", startSeg, "seek", job.StartOffsetSec, "incarnation", incarnation)
	return nil
}

// cleanupVODRemuxChild tears down a parent's child run (Stop / supersede).
func (h *NativeTranscodeHandler) cleanupVODRemuxChild(ctx context.Context, parent *transcode.Session) {
	childID := vodRemuxChildID(parent.ID)
	dir := transcode.SessionDir(childID)
	if child, err := h.sessions.Get(ctx, childID); err == nil {
		dir = child.Dir()
	}
	if h.killer != nil {
		h.killer.KillSession(childID)
	}
	if err := h.sessions.Delete(ctx, childID); err != nil {
		h.logger.WarnContext(ctx, "transcode teardown: delete vod-remux child failed",
			"session_id", parent.ID, "child_id", childID, "err", err)
	}
	releaseVODRemuxLocks(parent)
	go func(d string) { time.Sleep(30 * time.Second); _ = os.RemoveAll(d) }(dir)
}

// releaseVODRemuxLocks drops the child's lock entries (the progress-beacon
// stop deletes the sessions without going through tearDown).
func releaseVODRemuxLocks(parent *transcode.Session) {
	childID := vodRemuxChildID(parent.ID)
	abrChildLocks.Delete(childID)
	abrLastRestart.Delete(childID)
}

// VODRemuxSegment serves one segment of a VOD remux.
// GET /sessions/{sid}/vr/seg/{name} — name is "N.ts", "N.m4s" or "init.mp4".
func (h *NativeTranscodeHandler) VODRemuxSegment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := chi.URLParam(r, "sid")
	name := filepath.Base(chi.URLParam(r, "name"))
	token := r.URL.Query().Get("token")

	if !h.authorizeSegmentToken(w, r, token, sessionID) {
		return
	}
	parent, err := h.sessions.Get(ctx, sessionID)
	if err != nil && !errors.Is(err, valkey.ErrNotFound) {
		h.logger.WarnContext(ctx, "VOD remux: session lookup failed", "session_id", sessionID, "err", err)
		storeUnavailable(w)
		return
	}
	if err != nil || !parent.VODRemux || parent.RemuxJob == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	plan, _, ok := transcode.LoadRemuxPlan(ctx, parent.FilePath, parent.DurationMS)
	if !ok {
		http.Error(w, "remux plan unavailable", http.StatusGone)
		return
	}
	childID := vodRemuxChildID(sessionID)
	ext := parent.VideoOutput().SegExt()
	h.sessions.TouchActivity(ctx, sessionID) // keeps the parent's /sessions card live

	if name == "init.mp4" {
		if err := h.ensureVODRemuxChild(ctx, parent, plan, abrAnySeg, false); err != nil {
			storeUnavailable(w)
			return
		}
		if !h.waitRungSegment(ctx, childID, "init.mp4", abrColdStartWait) {
			http.Error(w, "init not ready", http.StatusServiceUnavailable)
			return
		}
		child, err := h.sessions.Get(ctx, childID)
		if err != nil {
			http.Error(w, "init not ready", http.StatusServiceUnavailable)
			return
		}
		h.sessions.TouchActivity(ctx, childID)
		h.serveChildFile(w, r, child, childID, "init.mp4")
		return
	}

	seg, err := strconv.Atoi(strings.TrimSuffix(name, ext))
	if err != nil || seg < 0 || !strings.HasSuffix(name, ext) {
		http.Error(w, "bad segment", http.StatusBadRequest)
		return
	}
	if seg >= plan.Count() {
		http.Error(w, "segment out of range", http.StatusNotFound)
		return
	}
	segStart, segEnd := plan.Span(seg)
	lastSeg := seg == plan.Count()-1

	if err := h.ensureVODRemuxChild(ctx, parent, plan, seg, false); err != nil {
		storeUnavailable(w)
		return
	}

	restarted := false
	deadline := time.Now().Add(abrColdStartWait)
	for {
		child, err := h.sessions.Get(ctx, childID)
		if err == nil && child.StartSeg >= 0 && child.StartSeg < plan.Count() {
			text, ok := readRunPlaylist(ctx, child)
			if !ok && !restarted && time.Since(child.CreatedAt) > vodRemuxUnreadableAfter {
				// A run that should long since have written its playlist
				// hasn't, or it's gone (its directory outlived by a very long
				// pause): start a fresh one here rather than wait it out.
				if err := h.ensureVODRemuxChild(ctx, parent, plan, seg, true); err != nil {
					storeUnavailable(w)
					return
				}
				restarted = true
				deadline = time.Now().Add(abrColdStartWait)
				continue
			}
			if ok {
				durs, ended := transcode.ParseRunPieces(text)
				runStart := plan.Starts[child.StartSeg]
				span := transcode.RemuxPieceSpan(runStart, durs, ended, segStart, segEnd, lastSeg)
				if span.Ready {
					h.sessions.TouchActivity(ctx, childID)
					servePieces(w, r, child, span, ext)
					return
				}
				head := runStart
				for _, d := range durs {
					head += d
				}
				if span.Mismatch || segStart-head > vodRemuxSeekAhead {
					if restarted {
						if span.Mismatch {
							h.logger.WarnContext(ctx, "VOD remux pieces don't match the plan",
								"session_id", sessionID, "seg", seg, "run_start", runStart, "pieces", len(durs))
						}
						http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
						return
					}
					if err := h.ensureVODRemuxChild(ctx, parent, plan, seg, true); err != nil {
						storeUnavailable(w)
						return
					}
					restarted = true
					deadline = time.Now().Add(abrColdStartWait)
					continue
				}
			}
		} else if errors.Is(err, valkey.ErrNotFound) && !restarted {
			// The child was reaped (idle) while the player was paused. (Any
			// other lookup error is the store failing, not a missing child:
			// keep polling until the deadline instead of dispatching a run.)
			if err := h.ensureVODRemuxChild(ctx, parent, plan, seg, false); err != nil {
				storeUnavailable(w)
				return
			}
			restarted = true
			deadline = time.Now().Add(abrColdStartWait)
		}
		if time.Now().After(deadline) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "segment not ready", http.StatusServiceUnavailable)
			return
		}
		h.sessions.TouchActivity(ctx, childID)
		select {
		case <-ctx.Done():
			return
		case <-time.After(vodRemuxPoll):
		}
	}
}

// readRunPlaylist reads a child run's own playlist (index.m3u8) from its
// worker, or from local disk for the embedded worker.
func readRunPlaylist(ctx context.Context, child *transcode.Session) (string, bool) {
	if child.WorkerAddr == "" {
		b, err := os.ReadFile(filepath.Join(child.Dir(), "index.m3u8"))
		return string(b), err == nil
	}
	// The worker's /segments route blocks up to 10 s for a missing file;
	// the caller polls, so don't let one read eat that.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://%s/segments/%s/index.m3u8", child.WorkerAddr, child.DirName())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	authWorkerReq(req)
	resp, err := workerClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return string(b), err == nil
}

// servePieces writes pieces span.First..span.Last of a child run as one
// segment. MPEG-TS packets and fMP4 fragments both concatenate; the pieces
// are complete (temp_file renames them into place), so their sizes give an
// exact Content-Length.
func servePieces(w http.ResponseWriter, r *http.Request, child *transcode.Session, span transcode.PieceSpan, ext string) {
	names := make([]string, 0, span.Last-span.First+1)
	for j := span.First; j <= span.Last; j++ {
		names = append(names, fmt.Sprintf("seg%05d%s", j, ext))
	}
	if ext == ".ts" {
		w.Header().Set("Content-Type", "video/MP2T")
	} else {
		w.Header().Set("Content-Type", "video/mp4")
	}

	if child.WorkerAddr == "" {
		var total int64
		for _, n := range names {
			st, err := os.Stat(filepath.Join(child.Dir(), n))
			if err != nil {
				http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
				return
			}
			total += st.Size()
		}
		w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		for _, n := range names {
			f, err := os.Open(filepath.Join(child.Dir(), n))
			if err != nil {
				return // mid-body: the player sees a short segment and retries
			}
			_, _ = io.Copy(w, f)
			f.Close()
		}
		return
	}

	if r.Method == http.MethodHead {
		var total int64
		for _, n := range names {
			size, ok := workerFileSize(r.Context(), child, n)
			if !ok {
				http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
				return
			}
			total += size
		}
		w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
		w.WriteHeader(http.StatusOK)
		return
	}

	// Open every piece before answering: once the 200 and Content-Length are
	// out, a piece the worker can't serve (404 after the run was wiped, 503
	// from a busy worker) used to have its error body spliced into the
	// segment. Checked here it is a clean 503 the player retries. A span is a
	// handful of keyframe pieces, so holding their bodies open is cheap.
	type piece struct {
		body io.ReadCloser
		size int64
	}
	pieces := make([]piece, 0, len(names))
	defer func() {
		for _, p := range pieces {
			p.body.Close()
		}
	}()
	var total int64
	for _, n := range names {
		body, size, err := openWorkerPiece(r.Context(), child, n)
		if err != nil {
			http.Error(w, "segment unavailable", http.StatusServiceUnavailable)
			return
		}
		pieces = append(pieces, piece{body, size})
		total += size
	}
	w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
	w.WriteHeader(http.StatusOK)
	for _, p := range pieces {
		if n, err := io.Copy(w, p.body); err != nil || n != p.size {
			// Mid-body: stop short of the declared length, so net/http drops
			// the connection and the player retries instead of decoding a
			// segment with a hole in it.
			return
		}
	}
}

// openWorkerPiece GETs one finished piece from a child's worker, refusing
// anything but a 200 with a known length (the pieces' sizes are the
// segment's Content-Length). The caller closes the body.
func openWorkerPiece(ctx context.Context, child *transcode.Session, name string) (io.ReadCloser, int64, error) {
	url := fmt.Sprintf("http://%s/segments/%s/%s", child.WorkerAddr, child.DirName(), name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build piece request: %w", err)
	}
	authWorkerReq(req)
	resp, err := workerClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch piece %s: %w", name, err)
	}
	if resp.StatusCode != http.StatusOK || resp.ContentLength < 0 {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("fetch piece %s: status %d, length %d", name, resp.StatusCode, resp.ContentLength)
	}
	return resp.Body, resp.ContentLength, nil
}

// storeUnavailable answers a segment request the session store couldn't
// serve. The player retries a 503; it would choke on a segment from a run
// started beside a live one.
func storeUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	http.Error(w, "session store unavailable", http.StatusServiceUnavailable)
}

// workerFileSize HEADs a finished file on a child's worker.
func workerFileSize(ctx context.Context, child *transcode.Session, name string) (int64, bool) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://%s/segments/%s/%s", child.WorkerAddr, child.DirName(), name)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, false
	}
	authWorkerReq(req)
	resp, err := workerClient.Do(req)
	if err != nil {
		return 0, false
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength < 0 {
		return 0, false
	}
	return resp.ContentLength, true
}
