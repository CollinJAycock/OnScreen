package livetv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ── test doubles run through TestMain ────────────────────────────────────────

// fakeScriptMain is the post-processing script: it records its argv[1] in
// the file named by DVR_FAKE_SCRIPT. DVR_FAKE_SCRIPT_EXIT makes it fail.
func fakeScriptMain() {
	arg := ""
	if len(os.Args) > 1 {
		arg = os.Args[1]
	}
	_ = os.WriteFile(os.Getenv("DVR_FAKE_SCRIPT"), []byte(arg), 0o644)
	fmt.Println("fake script saw", arg)
	if os.Getenv("DVR_FAKE_SCRIPT_EXIT") != "" {
		os.Exit(3)
	}
	os.Exit(0)
}

// fakeComskipMain writes a comskip EDL for its input into --output, naming
// one break from 8.5 s to 13.5 s, then exits 0 (commercials found).
func fakeComskipMain() {
	var outDir, input string
	for _, a := range os.Args[1:] {
		switch {
		case strings.HasPrefix(a, "--output="):
			outDir = strings.TrimPrefix(a, "--output=")
		case strings.HasPrefix(a, "--"):
		default:
			input = a
		}
	}
	base := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	_ = os.WriteFile(filepath.Join(outDir, base+".edl"), []byte("8.50\t13.50\t0\n20.00\t20.50\t2\n"), 0o644)
	os.Exit(0)
}

// ── fake store ───────────────────────────────────────────────────────────────

type ppFakeStore struct {
	mu      sync.Mutex
	recs    map[uuid.UUID]Recording
	jobs    map[uuid.UUID]*PostProcessJob
	order   []uuid.UUID
	msgs    map[uuid.UUID]string
	files   map[string]uuid.UUID // media_files path -> id
	swaps   []SwapFileParams
	markers map[uuid.UUID][]CommercialBreak
	sources map[uuid.UUID]string
	swapErr error
}

func newPPFakeStore() *ppFakeStore {
	return &ppFakeStore{
		recs: map[uuid.UUID]Recording{}, jobs: map[uuid.UUID]*PostProcessJob{},
		msgs: map[uuid.UUID]string{}, files: map[string]uuid.UUID{},
		markers: map[uuid.UUID][]CommercialBreak{}, sources: map[uuid.UUID]string{},
	}
}

func (s *ppFakeStore) GetRecording(_ context.Context, id uuid.UUID) (Recording, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.recs[id]
	if !ok {
		return Recording{}, errors.New("not found")
	}
	return r, nil
}

func (s *ppFakeStore) GetPostProcessJob(_ context.Context, id uuid.UUID) (PostProcessJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return PostProcessJob{}, false, nil
	}
	return *j, true, nil
}

func (s *ppFakeStore) EnqueuePostProcess(_ context.Context, id uuid.UUID, kind PostProcessJobKind) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok && (j.Status == PostProcessQueued || j.Status == PostProcessProcessing) {
		return false, nil
	}
	s.jobs[id] = &PostProcessJob{RecordingID: id, Kind: kind, Status: PostProcessQueued}
	s.order = append(s.order, id)
	return true, nil
}

func (s *ppFakeStore) ClaimNextPostProcess(context.Context) (PostProcessJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.order {
		if j := s.jobs[id]; j.Status == PostProcessQueued {
			j.Status = PostProcessProcessing
			return *j, true, nil
		}
	}
	return PostProcessJob{}, false, nil
}

func (s *ppFakeStore) SetPostProcessMessage(_ context.Context, id uuid.UUID, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs[id] = msg
	return nil
}

func (s *ppFakeStore) FinishPostProcess(_ context.Context, id uuid.UUID, st PostProcessStatus, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[id].Status = st
	s.msgs[id] = msg
	return nil
}

func (s *ppFakeStore) FailInterruptedPostProcess(context.Context) (int64, error) { return 0, nil }

func (s *ppFakeStore) FindMediaFileID(_ context.Context, path string) (uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.files[path], nil
}

func (s *ppFakeStore) SwapRecordingFile(_ context.Context, p SwapFileParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.swapErr != nil {
		return s.swapErr
	}
	s.swaps = append(s.swaps, p)
	r := s.recs[p.RecordingID]
	np := p.NewPath
	r.FilePath = &np
	s.recs[p.RecordingID] = r
	for path, id := range s.files {
		if id == p.FileID {
			delete(s.files, path)
			s.files[p.NewPath] = id
		}
	}
	return nil
}

