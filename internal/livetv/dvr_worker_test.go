package livetv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestMain doubles as a fake ffmpeg when re-invoked with DVR_FAKE_FFMPEG=1:
// it drains stdin to EOF — exactly what real ffmpeg does with `-i pipe:0` —
// then "finalizes" by writing its output file (the last argument) and exits 0.
// This mirrors the graceful shutdown contract the DVR capture now relies on:
// EOF on stdin, not a signal, is what ends a recording.
func TestMain(m *testing.M) {
	if os.Getenv("DVR_FAKE_FFMPEG") == "1" {
		fakeFFmpegMain()
		return
	}
	os.Exit(m.Run())
}

func fakeFFmpegMain() {
	// Args mirror the real invocation; the output path is last.
	out := os.Args[len(os.Args)-1]
	_, _ = io.Copy(io.Discard, os.Stdin)
	// Only after clean EOF does the "moov atom" get written — a killed ffmpeg
	// never reaches this line, which is precisely the original bug.
	_ = os.WriteFile(out, []byte("FAKE-MP4-WITH-MOOV"), 0o644)
	os.Exit(0)
}

// dvrFakeStream blocks reads until data arrives or the stream is closed —
// like a live tuner feed. Close unblocks the reader with an error, which
// exec's stdin copier turns into EOF on the child's stdin.
type dvrFakeStream struct {
	pr *io.PipeReader
	pw *io.PipeWriter

	mu       sync.Mutex
	closedAt time.Time
}

func newDVRFakeStream() *dvrFakeStream {
	pr, pw := io.Pipe()
	return &dvrFakeStream{pr: pr, pw: pw}
}

func (s *dvrFakeStream) Read(p []byte) (int, error) { return s.pr.Read(p) }

func (s *dvrFakeStream) Close() error {
	s.mu.Lock()
	if s.closedAt.IsZero() {
		s.closedAt = time.Now()
	}
	s.mu.Unlock()
	_ = s.pw.CloseWithError(io.EOF)
	return nil
}

func (s *dvrFakeStream) closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closedAt.IsZero()
}

type dvrFakeSource struct {
	mu      sync.Mutex
	streams []*dvrFakeStream
	err     error
}

func (f *dvrFakeSource) OpenChannelStream(_ context.Context, _ uuid.UUID) (Stream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	st := newDVRFakeStream()
	f.streams = append(f.streams, st)
	return st, nil
}

// dvrFakeQuerier is an in-memory DVRQuerier covering what the worker touches.
type dvrFakeQuerier struct {
	DVRQuerier // panic on anything the worker shouldn't call

	mu             sync.Mutex
	recs           map[uuid.UUID]*Recording
	getErrsLeft    int // GetRecording failures to inject
	completeErLeft int // SetRecordingCompleted failures to inject
	startedCalls   int
}

func newDVRFakeQuerier() *dvrFakeQuerier {
	return &dvrFakeQuerier{recs: map[uuid.UUID]*Recording{}}
}

func (q *dvrFakeQuerier) add(r Recording) {
	q.mu.Lock()
	defer q.mu.Unlock()
	cp := r
	q.recs[r.ID] = &cp
}

func (q *dvrFakeQuerier) status(id uuid.UUID) RecordingStatus {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.recs[id].Status
}

func (q *dvrFakeQuerier) ListDueRecordings(_ context.Context, upTo time.Time) ([]Recording, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var due []Recording
	for _, r := range q.recs {
		if r.Status == RecordingStatusScheduled && !r.StartsAt.After(upTo) {
			due = append(due, *r)
		}
	}
	return due, nil
}

func (q *dvrFakeQuerier) ListActiveRecordings(context.Context) ([]Recording, error) {
	return nil, nil
}

func (q *dvrFakeQuerier) GetRecording(_ context.Context, id uuid.UUID) (Recording, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.getErrsLeft > 0 {
		q.getErrsLeft--
		return Recording{}, errors.New("injected transient db error")
	}
	r, ok := q.recs[id]
	if !ok {
		return Recording{}, errors.New("not found")
	}
	return *r, nil
}

func (q *dvrFakeQuerier) SetRecordingStartedFile(_ context.Context, id uuid.UUID, filePath string) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.startedCalls++
	r, ok := q.recs[id]
	if !ok || r.Status != RecordingStatusScheduled {
		return false, nil
	}
	r.Status = RecordingStatusRecording
	r.FilePath = &filePath
	return true, nil
}

