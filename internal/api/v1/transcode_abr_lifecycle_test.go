package v1

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

// abrTestParent creates and stores an ABR parent session with one 720p rung.
func abrTestParent(t *testing.T, store *transcode.SessionStore) (*transcode.Session, transcode.Rendition) {
	t.Helper()
	rung := transcode.Rendition{Label: "720p", Width: 1280, Height: 720, BitrateKbps: 3000}
	parent := transcode.Session{
		ID:            "abr-parent-" + uuid.NewString(),
		UserID:        uuid.New(),
		MediaItemID:   uuid.New(),
		FileID:        uuid.New(),
		Decision:      "transcode",
		FilePath:      "/media/test.mkv",
		CreatedAt:     time.Now(),
		SegToken:      "tok",
		ABR:           true,
		ABRRenditions: []transcode.Rendition{rung},
		DurationMS:    2 * 60 * 60 * 1000,
		FrameRate:     24,
	}
	if err := store.Create(context.Background(), parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	return &parent, rung
}

// drainJob pops the next enqueued transcode job (no workers are registered in
// tests, so DispatchJob falls back to the global queue).
func drainJob(t *testing.T, store *transcode.SessionStore) *transcode.TranscodeJob {
	t.Helper()
	job, err := store.DequeueJob(context.Background(), "", 2*time.Second)
	if err != nil || job == nil {
		t.Fatalf("expected an enqueued rung job, got job=%v err=%v", job, err)
	}
	return job
}

// TestEnsureRungChild_RestartBumpsIncarnation pins the fix for the audit's
// critical: a rung restart reuses its session ID, so everything that outlives
// the old run — its ffmpeg, its deferred dir wipe, a remote worker's watchdog
// — must be able to tell the two runs apart. The incarnation is that
// discriminator, and the job must carry it plus a directory scoped by it.
func TestEnsureRungChild_RestartBumpsIncarnation(t *testing.T) {
	h, store := newTestHandler(t)
	ctx := context.Background()
	parent, rung := abrTestParent(t, store)
	childID := abrChildID(parent.ID, rung.Label)

	// First start: incarnation 0, bare directory (historical layout).
	h.ensureRungChild(ctx, parent, rung, childID, 0, false)
	child, err := store.Get(ctx, childID)
	if err != nil {
		t.Fatalf("child not created: %v", err)
	}
	if child.Incarnation != 0 || child.StartSeg != 0 {
		t.Errorf("first start: incarnation=%d startSeg=%d, want 0/0", child.Incarnation, child.StartSeg)
	}
	job1 := drainJob(t, store)
	if job1.Incarnation != 0 {
		t.Errorf("first job incarnation = %d, want 0", job1.Incarnation)
	}
	if job1.SessionDir != transcode.SessionDirFor(childID, 0) {
		t.Errorf("first job dir = %q, want bare-id dir", job1.SessionDir)
	}
	if job1.OutputTSOffsetSec != 0 {
		t.Errorf("seg-0 start must not shift timestamps, got %f", job1.OutputTSOffsetSec)
	}

	// Backward-seek restart at segment 300.
	h.ensureRungChild(ctx, parent, rung, childID, 300, true)
	child, err = store.Get(ctx, childID)
	if err != nil {
		t.Fatalf("restarted child missing: %v", err)
	}
	if child.Incarnation != 1 {
		t.Errorf("restart did not bump incarnation: got %d — the old run's cleanup "+
			"and a remote watchdog cannot distinguish the runs", child.Incarnation)
	}
	if child.StartSeg != 300 {
		t.Errorf("restart StartSeg = %d, want 300", child.StartSeg)
	}
	job2 := drainJob(t, store)
	if job2.Incarnation != 1 {
		t.Errorf("restart job incarnation = %d, want 1", job2.Incarnation)
	}
	if !strings.HasSuffix(job2.SessionDir, "-i1") {
		t.Errorf("restart job dir = %q, want an incarnation-scoped dir — sharing the "+
			"old dir is how the superseded run's wipe destroyed the successor's segments", job2.SessionDir)
	}
	// The restarted run's media timestamps must be rebased to its true content
	// time; without this every rung splice lurched the player to 0:00.
	wantOffset := abrSegmentBoundarySec(300, parent.FrameRate)
	if job2.OutputTSOffsetSec != wantOffset {
		t.Errorf("restart OutputTSOffsetSec = %f, want %f", job2.OutputTSOffsetSec, wantOffset)
	}
	if job2.StartOffsetSec != wantOffset {
		t.Errorf("restart StartOffsetSec = %f, want %f", job2.StartOffsetSec, wantOffset)
	}
}

// TestEnsureRungChild_InitSentinelDoesNotRestartMidStreamChild pins the
// init.mp4 fix: an init request must accept ANY running child. Passing a real
// segment index restarted a mid-film child from zero on every hls.js quality
// switch — the init segment it was fetching is identical across incarnations.
func TestEnsureRungChild_InitSentinelDoesNotRestartMidStreamChild(t *testing.T) {
	h, store := newTestHandler(t)
	ctx := context.Background()
	parent, rung := abrTestParent(t, store)
	childID := abrChildID(parent.ID, rung.Label)

	// A child running mid-stream (started at segment 300).
	h.ensureRungChild(ctx, parent, rung, childID, 300, false)
	drainJob(t, store)

	h.ensureRungChild(ctx, parent, rung, childID, abrAnySeg, false)

	child, err := store.Get(ctx, childID)
	if err != nil {
		t.Fatalf("child missing after init request: %v", err)
	}
	if child.Incarnation != 0 || child.StartSeg != 300 {
		t.Errorf("init request restarted the mid-stream child (incarnation=%d startSeg=%d) — "+
			"a quality switch mid-film kills live playback", child.Incarnation, child.StartSeg)
	}
	// And no second job may have been enqueued.
	if job, _ := store.DequeueJob(ctx, "", 250*time.Millisecond); job != nil {
		t.Errorf("init request enqueued a new rung job (session %s) for a healthy child", job.SessionID)
	}
}

// TestEnsureRungChild_ParentGoneAborts pins the resurrection guard: a segment
// request racing Stop/supersede/beacon-stop must not re-create a rung child
// after cleanupRungChildren already ran — that orphan encoder had no parent
// left to stop it.
func TestEnsureRungChild_ParentGoneAborts(t *testing.T) {
	h, store := newTestHandler(t)
	ctx := context.Background()
	parent, rung := abrTestParent(t, store)
	childID := abrChildID(parent.ID, rung.Label)

	// Parent torn down before the (stale) segment request reaches
	// ensureRungChild — the handler still holds the parent struct it loaded
	// before the teardown.
	if err := store.Delete(ctx, parent.ID); err != nil {
		t.Fatalf("delete parent: %v", err)
	}

	h.ensureRungChild(ctx, parent, rung, childID, 0, false)

	if _, err := store.Get(ctx, childID); err == nil {
		t.Error("rung child session survived although its parent is gone — orphan encoder")
	}
	if job, _ := store.DequeueJob(ctx, "", 250*time.Millisecond); job != nil {
		t.Errorf("a job was dispatched for a parentless child: %s", job.SessionID)
	}
}

// TestStartABR_ReportsZeroStartOffset pins the resume-position fix: an ABR
// playlist covers the FULL timeline, so start_offset_sec must be 0 no matter
// where the user resumes. Reporting the resume position shifted the client's
// scrubber mapping by that amount while playback actually began at 0:00, and
// the phantom position was then saved back over the user's real progress.
func TestStartABR_ReportsZeroStartOffset(t *testing.T) {
	h, store := newTestHandler(t)

	durationMS := int64(2 * 60 * 60 * 1000)
	fr := 24.0
	file := &media.File{
		ID:         uuid.New(),
		FilePath:   "/media/test.mkv",
		DurationMS: &durationMS,
		FrameRate:  &fr,
	}
	ladder := []transcode.Rendition{{Label: "720p", Width: 1280, Height: 720, BitrateKbps: 3000}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/items/x/transcode", nil)
	sessionID := transcode.NewSessionID()
	const resumeMS = int64(45 * 60 * 1000) // resume 45 minutes in

	h.startABR(rec, req, sessionID, "tok", "", uuid.New(), uuid.New(),
		file, ladder, 0, 2, false, transcode.LadderH264, resumeMS, 5)

	if rec.Code != 200 {
		t.Fatalf("startABR status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data transcodeStartResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.StartOffsetSec != 0 {
		t.Errorf("ABR start_offset_sec = %f, want 0 — the stream is full-timeline; "+
			"a nonzero offset shifts the scrubber and corrupts saved progress", resp.Data.StartOffsetSec)
	}
	// The requested resume position is preserved on the session for clients
	// that read it from /sessions.
	sess, err := store.Get(context.Background(), resp.Data.SessionID)
	if err != nil {
		t.Fatalf("session not created: %v", err)
	}
	if sess.PositionMS != resumeMS {
		t.Errorf("session PositionMS = %d, want %d", sess.PositionMS, resumeMS)
	}
}