func (s *ppFakeStore) ReplaceCommercialMarkers(_ context.Context, fileID uuid.UUID, source string, b []CommercialBreak) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markers[fileID] = append([]CommercialBreak(nil), b...)
	s.sources[fileID] = source
	return nil
}

func (s *ppFakeStore) addRecording(path string) (recID, fileID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recID, fileID = uuid.New(), uuid.New()
	p := path
	s.recs[recID] = Recording{ID: recID, Status: RecordingStatusCompleted, FilePath: &p, Title: "Test Show"}
	s.files[path] = fileID
	return recID, fileID
}

func (s *ppFakeStore) result(id uuid.UUID) (PostProcessStatus, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id].Status, s.msgs[id]
}

// ── test media ───────────────────────────────────────────────────────────────

// testHeuristic scales the ad-break rules down to the 21.5 s test recording:
// programme segments of 8 s, adverts of 2 s.
var testHeuristic = HeuristicParams{
	MinOverlap:          100 * time.Millisecond,
	MergeGap:            200 * time.Millisecond,
	MaxAdLen:            3 * time.Second,
	MinPodLen:           3 * time.Second,
	MaxPodLen:           10 * time.Second,
	MinAdsPerPod:        2,
	MinProgramLen:       6 * time.Second,
	BlackPixelThreshold: 0.10,
	BlackPictureRatio:   0.98,
	SilenceNoiseDB:      -50,
}

func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
}

// makeTestRecording writes a capture-shaped MP4 (H.264 + AAC) laid out like a
// broadcast: 8 s programme (colour bars + tone), then an ad break of two 2 s
// adverts, each transition 0.5 s of black + silence, then 8 s more programme.
//
//	0.0– 8.0 programme   8.0– 8.5 black   8.5–10.5 ad 1
//	10.5–11.0 black     11.0–13.0 ad 2   13.0–13.5 black
//	13.5–21.5 programme
//
// Short GOP so a stream-copy cut lands close to its points.
func makeTestRecording(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "Test Show - 2026-10-14 2000 [abcd1234].mp4")
	af := "aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=mono"
	seg := func(i int, video, audio string) string {
		return fmt.Sprintf("%s[v%d];%s,%s[a%d];", video, i, audio, af, i)
	}
	black := func(i int) string {
		return seg(i, "color=c=black:s=160x120:r=25:d=0.5", "anullsrc=r=48000:cl=mono,atrim=duration=0.5")
	}
	graph := seg(0, "smptebars=s=160x120:r=25:d=8", "sine=f=440:d=8:sample_rate=48000") +
		black(1) +
		seg(2, "testsrc=s=160x120:r=25:d=2", "sine=f=880:d=2:sample_rate=48000") +
		black(3) +
		seg(4, "rgbtestsrc=s=160x120:r=25:d=2", "sine=f=660:d=2:sample_rate=48000") +
		black(5) +
		seg(6, "smptebars=s=160x120:r=25:d=8", "sine=f=440:d=8:sample_rate=48000")
	for i := 0; i < 7; i++ {
		graph += fmt.Sprintf("[v%d][a%d]", i, i)
	}
	graph += "concat=n=7:v=1:a=1[v][a]"
	cmd := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-filter_complex", graph, "-map", "[v]", "-map", "[a]",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "5", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "64k", "-movflags", "+faststart", "-f", "mp4", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make test recording: %v\n%s", err, out)
	}
	return path
}