func (q *dvrFakeQuerier) SetRecordingStatus(_ context.Context, id uuid.UUID, status RecordingStatus) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recs[id].Status = status
	return nil
}

func (q *dvrFakeQuerier) SetRecordingCompleted(_ context.Context, id uuid.UUID, itemID uuid.UUID) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.completeErLeft > 0 {
		q.completeErLeft--
		return errors.New("injected completed-write failure")
	}
	q.recs[id].Status = RecordingStatusCompleted
	q.recs[id].ItemID = &itemID
	return nil
}

func (q *dvrFakeQuerier) SetRecordingFailed(_ context.Context, id uuid.UUID, msg string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recs[id].Status = RecordingStatusFailed
	q.recs[id].Error = &msg
	return nil
}

type dvrFakeMedia struct {
	mu    sync.Mutex
	calls int
}

func (m *dvrFakeMedia) CreateDVRMediaItem(context.Context, DVRMediaItemParams) (uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	return uuid.New(), nil
}

func newTestDVRWorker(t *testing.T, q DVRQuerier, src dvrStreamSource, lib DVRLibraryResolver, media DVRMediaCreator) *DVRWorker {
	t.Helper()
	w := NewDVRWorker(DVRWorkerConfig{
		RecordDir: t.TempDir(),
		FFmpegBin: os.Args[0], // re-invoke the test binary; see TestMain
	}, q, src, lib, media, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Setenv("DVR_FAKE_FFMPEG", "1")
	return w
}

func dvrWaitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

// TestDVR_ScheduledEndFinalizesPlayableFile is the guard for the audit's
// second critical: a recording reaching its scheduled end must END BY EOF —
// upstream closed, ffmpeg finalizes, exit 0, completed — never by a process
// kill, which truncated the MP4 before its moov atom and marked every
// successful recording failed.
func TestDVR_ScheduledEndFinalizesPlayableFile(t *testing.T) {
	q := newDVRFakeQuerier()
	src := &dvrFakeSource{}
	w := newTestDVRWorker(t, q, src, nil, nil)

	recID := uuid.New()
	q.add(Recording{
		ID: recID, ChannelID: uuid.New(), Title: "Evening News",
		Status:   RecordingStatusScheduled,
		StartsAt: time.Now().Add(-time.Second),
		// Ends almost immediately — but the END mechanism, not the wall time,
		// is what's under test. Shrink the grace so the timer fires fast.
		EndsAt: time.Now().Add(-time.Minute),
	})

	// Shrink to test scale: the end timer fires at EndsAt+grace which is
	// already in the past, i.e. immediately after the capture starts.
	w.startDueRecordings(context.Background())

	dvrWaitFor(t, 5*time.Second, func() bool {
		src.mu.Lock()
		defer src.mu.Unlock()
		return len(src.streams) == 1 && src.streams[0].closed()
	}, "end timer never closed the upstream — the recording would end by kill")

	// The fake ffmpeg drains to EOF then writes the file and exits 0; the
	// reaper + finalize should mark the recording completed.
	dvrWaitFor(t, 5*time.Second, func() bool {
		w.tick(context.Background())
		return q.status(recID) == RecordingStatusCompleted
	}, "recording not completed after graceful EOF finalize")

	// The "moov atom" only exists if ffmpeg exited on EOF rather than a kill.
	w.mu.Lock()
	_, stillActive := w.active[recID]
	w.mu.Unlock()
	if stillActive {
		t.Error("finalized capture still in active map")
	}
	files, _ := filepath.Glob(filepath.Join(w.cfg.RecordDir, "*.mp4"))
	if len(files) != 1 {
		t.Fatalf("want 1 recorded file, got %v", files)
	}
	b, err := os.ReadFile(files[0])
	if err != nil || string(b) != "FAKE-MP4-WITH-MOOV" {
		t.Fatalf("recording was not finalized cleanly (content %q, err %v) — "+
			"a killed ffmpeg never writes the moov atom", b, err)
	}
}

// TestDVR_StartFailureRetriesWhileProgramAirs: a transient tune failure at
// starts_at must leave the row scheduled for the next tick, not permanently
// fail the whole recording.
func TestDVR_StartFailureRetriesWhileProgramAirs(t *testing.T) {
	q := newDVRFakeQuerier()
	src := &dvrFakeSource{err: ErrAllTunersBusy}
	w := newTestDVRWorker(t, q, src, nil, nil)

	recID := uuid.New()
	q.add(Recording{
		ID: recID, ChannelID: uuid.New(), Title: "Late Show",
		Status:   RecordingStatusScheduled,
		StartsAt: time.Now().Add(-10 * time.Second),
		EndsAt:   time.Now().Add(30 * time.Minute), // still airing
	})

	w.startDueRecordings(context.Background())
	if got := q.status(recID); got != RecordingStatusScheduled {
		t.Fatalf("tuner-busy at start marked the recording %q — it must stay "+
			"scheduled and retry while the program airs", got)
	}

	// Tuner frees up; the next tick captures.
	src.mu.Lock()
	src.err = nil
	src.mu.Unlock()
	w.startDueRecordings(context.Background())
	if got := q.status(recID); got != RecordingStatusRecording {
		t.Fatalf("after the tuner freed, status = %q, want recording", got)
	}

	// Past ends_at, a failure IS permanent.
	rec2 := uuid.New()
	q.add(Recording{
		ID: rec2, ChannelID: uuid.New(), Title: "Over",
		Status:   RecordingStatusScheduled,
		StartsAt: time.Now().Add(-2 * time.Hour),
		EndsAt:   time.Now().Add(-time.Hour),
	})
	src.mu.Lock()
	src.err = ErrAllTunersBusy
	src.mu.Unlock()
	w.startDueRecordings(context.Background())
	if got := q.status(rec2); got != RecordingStatusFailed {
		t.Errorf("failure after ends_at should be terminal, got %q", got)
	}

	// Cleanup: stop the live capture so the test binary's child exits.
	w.shutdownActive()
}

// TestDVR_CancelDuringPickupStandsDown: SetRecordingStartedFile returning
// false (the row left 'scheduled' — user cancelled in the pickup window) must
// stand the capture down without overwriting the cancellation.
func TestDVR_CancelDuringPickupStandsDown(t *testing.T) {
	q := newDVRFakeQuerier()
	src := &dvrFakeSource{}
	w := newTestDVRWorker(t, q, src, nil, nil)

	recID := uuid.New()
	q.add(Recording{
		ID: recID, ChannelID: uuid.New(), Title: "Cancelled Show",
		Status:   RecordingStatusCancelled, // user cancelled between list and start
		StartsAt: time.Now().Add(-time.Second),
		EndsAt:   time.Now().Add(30 * time.Minute),
	})

	if err := w.beginCapture(context.Background(), Recording{
		ID: recID, ChannelID: uuid.New(), Title: "Cancelled Show",
		StartsAt: time.Now().Add(-time.Second),
		EndsAt:   time.Now().Add(30 * time.Minute),
	}); err != nil {
		t.Fatalf("beginCapture: %v", err)
	}

	if got := q.status(recID); got != RecordingStatusCancelled {
		t.Fatalf("capture overwrote a cancellation: status %q", got)
	}
	w.mu.Lock()
	_, active := w.active[recID]
	w.mu.Unlock()
	if active {
		t.Error("stood-down capture left in the active map")
	}
	dvrWaitFor(t, 3*time.Second, func() bool {
		src.mu.Lock()
		defer src.mu.Unlock()
		return len(src.streams) == 1 && src.streams[0].closed()
	}, "stood-down capture did not release its tuner stream")
}

// TestDVR_FinalizeRetriesTransientErrors: a DB blip during finalize must not
// strand the recording — the session stays active and the next ticks retry,
// and a retried finalize must not create a duplicate media item when only the
// completed-status write failed.
func TestDVR_FinalizeRetriesTransientErrors(t *testing.T) {
	q := newDVRFakeQuerier()
	src := &dvrFakeSource{}
	media := &dvrFakeMedia{}
	libID := uuid.New()
	w := newTestDVRWorker(t, q, src, func(context.Context) (uuid.UUID, error) { return libID, nil }, media)

	recID := uuid.New()
	q.add(Recording{
		ID: recID, ChannelID: uuid.New(), Title: "Documentary",
		Status:   RecordingStatusScheduled,
		StartsAt: time.Now().Add(-time.Second),
		EndsAt:   time.Now().Add(-time.Minute), // ends immediately (graceful EOF)
	})
	// Inject: two GetRecording failures, then one SetRecordingCompleted
	// failure — finalize must survive all three across ticks.
	q.mu.Lock()
	q.getErrsLeft = 2
	q.completeErLeft = 1
	q.mu.Unlock()

	w.startDueRecordings(context.Background())

	dvrWaitFor(t, 10*time.Second, func() bool {
		w.tick(context.Background())
		return q.status(recID) == RecordingStatusCompleted
	}, "finalize never completed across retries — a transient DB blip stranded the recording")

	media.mu.Lock()
	calls := media.calls
	media.mu.Unlock()
	if calls != 1 {
		t.Errorf("CreateDVRMediaItem called %d times across finalize retries — "+
			"a retry after a failed status write must reuse the item it already made", calls)
	}
}

// ── RTMP fan-out ─────────────────────────────────────────────────────────────

func rtmpTag(kind tagKind, payload string) *mediaTag {
	return &mediaTag{kind: kind, data: []byte(payload)}
}

// readSome reads n bytes with a deadline, so a broken (evicted) subscriber
// fails the test rather than hanging it.
func readSome(t *testing.T, r io.Reader, n int) error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, n)
		_, err := io.ReadFull(r, buf)
		errCh <- err
	}()
	select {
	case err := <-errCh:
		return err
	case <-time.After(3 * time.Second):
		return fmt.Errorf("read timed out")
	}
}

