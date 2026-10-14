package downloads

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// retention is how long failed/expired encodes and jobs with no file are
// remembered (so a user's Downloads list explains what happened) before the
// cleanup task forgets them.
const retention = 30 * 24 * time.Hour

// orphanGrace: a file in the cache that no live conversion claims is only
// removed once it is this old, so a conversion claimed a moment ago (its
// .part just created) is never mistaken for an orphan.
const orphanGrace = time.Hour

// Cleaner keeps the download cache within its expiry and size limit. Run by
// the download_cache_cleanup scheduler task.
type Cleaner struct {
	q        *gen.Queries
	root     string
	maxBytes int64
	// optimizedRoot is the optimized-versions directory; "" skips its
	// sweep (see WithOptimized).
	optimizedRoot string
	logger        *slog.Logger
	now           func() time.Time
}

// NewCleaner builds the cleaner for the cache at root, holding it under
// maxBytes.
func NewCleaner(pool *pgxpool.Pool, root string, maxBytes int64, logger *slog.Logger) *Cleaner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Cleaner{q: gen.New(pool), root: root, maxBytes: maxBytes, logger: logger, now: time.Now}
}

// WithOptimized also sweeps the optimized-versions directory: versions
// whose source was replaced since they were encoded are deleted, and files
// whose row is gone (the source file was deleted) are removed. Optimized
// versions are never evicted for age or size.
func (c *Cleaner) WithOptimized(root string) *Cleaner {
	c.optimizedRoot = root
	return c
}

// CleanupResult summarises one run.
type CleanupResult struct {
	Expired      int   // ready files past their expiry, evicted
	Evicted      int   // ready files evicted to get under the size limit
	Orphans      int   // cache files no conversion claims, removed
	Stale        int   // optimized versions whose source was replaced, deleted
	FreedBytes   int64 // total bytes removed
	CacheBytes   int64 // ready bytes left in the cache
	PurgedRows   int64 // old failed/expired conversions and jobs forgotten
	RemovalFails int   // files that couldn't be removed (retried next run)
}

func (r CleanupResult) String() string {
	return fmt.Sprintf("expired %d, evicted %d for size, removed %d orphans, deleted %d stale optimized versions, freed %d MB, cache now %d MB, forgot %d rows",
		r.Expired, r.Evicted, r.Orphans, r.Stale, r.FreedBytes>>20, r.CacheBytes>>20, r.PurgedRows)
}

// Run evicts expired files, then least-recently-used ones until the ready
// files fit under the size limit, removes orphaned cache files, and forgets
// old failed/expired rows.
func (c *Cleaner) Run(ctx context.Context) (CleanupResult, error) {
	var res CleanupResult

	expired, err := c.q.ListExpiredDownloadConversions(ctx)
	if err != nil {
		return res, fmt.Errorf("list expired: %w", err)
	}
	for _, conv := range expired {
		if c.evict(ctx, conv, &res) {
			res.Expired++
		}
	}

	ready, err := c.q.ListReadyDownloadConversionsLRU(ctx)
	if err != nil {
		return res, fmt.Errorf("list ready: %w", err)
	}
	var total int64
	for _, conv := range ready {
		total += sizeOf(conv)
	}
	for _, conv := range pickEvictions(ready, total, c.maxBytes) {
		if c.evict(ctx, conv, &res) {
			res.Evicted++
			total -= sizeOf(conv)
		}
	}
	res.CacheBytes = total

	if ids, err := c.q.ListLiveDownloadConversionIDs(ctx); err != nil {
		c.logger.WarnContext(ctx, "downloads: orphan sweep", "err", err)
	} else if err := c.removeOrphans(c.root, ids, &res); err != nil {
		c.logger.WarnContext(ctx, "downloads: orphan sweep", "err", err)
	}
	if c.optimizedRoot != "" {
		c.sweepOptimized(ctx, &res)
	}

	before := pgTime(c.now().Add(-retention))
	n1, err := c.q.PurgeOldDownloadConversions(ctx, before)
	if err != nil {
		return res, fmt.Errorf("purge conversions: %w", err)
	}
	n2, err := c.q.PurgeOldDownloadJobs(ctx, before)
	if err != nil {
		return res, fmt.Errorf("purge jobs: %w", err)
	}
	res.PurgedRows = n1 + n2
	return res, nil
}

