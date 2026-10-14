package downloads

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// Job states, as the API reports them. A job's state is its encode's; a job
// whose encode was cleaned away reads as expired.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusReady   = "ready"
	StatusFailed  = "failed"
	StatusExpired = "expired"
)

// ErrNotFound: no such job, or not the caller's.
var ErrNotFound = errors.New("downloads: job not found")

// ErrNotReady: the job's file isn't available (still encoding, failed, or
// expired).
var ErrNotReady = errors.New("downloads: file not ready")

// Job is one user's converted download as the API shows it.
type Job struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	ItemID       uuid.UUID
	FileID       uuid.UUID
	ConversionID *uuid.UUID
	Quality      string
	Status       string
	Progress     float64
	Error        string
	SizeBytes    *int64
	ItemTitle    string
	ItemType     string
	CreatedAt    time.Time
	CompletedAt  *time.Time
	ExpiresAt    *time.Time
}

// Active reports whether the job is still waiting on its encode.
func (j Job) Active() bool { return j.Status == StatusQueued || j.Status == StatusRunning }

// Service is the API's surface over the download tables and the cache.
type Service struct {
	pool   *pgxpool.Pool
	q      *gen.Queries
	root   string
	ttl    time.Duration
	runner *Runner
}

// NewService builds the service. root is the download cache directory
// (CACHE_PATH/downloads); ttl how long a finished file is kept after it was
// last requested or fetched.
func NewService(pool *pgxpool.Pool, root string, ttl time.Duration) *Service {
	return &Service{pool: pool, q: gen.New(pool), root: root, ttl: ttl}
}

// WithRunner wires the runner Request wakes and Delete cancels through. Nil
// is fine: encodes then start on the runner's next poll, wherever it runs.
func (s *Service) WithRunner(r *Runner) *Service {
	s.runner = r
	return s
}

// OutputPath is where the conversion's finished MP4 lives in the cache.
func OutputPath(root string, conversionID uuid.UUID) string {
	return filepath.Join(root, conversionID.String()+".mp4")
}

// partPath is the in-progress output; renamed to OutputPath on success, so a
// file at OutputPath is always complete.
func partPath(root string, conversionID uuid.UUID) string {
	return filepath.Join(root, conversionID.String()+".part")
}

// Request creates or reuses the user's job for (file, quality). When the user
// already has a live job for it, that job comes back unchanged (created =
// false). Otherwise the job points at the file's live encode at that quality
// — another user's ready file, a queued or running encode, or a new queued
// one — reusing the user's earlier failed/expired job row when there is one.
func (s *Service) Request(ctx context.Context, userID, itemID, fileID uuid.UUID, quality string) (Job, bool, error) {
	prior, err := s.q.GetUserDownloadJobFor(ctx, gen.GetUserDownloadJobForParams{
		UserID: userID, MediaFileID: fileID, Quality: quality,
	})
	hasPrior := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, fmt.Errorf("find prior job: %w", err)
	}
	if hasPrior {
		j, err := s.Get(ctx, prior.ID)
		if err != nil {
			return Job{}, false, err
		}
		if j.Active() || j.Status == StatusReady {
			// Asking again for a ready file keeps it in the cache.
			if j.Status == StatusReady && j.ConversionID != nil {
				_ = s.q.TouchDownloadConversion(ctx, gen.TouchDownloadConversionParams{
					ID: *j.ConversionID, TtlSeconds: int64(s.ttl.Seconds()),
				})
			}
			return j, false, nil
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Job{}, false, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := s.q.WithTx(tx)
	conv, err := qtx.GetOrCreateDownloadConversion(ctx, gen.GetOrCreateDownloadConversionParams{
		MediaItemID: itemID, MediaFileID: fileID, Quality: quality,
		TtlSeconds: int64(s.ttl.Seconds()),
	})
	if err != nil {
		return Job{}, false, fmt.Errorf("conversion: %w", err)
	}
	convID := pgtype.UUID{Bytes: conv.ID, Valid: true}
	var row gen.DownloadJob
	if hasPrior {
		row, err = qtx.RepointDownloadJob(ctx, gen.RepointDownloadJobParams{ID: prior.ID, ConversionID: convID})
	} else {
		row, err = qtx.CreateDownloadJob(ctx, gen.CreateDownloadJobParams{
			UserID: userID, MediaItemID: itemID, MediaFileID: fileID, Quality: quality, ConversionID: convID,
		})
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, false, fmt.Errorf("commit: %w", err)
	}
	if conv.Status == StatusQueued && s.runner != nil {
		s.runner.Wake()
	}
	j, err := s.Get(ctx, row.ID)
	return j, !hasPrior, err
}

// ActiveCount is how many of the user's jobs are still waiting on an encode.
func (s *Service) ActiveCount(ctx context.Context, userID uuid.UUID) (int, error) {
	n, err := s.q.CountActiveDownloadJobsForUser(ctx, userID)
	return int(n), err
}

// Get reads one job (any user's; callers check ownership).
func (s *Service) Get(ctx context.Context, jobID uuid.UUID) (Job, error) {
	row, err := s.q.GetDownloadJobView(ctx, jobID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, ErrNotFound
		}
		return Job{}, err
	}
	return jobFromRow(gen.ListDownloadJobViewsForUserRow(row)), nil
}

