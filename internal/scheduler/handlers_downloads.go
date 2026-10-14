package scheduler

import (
	"context"
	"encoding/json"

	"github.com/onscreen/onscreen/internal/downloads"
)

// DownloadCacheCleaner keeps the converted-download cache within its expiry
// and size limit. Satisfied by *downloads.Cleaner.
type DownloadCacheCleaner interface {
	Run(ctx context.Context) (downloads.CleanupResult, error)
}

// NewDownloadCacheCleanupHandler returns the download_cache_cleanup task:
// evict converted downloads past their expiry, then the least recently used
// ones until the cache fits DOWNLOAD_CACHE_MAX_GB, remove orphaned files, and
// forget old failed/expired jobs.
func NewDownloadCacheCleanupHandler(c DownloadCacheCleaner) HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		res, err := c.Run(ctx)
		if err != nil {
			return "", err
		}
		return res.String(), nil
	}
}
