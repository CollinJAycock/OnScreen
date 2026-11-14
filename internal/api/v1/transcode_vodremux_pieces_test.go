package v1

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

// pieceWorker is a fake transcode worker serving a child's pieces; a name in
// fail answers a GET with that status and an error body instead (its HEAD
// still succeeds: the piece vanished between the size check and the fetch).
func pieceWorker(t *testing.T, child *transcode.Session, pieces map[string]string, fail map[string]int) {
	t.Helper()
	prefix := "/segments/" + child.DirName() + "/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, prefix)
		if code, ok := fail[name]; ok && r.Method == http.MethodGet {
			http.Error(w, "worker error body", code)
			return
		}
		body, ok := pieces[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, name, time.Time{}, strings.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	child.WorkerAddr = strings.TrimPrefix(srv.URL, "http://")
}

func TestServePieces_RemoteConcatenatesPieces(t *testing.T) {
	child := &transcode.Session{ID: "vr-child-" + uuid.NewString()}
	pieceWorker(t, child, map[string]string{"seg00002.ts": "two;", "seg00003.ts": "three;"}, nil)

	rec := httptest.NewRecorder()
	servePieces(rec, httptest.NewRequest(http.MethodGet, "/seg/1.ts", nil), child, transcode.PieceSpan{First: 2, Last: 3, Ready: true}, ".ts")
	if rec.Code != http.StatusOK || rec.Body.String() != "two;three;" || rec.Header().Get("Content-Length") != "10" {
		t.Fatalf("got %d %q (len %s)", rec.Code, rec.Body.String(), rec.Header().Get("Content-Length"))
	}

	rec = httptest.NewRecorder()
	servePieces(rec, httptest.NewRequest(http.MethodHead, "/seg/1.ts", nil), child, transcode.PieceSpan{First: 2, Last: 3, Ready: true}, ".ts")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Length") != "10" || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: got %d len %s body %q", rec.Code, rec.Header().Get("Content-Length"), rec.Body.String())
	}
}

// A piece the worker refuses (wiped run, busy worker) is a 503 before any
// header goes out — not a 200 with the worker's error text spliced into the
// segment.
func TestServePieces_RemoteBadPieceIsA503(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusServiceUnavailable} {
		child := &transcode.Session{ID: "vr-child-" + uuid.NewString()}
		pieceWorker(t, child, map[string]string{"seg00002.ts": "two;", "seg00003.ts": "three;"}, map[string]int{"seg00003.ts": code})

		rec := httptest.NewRecorder()
		servePieces(rec, httptest.NewRequest(http.MethodGet, "/seg/1.ts", nil), child, transcode.PieceSpan{First: 2, Last: 3, Ready: true}, ".ts")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("worker %d: status %d, want 503", code, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "two;") || strings.Contains(body, "worker error body") {
			t.Errorf("worker %d: body %q carries piece or worker bytes", code, body)
		}
	}
}

// When the session store can't say whether the child exists (here: a record
// it can't decode), that isn't "no child": no second run is dispatched, and
// the caller learns it so the segment request can 503.
func TestEnsureVODRemuxChild_StoreErrorDispatchesNothing(t *testing.T) {
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	h := NewNativeTranscodeHandler(store, transcode.NewSegmentTokenManager(v), &mockTranscodeMedia{}, &config.Config{}, slog.Default()).WithLibraryAccess(allowAllLibraries{})
	ctx := context.Background()
	parent := &transcode.Session{ID: "vr-parent-" + uuid.NewString(), UserID: uuid.New(), VODRemux: true,
		RemuxJob: &transcode.TranscodeJob{Decision: "remux", Encoder: "copy"}}
	if err := store.Create(ctx, *parent); err != nil {
		t.Fatal(err)
	}
	if err := v.Set(ctx, "transcode:session:"+vodRemuxChildID(parent.ID), "{not json", time.Minute); err != nil {
		t.Fatal(err)
	}

	if err := h.ensureVODRemuxChild(ctx, parent, transcode.RemuxPlan{}, 0, false); err == nil {
		t.Fatal("want an error for an unreadable child record")
	}
	noMoreJobs(t, store)

	// The same guard on ABR rungs: nothing dispatched.
	rungChild := abrChildID(parent.ID, "720p")
	if err := v.Set(ctx, "transcode:session:"+rungChild, "{not json", time.Minute); err != nil {
		t.Fatal(err)
	}
	h.ensureRungChild(ctx, parent, transcode.Rendition{Label: "720p"}, rungChild, 0, false)
	noMoreJobs(t, store)
}

// The segment route answers that 503 itself.
func TestVODRemuxSegment_StoreErrorIs503(t *testing.T) {
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	segTok := transcode.NewSegmentTokenManager(v)
	h := NewNativeTranscodeHandler(store, segTok, &mockTranscodeMedia{}, &config.Config{}, slog.Default()).WithLibraryAccess(allowAllLibraries{})
	path, _ := vodTestMKV(t)
	ctx := context.Background()
	parent := vodTestParent(t, store, path)
	tok, err := segTok.Issue(ctx, parent.ID, parent.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Set(ctx, "transcode:session:"+vodRemuxChildID(parent.ID), "{not json", time.Minute); err != nil {
		t.Fatal(err)
	}
	srv := vodServer(t, h)
	resp, err := http.Get(srv.URL + "/sessions/" + parent.ID + "/vr/seg/0.ts?token=" + tok)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", resp.StatusCode)
	}
	noMoreJobs(t, store)
}