// pickEvictions returns the least-recently-used prefix of ready (already in
// LRU order) whose removal brings total under maxBytes. maxBytes <= 0 = no
// limit.
func pickEvictions(ready []gen.DownloadConversion, total, maxBytes int64) []gen.DownloadConversion {
	if maxBytes <= 0 || total <= maxBytes {
		return nil
	}
	var out []gen.DownloadConversion
	for _, conv := range ready {
		if total <= maxBytes {
			break
		}
		out = append(out, conv)
		total -= sizeOf(conv)
	}
	return out
}

func sizeOf(c gen.DownloadConversion) int64 {
	if c.SizeBytes == nil {
		return 0
	}
	return *c.SizeBytes
}

// evict deletes a ready conversion's file and marks it expired. A file that
// can't be removed (Windows: still open by a download in progress) keeps its
// row ready so the next run tries again.
func (c *Cleaner) evict(ctx context.Context, conv gen.DownloadConversion, res *CleanupResult) bool {
	p := OutputPath(c.root, conv.ID)
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		res.RemovalFails++
		c.logger.WarnContext(ctx, "downloads: remove cached file", "conversion_id", conv.ID, "err", err)
		return false
	}
	if err := c.q.MarkDownloadConversionExpired(ctx, conv.ID); err != nil {
		c.logger.WarnContext(ctx, "downloads: mark expired", "conversion_id", conv.ID, "err", err)
		return false
	}
	res.FreedBytes += sizeOf(conv)
	return true
}

// sweepOptimized deletes optimized versions whose source was replaced in
// place, then removes files in the optimized directory no live row claims —
// the rows of a deleted source file cascade away with it.
func (c *Cleaner) sweepOptimized(ctx context.Context, res *CleanupResult) {
	stale, err := c.q.ListStaleOptimizeConversions(ctx)
	if err != nil {
		c.logger.WarnContext(ctx, "optimize: list stale versions", "err", err)
	}
	for _, conv := range stale {
		if _, err := c.q.DeleteOptimizeConversion(ctx, conv.ID); err != nil {
			c.logger.WarnContext(ctx, "optimize: delete stale version", "conversion_id", conv.ID, "err", err)
			continue
		}
		c.logger.InfoContext(ctx, "optimize: deleted version of a replaced source file",
			"conversion_id", conv.ID, "file_id", conv.MediaFileID, "profile", conv.Quality)
		res.Stale++
		for _, p := range []string{OutputPath(c.optimizedRoot, conv.ID), partPath(c.optimizedRoot, conv.ID)} {
			if err := os.Remove(p); err == nil {
				res.FreedBytes += sizeOf(conv)
			} else if !errors.Is(err, fs.ErrNotExist) {
				res.RemovalFails++ // the orphan sweep retries it
			}
		}
	}
	ids, err := c.q.ListLiveOptimizeConversionIDs(ctx)
	if err != nil {
		c.logger.WarnContext(ctx, "optimize: orphan sweep", "err", err)
		return
	}
	if err := c.removeOrphans(c.optimizedRoot, ids, res); err != nil {
		c.logger.WarnContext(ctx, "optimize: orphan sweep", "err", err)
	}
}

// removeOrphans deletes files in root no queued/running/ready conversion
// (ids) claims: leftovers of a crash, or of a row purged while its file
// stayed.
func (c *Cleaner) removeOrphans(root string, ids []uuid.UUID, res *CleanupResult) error {
	live := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		live[id] = true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	cutoff := c.now().Add(-orphanGrace)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".mp4" && ext != ".part" {
			continue
		}
		id, err := uuid.Parse(strings.TrimSuffix(name, ext))
		if err != nil || live[id] {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			res.RemovalFails++
			continue
		}
		res.Orphans++
		res.FreedBytes += info.Size()
	}
	return nil
}

func pgTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