// TestRTMP_TwoSubscribersCoexist pins the fan-out fix: live viewing (the HLS
// proxy's ffmpeg) and a DVR capture subscribe to the same broadcast, and
// attaching the second must not evict the first — that eviction is exactly
// how starting a recording used to kill everyone's live stream.
func TestRTMP_TwoSubscribersCoexist(t *testing.T) {
	p := &rtmpPublish{key: "k"}

	feed := func(n int) {
		p.publish(rtmpTag(tagVideoSeqHeader, "SEQHDR"))
		for i := 0; i < n; i++ {
			p.publish(rtmpTag(tagVideoKeyframe, "FRAME"))
		}
	}

	sub1, err := p.subscribe()
	if err != nil {
		t.Fatalf("subscribe 1: %v", err)
	}
	feed(4)
	if err := readSome(t, sub1, 16); err != nil {
		t.Fatalf("first subscriber got no data: %v", err)
	}

	sub2, err := p.subscribe()
	if err != nil {
		t.Fatalf("subscribe 2: %v", err)
	}
	feed(8)

	// BOTH must be receiving. Under the old single-slot semantics sub1's pipe
	// was shut down the moment sub2 attached, and this read errored.
	if err := readSome(t, sub1, 16); err != nil {
		t.Fatalf("first subscriber lost its stream when the second attached "+
			"(DVR capture vs live viewing eviction): %v", err)
	}
	if err := readSome(t, sub2, 16); err != nil {
		t.Fatalf("second subscriber got no data: %v", err)
	}

	// Detaching one leaves the other alive.
	_ = sub1.Close()
	feed(8)
	if err := readSome(t, sub2, 16); err != nil {
		t.Fatalf("surviving subscriber broken after the other detached: %v", err)
	}
	_ = sub2.Close()
	p.close()
}