func newTestPostProcessor(store PostProcessStore, set PostProcessSettings) *PostProcessor {
	hp := testHeuristic
	return NewPostProcessor(PostProcessConfig{
		Settings:  func(context.Context) PostProcessSettings { return set },
		Heuristic: &hp,
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func near(got, want, tol int64) bool { return got >= want-tol && got <= want+tol }

// ── pipeline tests (real ffmpeg) ─────────────────────────────────────────────

func TestPostProcess_RemuxKeepsOriginalAndMarksCommercials(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestRecording(t, dir)
	store := newPPFakeStore()
	recID, fileID := store.addRecording(src)
	pp := newTestPostProcessor(store, PostProcessSettings{
		Remux: true, Container: "mkv", Commercials: CommercialsMark,
	})

	pp.QueueAfterCapture(context.Background(), recID)
	if !pp.runOne(context.Background()) {
		t.Fatal("no job was queued")
	}
	st, msg := store.result(recID)
	if st != PostProcessProcessed {
		t.Fatalf("status = %s (%s), want processed", st, msg)
	}
	if !strings.Contains(msg, "remuxed to MKV") || !strings.Contains(msg, "1 commercial break marked") {
		t.Errorf("message = %q", msg)
	}

	final := strings.TrimSuffix(src, ".mp4") + ".mkv"
	if len(store.swaps) != 1 || store.swaps[0].NewPath != final || store.swaps[0].FileID != fileID {
		t.Fatalf("swaps = %+v, want one swap of file %s to %s", store.swaps, fileID, final)
	}
	if store.swaps[0].Container != "matroska" || store.swaps[0].VideoCodec != "h264" {
		t.Errorf("swap metadata = %+v", store.swaps[0])
	}
	kept := filepath.Join(dir, OriginalsDirName, filepath.Base(src))
	if store.swaps[0].OriginalPath == nil || *store.swaps[0].OriginalPath != kept {
		t.Errorf("original path = %v, want %s", store.swaps[0].OriginalPath, kept)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("kept capture missing: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("capture still in the library folder (rescan would duplicate it): %v", err)
	}
	info, err := pp.probeMedia(context.Background(), final)
	if err != nil || math.Abs(info.DurationSec-21.5) > 0.4 {
		t.Errorf("remuxed duration = %.2f (%v), want ~21.5", info.DurationSec, err)
	}
	assertNoTempFiles(t, dir)

	b := store.markers[fileID]
	if len(b) != 1 || !near(b[0].StartMS, 8500, 300) || !near(b[0].EndMS, 13000, 300) {
		t.Fatalf("markers = %+v, want one break ~8.5s–13.0s", b)
	}
	if store.sources[fileID] != CommercialSourceHeuristic {
		t.Errorf("source = %q", store.sources[fileID])
	}
}

func TestPostProcess_CutRemovesBreakAndDeletesOriginal(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestRecording(t, dir)
	store := newPPFakeStore()
	recID, fileID := store.addRecording(src)
	pp := newTestPostProcessor(store, PostProcessSettings{
		Remux: true, Container: "mp4", Commercials: CommercialsCut, DeleteOriginal: true,
	})
	store.markers[fileID] = []CommercialBreak{{StartMS: 1, EndMS: 2}} // stale; must be cleared

	pp.QueueAfterCapture(context.Background(), recID)
	pp.runOne(context.Background())
	st, msg := store.result(recID)
	if st != PostProcessProcessed || !strings.Contains(msg, "1 commercial break removed") {
		t.Fatalf("status = %s, message = %q", st, msg)
	}
	// MP4 -> MP4 with delete-original: the processed file replaces the capture
	// in place.
	if len(store.swaps) != 1 || store.swaps[0].NewPath != src || store.swaps[0].OriginalPath != nil {
		t.Fatalf("swaps = %+v", store.swaps)
	}
	info, err := pp.probeMedia(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	// 21.5 s minus the ~4.5 s break.
	if math.Abs(info.DurationSec-17.0) > 0.5 {
		t.Errorf("cut duration = %.2f, want ~17.0", info.DurationSec)
	}
	if _, err := os.Stat(filepath.Join(dir, OriginalsDirName)); !os.IsNotExist(err) {
		t.Errorf("delete-original kept a copy anyway")
	}
	if len(store.markers[fileID]) != 0 {
		t.Errorf("markers after cut = %+v, want none (the breaks are gone)", store.markers[fileID])
	}
	assertNoTempFiles(t, dir)
}

func TestPostProcess_TranscodeSoftware(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestRecording(t, dir)
	store := newPPFakeStore()
	recID, _ := store.addRecording(src)
	pp := newTestPostProcessor(store, PostProcessSettings{
		Transcode: true, Codec: "h264", Quality: "small", Container: "mkv", DeleteOriginal: true,
	})
	pp.QueueAfterCapture(context.Background(), recID)
	pp.runOne(context.Background())
	st, msg := store.result(recID)
	if st != PostProcessProcessed || !strings.Contains(msg, "transcoded to H.264 MKV with libx264") {
		t.Fatalf("status = %s, message = %q", st, msg)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("original not deleted after a verified transcode")
	}
	assertNoTempFiles(t, dir)
}

// A failed rewrite must leave the capture exactly where it was, untouched,
// with the recording still pointing at it.
func TestPostProcess_FailureLeavesOriginalIntact(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestRecording(t, dir)
	before, _ := os.ReadFile(src)
	store := newPPFakeStore()
	recID, _ := store.addRecording(src)
	hp := testHeuristic
	pp := NewPostProcessor(PostProcessConfig{
		// The capture-test fake ffmpeg writes a junk file and exits 0: the
		// output must fail verification.
		FFmpegBin: os.Args[0],
		Settings: func(context.Context) PostProcessSettings {
			return PostProcessSettings{Remux: true, Container: "mkv", DeleteOriginal: true}
		},
		Heuristic: &hp,
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Setenv("DVR_FAKE_FFMPEG", "1")

	pp.QueueAfterCapture(context.Background(), recID)
	pp.runOne(context.Background())
	st, msg := store.result(recID)
	if st != PostProcessFailed || !strings.Contains(msg, "verification") {
		t.Fatalf("status = %s, message = %q; want failed verification", st, msg)
	}
	after, err := os.ReadFile(src)
	if err != nil || string(after) != string(before) {
		t.Fatalf("capture changed or missing after a failed job: %v", err)
	}
	if len(store.swaps) != 0 {
		t.Errorf("database was re-pointed despite the failure: %+v", store.swaps)
	}
	assertNoTempFiles(t, dir)
}

// A database failure during the swap rolls the filesystem back: the capture
// is back in place and the processed file is gone.
func TestPostProcess_SwapFailureRollsBack(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestRecording(t, dir)
	store := newPPFakeStore()
	store.swapErr = errors.New("db down")
	recID, _ := store.addRecording(src)
	pp := newTestPostProcessor(store, PostProcessSettings{Remux: true, Container: "mkv"})
	pp.QueueAfterCapture(context.Background(), recID)
	pp.runOne(context.Background())
	if st, _ := store.result(recID); st != PostProcessFailed {
		t.Fatalf("status = %s, want failed", st)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("capture not restored: %v", err)
	}
	if _, err := os.Stat(strings.TrimSuffix(src, ".mp4") + ".mkv"); !os.IsNotExist(err) {
		t.Errorf("processed file left behind")
	}
	if _, err := os.Stat(filepath.Join(dir, OriginalsDirName, filepath.Base(src))); !os.IsNotExist(err) {
		t.Errorf("capture still in the originals folder")
	}
	assertNoTempFiles(t, dir)
}

func TestPostProcess_ScriptGetsRecordingPath(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestRecording(t, dir)
	store := newPPFakeStore()
	recID, _ := store.addRecording(src)
	marker := filepath.Join(t.TempDir(), "script-arg")
	t.Setenv("DVR_FAKE_SCRIPT", marker)
	pp := NewPostProcessor(PostProcessConfig{ScriptPath: os.Args[0]}, store,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Settings all off: the script alone is reason enough to queue.
	pp.QueueAfterCapture(context.Background(), recID)
	pp.runOne(context.Background())
	if st, msg := store.result(recID); st != PostProcessProcessed || msg != "script ran" {
		t.Fatalf("status = %s, message = %q", st, msg)
	}
	got, _ := os.ReadFile(marker)
	if string(got) != src {
		t.Fatalf("script argv[1] = %q, want %q", got, src)
	}

	// A failing script fails the job; the file is untouched.
	t.Setenv("DVR_FAKE_SCRIPT_EXIT", "1")
	rec2, _ := store.addRecording(src)
	pp.QueueAfterCapture(context.Background(), rec2)
	pp.runOne(context.Background())
	if st, msg := store.result(rec2); st != PostProcessFailed || !strings.Contains(msg, "script failed") {
		t.Fatalf("status = %s, message = %q; want script failure", st, msg)
	}
}

func TestPostProcess_NothingQueuedWhenAllOff(t *testing.T) {
	store := newPPFakeStore()
	recID, _ := store.addRecording("/nowhere.mp4")
	pp := newTestPostProcessor(store, PostProcessSettings{Commercials: CommercialsOff})
	pp.QueueAfterCapture(context.Background(), recID)
	if _, ok, _ := store.GetPostProcessJob(context.Background(), recID); ok {
		t.Fatal("a job was queued with every post-processing option off")
	}
}

func TestPostProcess_DetectWithComskip(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestRecording(t, dir)
	store := newPPFakeStore()
	recID, fileID := store.addRecording(src)
	t.Setenv("DVR_FAKE_COMSKIP", "1")
	pp := NewPostProcessor(PostProcessConfig{ComskipPath: os.Args[0], ComskipINI: "/etc/comskip.ini"}, store,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := pp.Enqueue(context.Background(), recID, PostProcessDetect); err != nil {
		t.Fatal(err)
	}
	if err := pp.Enqueue(context.Background(), recID, PostProcessDetect); !errors.Is(err, ErrPostProcessBusy) {
		t.Fatalf("second enqueue err = %v, want ErrPostProcessBusy", err)
	}
	pp.runOne(context.Background())
	if st, msg := store.result(recID); st != PostProcessProcessed {
		t.Fatalf("status = %s (%s)", st, msg)
	}
	// The scene marker (action 2) in the EDL is not an advert.
	want := []CommercialBreak{{StartMS: 8500, EndMS: 13500}}
	if got := store.markers[fileID]; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("markers = %+v, want %+v", got, want)
	}
	if store.sources[fileID] != CommercialSourceComskip {
		t.Errorf("source = %q", store.sources[fileID])
	}
	if len(store.swaps) != 0 {
		t.Error("detect-only job rewrote the file")
	}
}

func TestPostProcess_EnqueueRules(t *testing.T) {
	store := newPPFakeStore()
	recID, _ := store.addRecording("/x.mp4")
	pp := newTestPostProcessor(store, PostProcessSettings{})
	store.jobs[recID] = &PostProcessJob{RecordingID: recID, Kind: PostProcessFull, Status: PostProcessProcessed}
	if err := pp.Enqueue(context.Background(), recID, PostProcessFull); !errors.Is(err, ErrPostProcessIneligible) {
		t.Errorf("re-running a processed recording: err = %v, want ErrPostProcessIneligible", err)
	}
	if err := pp.Enqueue(context.Background(), recID, PostProcessDetect); err != nil {
		t.Errorf("detect on a processed recording: %v", err)
	}
	r := store.recs[recID]
	r.Status = RecordingStatusRecording
	store.recs[recID] = r
	if err := pp.Enqueue(context.Background(), recID, PostProcessDetect); !errors.Is(err, ErrPostProcessIneligible) {
		t.Errorf("in-progress recording: err = %v, want ErrPostProcessIneligible", err)
	}
}

func TestHeuristic_OnGeneratedMedia(t *testing.T) {
	requireFFmpeg(t)
	src := makeTestRecording(t, t.TempDir())
	pp := newTestPostProcessor(newPPFakeStore(), PostProcessSettings{})
	info, err := pp.probeMedia(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	breaks, source, err := pp.detectCommercials(context.Background(), src, info)
	if err != nil || source != CommercialSourceHeuristic {
		t.Fatalf("detect: %v (source %q)", err, source)
	}
	if len(breaks) != 1 || !near(breaks[0].StartMS, 8500, 300) || !near(breaks[0].EndMS, 13000, 300) {
		t.Fatalf("breaks = %+v, want one ~8.5s–13.0s", breaks)
	}

	// With production thresholds the same file has no ad break: 8 s of
	// "programme" is far too short to bracket one.
	pp.cfg.Heuristic = nil
	breaks, _, err = pp.detectCommercials(context.Background(), src, info)
	if err != nil || len(breaks) != 0 {
		t.Fatalf("default params: breaks = %+v, err = %v; want none", breaks, err)
	}
}

type captureHookFake struct {
	mu  sync.Mutex
	ids []uuid.UUID
}

func (h *captureHookFake) QueueAfterCapture(_ context.Context, id uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ids = append(h.ids, id)
}

// A capture that completes is handed to post-processing exactly once.
func TestDVR_CompletedCaptureQueuesPostProcess(t *testing.T) {
	q := newDVRFakeQuerier()
	src := &dvrFakeSource{}
	hook := &captureHookFake{}
	w := newTestDVRWorker(t, q, src, nil, nil).WithPostProcessor(hook)
	recID := uuid.New()
	q.add(Recording{
		ID: recID, ChannelID: uuid.New(), Title: "Evening News",
		Status:   RecordingStatusScheduled,
		StartsAt: time.Now().Add(-time.Second),
		EndsAt:   time.Now().Add(-time.Minute),
	})
	w.startDueRecordings(context.Background())
	dvrWaitFor(t, 5*time.Second, func() bool {
		w.tick(context.Background())
		return q.status(recID) == RecordingStatusCompleted
	}, "recording not completed")
	w.tick(context.Background())
	hook.mu.Lock()
	defer hook.mu.Unlock()
	if len(hook.ids) != 1 || hook.ids[0] != recID {
		t.Fatalf("post-process hook calls = %v, want exactly [%s]", hook.ids, recID)
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(path, partSuffix) {
			t.Errorf("temp file left behind: %s", path)
		}
		return nil
	})
}

// ── pure logic ───────────────────────────────────────────────────────────────

func TestClassifyBreaks(t *testing.T) {
	hp := DefaultHeuristicParams()
	tr := func(at float64) (timeSpan, timeSpan) { // a 0.5 s black+silence transition
		return timeSpan{at, at + 0.5}, timeSpan{at - 0.1, at + 0.6}
	}
	build := func(points ...float64) (black, silence []timeSpan) {
		for _, p := range points {
			b, s := tr(p)
			black = append(black, b)
			silence = append(silence, s)
		}
		return
	}

	t.Run("pod between programme", func(t *testing.T) {
		// Programme to 600, three 30 s ads, programme from 690.5 to 1300.
		b, s := build(600, 630, 660, 690)
		got := classifyBreaks(b, s, 1300, hp)
		if len(got) != 1 || got[0].StartMS != 600500 || got[0].EndMS != 690000 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("lone short segment is not a pod", func(t *testing.T) {
		// A 60 s title sequence between two fades.
		b, s := build(600, 660)
		if got := classifyBreaks(b, s, 1300, hp); len(got) != 0 {
			t.Fatalf("got %+v, want none", got)
		}
	})
	t.Run("pod at the start of the recording", func(t *testing.T) {
		b, s := build(30, 60, 90)
		if got := classifyBreaks(b, s, 1300, hp); len(got) != 0 {
			t.Fatalf("got %+v, want none", got)
		}
	})
	t.Run("pod longer than six minutes", func(t *testing.T) {
		var pts []float64
		for at := 600.0; at <= 600+7*60; at += 60 {
			pts = append(pts, at)
		}
		b, s := build(pts...)
		if got := classifyBreaks(b, s, 2400, hp); len(got) != 0 {
			t.Fatalf("got %+v, want none", got)
		}
	})
	t.Run("black without silence", func(t *testing.T) {
		b, _ := build(600, 630, 660, 690)
		if got := classifyBreaks(b, nil, 1300, hp); len(got) != 0 {
			t.Fatalf("got %+v, want none", got)
		}
	})
	t.Run("short programme either side", func(t *testing.T) {
		// Only 2.5 minutes of programme after the pod: could be a recap, not ads.
		b, s := build(600, 630, 660, 690, 840)
		if got := classifyBreaks(b, s, 1300, hp); len(got) != 0 {
			t.Fatalf("got %+v, want none", got)
		}
	})
}

func TestParseDetectOutput(t *testing.T) {
	stderr := strings.Join([]string{
		"[blackdetect @ 0x55] black_start:8 black_end:8.52 black_duration:0.52",
		"[silencedetect @ 0x56] silence_start: 7.98",
		"[silencedetect @ 0x56] silence_end: 8.51 | silence_duration: 0.53",
		"frame=  100 fps=0.0 q=-0.0 size=N/A time=00:00:04.00",
		"[blackdetect @ 0x55] black_start:bogus black_end:1",
		"[silencedetect @ 0x56] silence_start: 20.2",
	}, "\n")
	black, silence := parseDetectOutput(strings.NewReader(stderr), 21.5)
	if len(black) != 1 || black[0] != (timeSpan{8, 8.52}) {
		t.Errorf("black = %+v", black)
	}
	if len(silence) != 2 || silence[0] != (timeSpan{7.98, 8.51}) || silence[1] != (timeSpan{20.2, 21.5}) {
		t.Errorf("silence = %+v (an open silence at EOF runs to the end)", silence)
	}
}

func TestParseComskipOutputs(t *testing.T) {
	edl := parseEDL("0.00\t2.00\t0\n600.5\t690.25\t3\n700 701 1\njunk\n")
	if len(edl) != 2 || edl[1] != (CommercialBreak{600500, 690250}) {
		t.Errorf("edl = %+v", edl)
	}
	txt, err := parseComskipTxt("FILE PROCESSING COMPLETE  53999 FRAMES AT  2997\n-------------------\n17982\t20679\n")
	if err != nil || len(txt) != 1 || !near(txt[0].StartMS, 600000, 20) || !near(txt[0].EndMS, 690000, 20) {
		t.Errorf("txt = %+v, err = %v", txt, err)
	}
	if _, err := parseComskipTxt("100 200\n"); err == nil {
		t.Error("txt without a frame-rate header should be an error")
	}
	got := clampBreaks([]CommercialBreak{{-5, 500}, {9000, 20000}, {1000, 3000}, {2500, 4000}, {5000, 5200}}, 15)
	want := []CommercialBreak{{1000, 4000}, {9000, 15000}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("clampBreaks = %+v, want %+v", got, want)
	}
}

func TestKeepSegments(t *testing.T) {
	got := keepSegments([]CommercialBreak{{600000, 690000}, {1200000, 1300000}}, 1800)
	want := []timeSpan{{0, 600}, {690, 1200}, {1300, 1800}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("keep = %+v, want %+v", got, want)
	}
	if got := keepSegments(nil, 60); len(got) != 1 || got[0] != (timeSpan{0, 60}) {
		t.Errorf("no breaks: keep = %+v", got)
	}
}

func TestEncoderCandidates(t *testing.T) {
	active := []string{"h264_nvenc", "hevc_nvenc", "h264_vaapi", "hevc_qsv", "av1_nvenc", "libx264"}
	if got := encoderCandidates("hevc", active); fmt.Sprint(got) != "[hevc_nvenc hevc_qsv libx265]" {
		t.Errorf("hevc = %v", got)
	}
	if got := encoderCandidates("h264", active); fmt.Sprint(got) != "[h264_nvenc libx264]" {
		t.Errorf("h264 = %v (VAAPI must be skipped)", got)
	}
	if got := encoderCandidates("h264", nil); fmt.Sprint(got) != "[libx264]" {
		t.Errorf("no hardware = %v", got)
	}
}

func TestVerifyOutput(t *testing.T) {
	src := mediaInfo{DurationSec: 1800, HasVideo: true, HasAudio: true}
	if err := verifyOutput(src, mediaInfo{DurationSec: 1790, HasVideo: true, HasAudio: true}, 1800); err != nil {
		t.Errorf("within 2%%: %v", err)
	}
	if err := verifyOutput(src, mediaInfo{DurationSec: 1700, HasVideo: true, HasAudio: true}, 1800); err == nil {
		t.Error("5% short accepted")
	}
	if err := verifyOutput(src, mediaInfo{DurationSec: 1800, HasVideo: true}, 1800); err == nil {
		t.Error("lost audio accepted")
	}
	if err := verifyOutput(src, mediaInfo{}, 1800); err == nil {
		t.Error("empty output accepted")
	}
}

func TestTranscodeArgsGuardInput(t *testing.T) {
	args := transcodeArgs("/dvr/a.mp4", "/dvr/.a.onscreen-process.mp4.part", "mp4", "libx265", "hevc", "high")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-f mov -protocol_whitelist file,pipe,crypto,data,subfile -i /dvr/a.mp4") {
		t.Errorf("input not guarded: %s", joined)
	}
	if !strings.Contains(joined, "-crf 22") || !strings.Contains(joined, "-tag:v hvc1") || !strings.HasSuffix(joined, "-f mp4 /dvr/.a.onscreen-process.mp4.part") {
		t.Errorf("args = %s", joined)
	}
}