// ListForUser returns the user's jobs, newest first (at most 100).
func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID) ([]Job, error) {
	rows, err := s.q.ListDownloadJobViewsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, jobFromRow(r))
	}
	return out, nil
}

// Delete removes the user's job. An encode nobody else is waiting for is
// cancelled (a running ffmpeg is stopped); a ready file stays in the cache
// for anyone who asks for it next, until the cleanup task evicts it.
func (s *Service) Delete(ctx context.Context, userID, jobID uuid.UUID) error {
	row, err := s.q.DeleteDownloadJob(ctx, gen.DeleteDownloadJobParams{ID: jobID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !row.ConversionID.Valid {
		return nil
	}
	convID := uuid.UUID(row.ConversionID.Bytes)
	n, err := s.q.DeleteUnreferencedPendingConversion(ctx, convID)
	if err != nil {
		return fmt.Errorf("cancel conversion: %w", err)
	}
	if n > 0 {
		if s.runner != nil {
			s.runner.Cancel(convID)
		}
		_ = os.Remove(partPath(s.root, convID))
	}
	return nil
}

// ReadyFile returns the path of a ready job's MP4 and marks it used, so the
// cache keeps it a while longer. ErrNotReady when the job has no finished
// file, including one the cleanup task already evicted from disk.
func (s *Service) ReadyFile(ctx context.Context, j Job) (string, error) {
	if j.Status != StatusReady || j.ConversionID == nil {
		return "", ErrNotReady
	}
	p := OutputPath(s.root, *j.ConversionID)
	if _, err := os.Stat(p); err != nil {
		return "", ErrNotReady
	}
	_ = s.q.TouchDownloadConversion(ctx, gen.TouchDownloadConversionParams{
		ID: *j.ConversionID, TtlSeconds: int64(s.ttl.Seconds()),
	})
	return p, nil
}

func jobFromRow(r gen.ListDownloadJobViewsForUserRow) Job {
	j := Job{
		ID:        r.ID,
		UserID:    r.UserID,
		ItemID:    r.MediaItemID,
		FileID:    r.MediaFileID,
		Quality:   r.Quality,
		Status:    r.Status,
		Progress:  float64(r.Progress),
		SizeBytes: r.SizeBytes,
		ItemTitle: r.ItemTitle,
		ItemType:  r.ItemType,
		CreatedAt: r.CreatedAt.Time,
	}
	if r.ConversionID.Valid {
		id := uuid.UUID(r.ConversionID.Bytes)
		j.ConversionID = &id
	}
	if r.Error != nil {
		j.Error = *r.Error
	}
	if r.CompletedAt.Valid {
		t := r.CompletedAt.Time
		j.CompletedAt = &t
	}
	if r.ExpiresAt.Valid && j.Status == StatusReady {
		t := r.ExpiresAt.Time
		j.ExpiresAt = &t
	}
	if j.Progress < 0 {
		j.Progress = 0
	}
	if j.Progress > 1 {
		j.Progress = 1
	}
	return j
}
