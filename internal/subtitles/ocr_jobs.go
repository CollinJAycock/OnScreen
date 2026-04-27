package subtitles

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// OCRJobStatus is the lifecycle state of a background OCR job.
type OCRJobStatus string

const (
	OCRJobRunning   OCRJobStatus = "running"
	OCRJobCompleted OCRJobStatus = "completed"
	OCRJobFailed    OCRJobStatus = "failed"
)

// OCRJob is the public-facing record of a single OCR run that the API
// surfaces via the job-status endpoint. Embedding tesseract behind a job
// abstraction lets the HTTP request return immediately (202) instead of
// blocking for the multi-minute pipeline; clients poll for completion.
//
// This was added in v2.1 because the synchronous endpoint that shipped in
// v2.0 hit Cloudflare Tunnel's 100-second response ceiling on PGS-only
// movies long enough to actually need OCR — feature-length tracks. The
// old request-bound path also coupled tesseract's lifetime to r.Context(),
// so an aborted client killed the subprocess and produced zero output
// even when the upstream proxy timeout was the only culprit.
type OCRJob struct {
	ID            string             `json:"job_id"`
	Status        OCRJobStatus       `json:"status"`
	FileID        uuid.UUID          `json:"file_id"`
	StreamIndex   int                `json:"stream_index"`
	StartedAt     time.Time          `json:"started_at"`
	CompletedAt   *time.Time         `json:"completed_at,omitempty"`
	Error         string             `json:"error,omitempty"`
	Result        *gen.ExternalSubtitle `json:"-"` // surfaced by the handler when status=completed
	expiresAt     time.Time
}

// OCRJobStore tracks OCR jobs in memory with a TTL. Single-instance only
// — multi-instance OnScreen would need a shared store (Valkey-backed,
// drop-in by implementing the same lookup signature). v2.1 ships the
// in-memory variant because the SAML request tracker made the same
// trade-off and SAML deployments are rarely multi-instance.
//
// TTL applies from job completion (or failure), not creation. A
// long-running job won't be evicted while it's still in flight; the
// 1-hour clock starts after it transitions to a terminal state.
type OCRJobStore struct {
	mu   sync.Mutex
	jobs map[string]*OCRJob
	ttl  time.Duration
}

// NewOCRJobStore returns a fresh store with a 1-hour TTL on terminal
// jobs. The watch-page poll loop runs at ~5s so a successful job is
// typically observed and dropped in seconds; the hour upper bound
// covers the case where the tab closes before the user sees the result.
func NewOCRJobStore() *OCRJobStore {
	return &OCRJobStore{
		jobs: make(map[string]*OCRJob),
		ttl:  time.Hour,
	}
}

// Create registers a new running job and returns it ready for the
// caller to spawn the OCR goroutine. ID is base64url-encoded random
// bytes — long enough that an attacker can't brute-force a valid id.
func (s *OCRJobStore) Create(fileID uuid.UUID, streamIndex int) (*OCRJob, error) {
	id, err := randomJobID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	job := &OCRJob{
		ID:          id,
		Status:      OCRJobRunning,
		FileID:      fileID,
		StreamIndex: streamIndex,
		StartedAt:   now,
		expiresAt:   now.Add(s.ttl), // refreshed on terminal-state transition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	s.jobs[id] = job
	return job, nil
}

// Get returns a snapshot of the job by id, or false if unknown / expired.
// Returns a copy so callers can't mutate store state through the pointer.
func (s *OCRJobStore) Get(id string) (OCRJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	j, ok := s.jobs[id]
	if !ok {
		return OCRJob{}, false
	}
	return *j, true
}

// Complete marks the job successful and stores the resulting external-
// subtitles row. The TTL clock starts now — earlier we left it pinned to
// creation time, but that meant a 60-minute OCR run had only seconds for
// the client to poll the result before eviction.
func (s *OCRJobStore) Complete(id string, row gen.ExternalSubtitle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return
	}
	now := time.Now()
	j.Status = OCRJobCompleted
	j.CompletedAt = &now
	j.Result = &row
	j.expiresAt = now.Add(s.ttl)
}

// Fail records an OCR failure with the underlying error message. Stored
// for the same TTL as a completed job so the client polling sees the
// final state instead of a "job not found" 404.
func (s *OCRJobStore) Fail(id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return
	}
	now := time.Now()
	j.Status = OCRJobFailed
	j.CompletedAt = &now
	if err != nil {
		j.Error = err.Error()
	}
	j.expiresAt = now.Add(s.ttl)
}

// gcLocked must be called with the mutex held. Drops jobs whose TTL has
// elapsed. O(n) per call, n is bounded by concurrent OCR jobs in the
// last hour — typically <10 even for a busy library.
func (s *OCRJobStore) gcLocked() {
	now := time.Now()
	for id, j := range s.jobs {
		if now.After(j.expiresAt) {
			delete(s.jobs, id)
		}
	}
}

// ErrJobNotFound is returned by handlers when the requested job id is
// unknown or has been evicted by TTL. Distinct from a generic 404 so the
// frontend can surface a clearer "session expired, please retry" message
// rather than the generic not-found.
var ErrJobNotFound = errors.New("ocr job not found or expired")

func randomJobID() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
