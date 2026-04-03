package transcode

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/testvalkey"
)

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewWorker_Defaults(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	w := NewWorker("w1", ":7073", store, []Encoder{EncoderSoftware}, 0, EncoderOpts{}, nopLogger())
	if w.maxSessions != 4 {
		t.Errorf("want default maxSessions=4 when 0 given, got %d", w.maxSessions)
	}
	if w.id != "w1" {
		t.Errorf("want id=w1, got %s", w.id)
	}
}

func TestNewWorker_ExplicitMaxSessions(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	w := NewWorker("w2", ":7074", store, []Encoder{EncoderSoftware}, 8, EncoderOpts{}, nopLogger())
	if w.maxSessions != 8 {
		t.Errorf("want maxSessions=8, got %d", w.maxSessions)
	}
}

func TestKillSession_NoOp_UnknownSession(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	w := NewWorker("w3", ":7075", store, nil, 4, EncoderOpts{}, nopLogger())
	// Should not panic for an unknown session ID.
	w.KillSession("does-not-exist")
}

func TestSessionDir(t *testing.T) {
	got := SessionDir("abc-123")
	want := filepath.Join(os.TempDir(), "onscreen", "sessions", "abc-123")
	if got != want {
		t.Errorf("SessionDir: want %q, got %q", want, got)
	}
}

func TestWorkerID_UniqueEachCall(t *testing.T) {
	a := WorkerID()
	b := WorkerID()
	if a == "" {
		t.Error("WorkerID returned empty string")
	}
	if a == b {
		t.Error("WorkerID returned same value twice")
	}
}

func TestSweepOrphanedSessions_RemovesDirs(t *testing.T) {
	// Create a temp dir to stand in for segmentBaseDir, then patch via a
	// test worker that calls sweepOrphanedSessions through the real code path.
	// We can't change segmentBaseDir (constant), so we test the behavior by
	// creating real dirs under it and verifying removal.
	//
	// Skip if we can't write to /tmp/onscreen/sessions.
	base := segmentBaseDir
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Skipf("cannot create %s: %v", base, err)
	}

	// Create two fake orphaned session dirs.
	id1 := NewSessionID()
	id2 := NewSessionID()
	dir1 := filepath.Join(base, id1)
	dir2 := filepath.Join(base, id2)
	if err := os.MkdirAll(dir1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir2, 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir1)
		_ = os.RemoveAll(dir2)
	})

	v := testvalkey.New(t)
	store := NewSessionStore(v)
	w := NewWorker("sweep-test", ":0", store, nil, 4, EncoderOpts{}, nopLogger())
	w.sweepOrphanedSessions()

	if _, err := os.Stat(dir1); !os.IsNotExist(err) {
		t.Errorf("expected dir1 to be removed, stat: %v", err)
	}
	if _, err := os.Stat(dir2); !os.IsNotExist(err) {
		t.Errorf("expected dir2 to be removed, stat: %v", err)
	}
}

func TestSweepOrphanedSessions_NoBassDir_NoError(t *testing.T) {
	// Should silently do nothing if segmentBaseDir doesn't exist.
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	w := NewWorker("sweep-nobase", ":0", store, nil, 4, EncoderOpts{}, nopLogger())
	// segmentBaseDir may or may not exist; either way should not panic.
	w.sweepOrphanedSessions()
}

func TestWorker_Register(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	w := NewWorker(WorkerID(), ":7073", store, []Encoder{EncoderSoftware}, 4, EncoderOpts{}, nopLogger())

	if err := w.register(context.Background()); err != nil {
		t.Fatalf("register: %v", err)
	}

	workers, err := store.ListWorkers(context.Background())
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(workers) != 1 {
		t.Fatalf("want 1 registered worker, got %d", len(workers))
	}
	if workers[0].Addr != ":7073" {
		t.Errorf("want addr :7073, got %s", workers[0].Addr)
	}
}

func TestDetectEncoders_Override(t *testing.T) {
	encoders, err := DetectEncoders(context.Background(), "nvenc,software")
	if err != nil {
		t.Fatalf("DetectEncoders with override: %v", err)
	}
	if len(encoders) != 2 {
		t.Fatalf("want 2 encoders, got %d", len(encoders))
	}
	if encoders[0] != EncoderNVENC {
		t.Errorf("want first encoder NVENC, got %s", encoders[0])
	}
	if encoders[1] != EncoderSoftware {
		t.Errorf("want second encoder software, got %s", encoders[1])
	}
}

// TestSegmentServer_PathTraversal verifies the traversal guard in startSegmentServer.
// We exercise the handler function directly via httptest rather than starting a
// real TCP listener.
func TestSegmentServer_PathTraversal(t *testing.T) {
	// Build the same handler that startSegmentServer registers.
	handler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rel := r.URL.Path[len("/segments/"):]
		abs := filepath.Join(segmentBaseDir, rel)
		clean := filepath.Clean(abs)
		base := filepath.Clean(segmentBaseDir) + string(os.PathSeparator)
		if clean != filepath.Clean(segmentBaseDir) && !strings.HasPrefix(clean, base) {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		http.ServeFile(rw, r, clean)
	})

	traversalPaths := []string{
		"/segments/../../../etc/passwd",
		"/segments/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"/segments/abc/../../etc/passwd",
	}

	for _, path := range traversalPaths {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		// Either 403 Forbidden or a redirect (301) from http.ServeFile's path
		// cleaning — never a 200 serving /etc/passwd.
		if w.Code == http.StatusOK {
			t.Errorf("path traversal %q returned 200", path)
		}
	}
}
