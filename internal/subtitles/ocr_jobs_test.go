package subtitles

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// testSubtitleRow is a minimal stored-subtitle row; only its presence matters
// to these tests.
func testSubtitleRow() gen.ExternalSubtitle {
	return gen.ExternalSubtitle{ID: uuid.New(), Language: "eng", Source: "ocr"}
}

// newTestStore builds a store without the background sweep goroutine, so a
// test drives GC explicitly instead of racing a ticker.
func newTestStore(ttl time.Duration) *OCRJobStore {
	return &OCRJobStore{
		jobs: make(map[string]*OCRJob),
		ttl:  ttl,
		done: make(chan struct{}),
	}
}

// The store documents "TTL applies from job completion, not creation. A
// long-running job won't be evicted while it's still in flight." The GC used to
// evict on the creation-seeded expiry regardless of status, so an OCR pass that
// outran the TTL vanished mid-flight and its result was discarded on arrival.
func TestOCRJobStore_GCKeepsRunningJobPastTTL(t *testing.T) {
	s := newTestStore(time.Hour)
	job, err := s.Create(uuid.New(), 3)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Push the job's expiry into the past — what a real job does simply by
	// running longer than the TTL.
	s.mu.Lock()
	s.jobs[job.ID].expiresAt = time.Now().Add(-time.Minute)
	s.mu.Unlock()

	s.mu.Lock()
	s.gcLocked()
	s.mu.Unlock()

	got, ok := s.Get(job.ID)
	if !ok {
		t.Fatal("running job was evicted past its creation TTL: its result " +
			"will be dropped by Complete and the client polls a 404 forever")
	}
	if got.Status != OCRJobRunning {
		t.Errorf("status: got %q, want %q", got.Status, OCRJobRunning)
	}
}

// The result of a long pass must still land. This is the consequence the
// eviction bug actually produced, exercised end to end through Complete.
func TestOCRJobStore_CompleteAfterTTLStillDelivers(t *testing.T) {
	s := newTestStore(time.Hour)
	job, err := s.Create(uuid.New(), 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s.mu.Lock()
	s.jobs[job.ID].expiresAt = time.Now().Add(-time.Minute)
	s.gcLocked()
	s.mu.Unlock()

	s.Complete(job.ID, testSubtitleRow())

	got, ok := s.Get(job.ID)
	if !ok {
		t.Fatal("job gone after Complete")
	}
	if got.Status != OCRJobCompleted {
		t.Fatalf("status: got %q, want %q", got.Status, OCRJobCompleted)
	}
	if got.Result == nil {
		t.Error("completed job carries no result")
	}
}

// A job whose goroutine died without reaching a terminal state (runOCRJob runs
// under SafeGo, so a panic never calls Fail) must still be reclaimed — the
// running exemption is bounded, not permanent.
func TestOCRJobStore_GCReclaimsStalledRunningJob(t *testing.T) {
	s := newTestStore(time.Hour)
	job, err := s.Create(uuid.New(), 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s.mu.Lock()
	s.jobs[job.ID].StartedAt = time.Now().Add(-maxRunTime - time.Minute)
	s.gcLocked()
	s.mu.Unlock()

	if _, ok := s.Get(job.ID); ok {
		t.Errorf("job stuck in %q for longer than maxRunTime was not reclaimed; "+
			"one panicked OCR pass would pin a map entry for the life of the process",
			OCRJobRunning)
	}
}

// Terminal jobs must still expire — the fix must not turn the GC off.
func TestOCRJobStore_GCStillEvictsTerminalJobs(t *testing.T) {
	s := newTestStore(time.Hour)
	done, err := s.Create(uuid.New(), 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	failed, err := s.Create(uuid.New(), 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s.Complete(done.ID, testSubtitleRow())
	s.Fail(failed.ID, errors.New("tesseract exploded"))

	s.mu.Lock()
	s.jobs[done.ID].expiresAt = time.Now().Add(-time.Minute)
	s.jobs[failed.ID].expiresAt = time.Now().Add(-time.Minute)
	s.gcLocked()
	s.mu.Unlock()

	for name, id := range map[string]string{"completed": done.ID, "failed": failed.ID} {
		if _, ok := s.Get(id); ok {
			t.Errorf("%s job past its TTL was not evicted", name)
		}
	}
}