// ── HLS double-tune join ─────────────────────────────────────────────────────

// TestHLSProxy_TuneFailureJoinsConcurrentWinner: when two first-viewers race
// and the loser's tune fails (single tuner), the loser must join the winner's
// session instead of surfacing ALL_TUNERS_BUSY for a channel that is
// actively streaming.
func TestHLSProxy_TuneFailureJoinsConcurrentWinner(t *testing.T) {
	p := &HLSProxy{
		cfg:      HLSConfig{Dir: t.TempDir(), FFmpegBin: "unused"},
		svc:      &stubProxyService{err: ErrAllTunersBusy},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: make(map[uuid.UUID]*HLSSession),
	}
	channel := uuid.New()

	// The "winner" registers its session while the loser's Acquire is inside
	// its post-failure join window.
	go func() {
		time.Sleep(300 * time.Millisecond)
		p.newTestSession(channel, t.TempDir(), &stubChannelStream{Reader: io.LimitReader(nil, 0)}, nil, func() {})
	}()

	s, err := p.Acquire(context.Background(), channel)
	if err != nil {
		t.Fatalf("loser surfaced %v although the winner's session was live — "+
			"the second concurrent viewer of a channel gets a spurious tuner-busy error", err)
	}
	s.mu.Lock()
	rc := s.refcount
	s.mu.Unlock()
	if rc != 2 {
		t.Errorf("joined session refcount = %d, want 2 (winner + joiner)", rc)
	}

	// A genuinely busy tuner (no session ever appears) still errors.
	channel2 := uuid.New()
	if _, err := p.Acquire(context.Background(), channel2); !errors.Is(err, ErrAllTunersBusy) {
		t.Errorf("want ErrAllTunersBusy for a channel nobody is streaming, got %v", err)
	}
}
