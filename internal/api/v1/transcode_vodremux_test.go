package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

const vodTestDurMS = 40_000

// vodTestMKV synthesises a 40 s MKV with keyframes at known, irregular
// times (the plan reads its Cues) and returns its path and keyframes.
func vodTestMKV(t *testing.T) (string, []float64) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	src := filepath.Join(t.TempDir(), "movie.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24",
		"-t", "40", "-c:v", "libx264", "-preset", "ultrafast", "-g", "1000",
		"-force_key_frames", "0,1.5,4,4.5,9,13.2,14,20,21,27.5,30,31,38",
		"-y", src).CombinedOutput(); err != nil {
		t.Fatalf("synthesise MKV: %v: %s", err, out)
	}
	kf, err := transcode.MKVVideoKeyframes(src)
	if err != nil {
		t.Fatal(err)
	}
	return src, kf
}

func vodTestParent(t *testing.T, store *transcode.SessionStore, path string) *transcode.Session {
	t.Helper()
	parent := transcode.Session{
		ID:            "vr-parent-" + uuid.NewString(),
		UserID:        uuid.New(),
		MediaItemID:   uuid.New(),
		FileID:        uuid.New(),
		Decision:      "remux",
		FilePath:      path,
		CreatedAt:     time.Now(),
		SegToken:      "tok",
		VODRemux:      true,
		RemuxTSOffset: 0.175,
		DurationMS:    vodTestDurMS,
		RemuxJob: &transcode.TranscodeJob{
			FilePath: path, Decision: "remux", Encoder: "copy",
			AudioCodec: "aac", AudioChannels: 2, IsH264: true,
		},
	}
	if err := store.Create(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	return &parent
}

func vodTestPlan(t *testing.T, path string) transcode.RemuxPlan {
	t.Helper()
	plan, _, ok := transcode.LoadRemuxPlan(context.Background(), path, vodTestDurMS)
	if !ok {
		t.Fatal("plan refused")
	}
	return plan
}

func noMoreJobs(t *testing.T, store *transcode.SessionStore) {
	t.Helper()
	if job, _ := store.DequeueJob(context.Background(), "", 200*time.Millisecond); job != nil {
		t.Fatalf("unexpected job dispatched: %+v", job)
	}
}

func TestEnsureVODRemuxChild_DispatchesAPieceRun(t *testing.T) {
	path, _ := vodTestMKV(t)
	h, store := newTestHandler(t)
	ctx := context.Background()
	parent := vodTestParent(t, store, path)
	plan := vodTestPlan(t, path)
	childID := vodRemuxChildID(parent.ID)

	h.ensureVODRemuxChild(ctx, parent, plan, 3, false)
	job := drainJob(t, store)
	if !job.CopyTS || !job.KeyframePieces {
		t.Errorf("job CopyTS=%v KeyframePieces=%v, want both", job.CopyTS, job.KeyframePieces)
	}
	if job.StartOffsetSec != plan.SeekTarget(3) || job.OutputTSOffsetSec != 0.175 {
		t.Errorf("job seek %v offset %v, want %v and 0.175", job.StartOffsetSec, job.OutputTSOffsetSec, plan.SeekTarget(3))
	}
	if job.AudioCodec != "aac" || job.Encoder != "copy" || job.SessionDir != transcode.SessionDirFor(childID, 0) {
		t.Errorf("job didn't come from the template: %+v", job)
	}
	child, err := store.Get(ctx, childID)
	if err != nil || child.StartSeg != 3 || child.ParentID != parent.ID {
		t.Fatalf("child = %+v, err %v", child, err)
	}

	// A later segment is reachable by the running child: nothing new.
	h.ensureVODRemuxChild(ctx, parent, plan, 5, false)
	noMoreJobs(t, store)

	// An earlier one restarts it under a new incarnation.
	h.ensureVODRemuxChild(ctx, parent, plan, 1, false)
	job = drainJob(t, store)
	if job.Incarnation != 1 || job.StartOffsetSec != plan.SeekTarget(1) {
		t.Errorf("restart job incarnation %d seek %v, want 1 and %v", job.Incarnation, job.StartOffsetSec, plan.SeekTarget(1))
	}
}

// writeRun lays out a child run on local disk: piece files with
// recognisable bodies and the run's own playlist.
func writeRun(t *testing.T, child *transcode.Session, durs []float64, ended bool) {
	t.Helper()
	dir := child.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	var pl strings.Builder
	pl.WriteString("#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n")
	for j, d := range durs {
		name := fmt.Sprintf("seg%05d.ts", j)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprintf("piece-%d;", j)), 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&pl, "#EXTINF:%.6f,\n%s\n", d, name)
	}
	if ended {
		pl.WriteString("#EXT-X-ENDLIST\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte(pl.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func vodServer(t *testing.T, h *NativeTranscodeHandler) *httptest.Server {
	r := chi.NewRouter()
	r.Get("/sessions/{sid}/playlist.m3u8", h.Playlist)
	r.Get("/sessions/{sid}/vr/seg/{name}", h.VODRemuxSegment)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func getBody(t *testing.T, u string) (int, string, string) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("Content-Length")
}

func TestVODRemuxSegment_ServesThePiecesOfASegment(t *testing.T) {
	path, kf := vodTestMKV(t)
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	segTok := transcode.NewSegmentTokenManager(v)
	h := NewNativeTranscodeHandler(store, segTok, &mockTranscodeMedia{}, &config.Config{}, slog.Default())
	ctx := context.Background()
	parent := vodTestParent(t, store, path)
	plan := vodTestPlan(t, path)
	tok, err := segTok.Issue(ctx, parent.ID, parent.UserID)
	if err != nil {
		t.Fatal(err)
	}

	// A finished run from 0: one piece per keyframe, as ffmpeg writes it.
	child := transcode.Session{ID: vodRemuxChildID(parent.ID), UserID: parent.UserID, ParentID: parent.ID, CreatedAt: time.Now()}
	if err := store.Create(ctx, child); err != nil {
		t.Fatal(err)
	}
	var durs []float64
	for j := range kf {
		end := float64(vodTestDurMS) / 1000
		if j+1 < len(kf) {
			end = kf[j+1]
		}
		durs = append(durs, end-kf[j])
	}
	writeRun(t, &child, durs, true)

	srv := vodServer(t, h)
	segURL := func(name string) string {
		return fmt.Sprintf("%s/sessions/%s/vr/seg/%s?token=%s", srv.URL, parent.ID, name, url.QueryEscape(tok))
	}
	for seg := 0; seg < plan.Count(); seg++ {
		s, e := plan.Span(seg)
		var want strings.Builder
		for j, k := range kf {
			if k >= s-0.01 && k < e-0.01 {
				fmt.Fprintf(&want, "piece-%d;", j)
			}
		}
		code, body, clen := getBody(t, segURL(fmt.Sprintf("%d.ts", seg)))
		if code != 200 || body != want.String() || clen != fmt.Sprint(len(want.String())) {
			t.Errorf("segment %d [%v,%v): %d %q (len %s), want %q", seg, s, e, code, body, clen, want.String())
		}
	}
	noMoreJobs(t, store) // every segment came from the existing run

	if code, _, _ := getBody(t, segURL(fmt.Sprintf("%d.ts", plan.Count()))); code != http.StatusNotFound {
		t.Errorf("past the end: %d, want 404", code)
	}
	if code, _, _ := getBody(t, segURL("x.ts")); code != http.StatusBadRequest {
		t.Errorf("bad name: %d, want 400", code)
	}

	// The playlist is the plan's, pointing at these segments.
	code, pl, _ := getBody(t, fmt.Sprintf("%s/sessions/%s/playlist.m3u8?token=%s", srv.URL, parent.ID, url.QueryEscape(tok)))
	if code != 200 || !strings.Contains(pl, "#EXT-X-PLAYLIST-TYPE:VOD") ||
		!strings.Contains(pl, fmt.Sprintf("/api/v1/transcode/sessions/%s/vr/seg/0.ts?token=", parent.ID)) ||
		strings.Count(pl, "#EXTINF:") != plan.Count() {
		t.Errorf("playlist %d:\n%s", code, pl)
	}
}

func TestVODRemuxSegment_MismatchRestartsTheRun(t *testing.T) {
	origWait, origPoll := abrColdStartWait, vodRemuxPoll
	abrColdStartWait, vodRemuxPoll = 400*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { abrColdStartWait, vodRemuxPoll = origWait, origPoll })

	path, _ := vodTestMKV(t)
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	segTok := transcode.NewSegmentTokenManager(v)
	h := NewNativeTranscodeHandler(store, segTok, &mockTranscodeMedia{}, &config.Config{}, slog.Default())
	ctx := context.Background()
	parent := vodTestParent(t, store, path)
	tok, _ := segTok.Issue(ctx, parent.ID, parent.UserID)

	// A run whose pieces never land on the plan's boundaries (a worker that
	// ignored KeyframePieces and cut 3 s segments).
	child := transcode.Session{ID: vodRemuxChildID(parent.ID), UserID: parent.UserID, ParentID: parent.ID, CreatedAt: time.Now()}
	if err := store.Create(ctx, child); err != nil {
		t.Fatal(err)
	}
	writeRun(t, &child, []float64{3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 4}, true)

	srv := vodServer(t, h)
	code, _, _ := getBody(t, fmt.Sprintf("%s/sessions/%s/vr/seg/1.ts?token=%s", srv.URL, parent.ID, url.QueryEscape(tok)))
	if code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503 while the restarted run spins up", code)
	}
	job := drainJob(t, store)
	if job.Incarnation != 1 || !job.CopyTS {
		t.Errorf("restart job = incarnation %d CopyTS %v, want a fresh piece run", job.Incarnation, job.CopyTS)
	}
}

// TestStart_VODRemuxIsOptIn drives the real Start handler: the same remux
// request becomes a VOD remux only when the client declares vodRemux=1.
func TestStart_VODRemuxIsOptIn(t *testing.T) {
	path, _ := vodTestMKV(t)
	for _, optIn := range []bool{true, false} {
		t.Run(fmt.Sprintf("vodRemux=%v", optIn), func(t *testing.T) {
			v := testvalkey.New(t)
			store := transcode.NewSessionStore(v)
			segTok := transcode.NewSegmentTokenManager(v)
			dur := int64(vodTestDurMS)
			h := NewNativeTranscodeHandler(store, segTok, &mockTranscodeMedia{
				item: &media.Item{ID: uuid.New(), Type: "movie", Title: "Test"},
				files: []media.File{{
					ID: uuid.New(), FilePath: path, Container: strPtr("matroska"),
					VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), DurationMS: &dur,
				}},
			}, &config.Config{TranscodeMaxHeight: 2160, TranscodeVODRemux: true}, slog.Default()).
				WithVerifySource(fakeSourceOK)

			raw, _ := json.Marshal(transcodeStartRequest{VideoCopy: true, PositionMS: 21_000})
			req := httptest.NewRequest("POST", "/api/v1/items/x/transcode", bytes.NewReader(raw))
			req = withChiParam(req, "id", uuid.New().String())
			req = withClaims(req)
			caps := "videoDecoder=h264,audioDecoder=aac"
			if optIn {
				caps += ",vodRemux=1"
			}
			req.Header.Set("X-Client-Capabilities", caps)
			rec := httptest.NewRecorder()
			h.Start(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("Start: %d %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				Data transcodeStartResponse `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			sess, err := store.Get(context.Background(), resp.Data.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			job := drainJob(t, store)
			if !optIn {
				if sess.VODRemux || job.CopyTS || job.SessionID != sess.ID || resp.Data.FullTimeline {
					t.Errorf("without the opt-in: VODRemux=%v CopyTS=%v job for %s", sess.VODRemux, job.CopyTS, job.SessionID)
				}
				return
			}
			plan := vodTestPlan(t, path)
			if !sess.VODRemux || sess.RemuxJob == nil || resp.Data.StartOffsetSec != 0 || !resp.Data.FullTimeline {
				t.Errorf("session VODRemux=%v template=%v start_offset=%v full_timeline=%v",
					sess.VODRemux, sess.RemuxJob != nil, resp.Data.StartOffsetSec, resp.Data.FullTimeline)
			}
			// The child is warm at the resume position's segment.
			wantSeg := plan.SegmentAt(21)
			if job.SessionID != vodRemuxChildID(sess.ID) || !job.CopyTS || job.StartOffsetSec != plan.SeekTarget(wantSeg) {
				t.Errorf("first run %s CopyTS=%v seek %v, want child at segment %d (seek %v)",
					job.SessionID, job.CopyTS, job.StartOffsetSec, wantSeg, plan.SeekTarget(wantSeg))
			}
			noMoreJobs(t, store)
		})
	}
}
