// Hand-written gen code for analytics queries.
// Replace by running `make generate` once sqlc is configured.
package gen

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ── Analytics: overview ───────────────────────────────────────────────────────

const getAnalyticsOverview = `
SELECT
  (SELECT COUNT(*)                     FROM media_items  WHERE deleted_at IS NULL)               AS total_items,
  (SELECT COUNT(*)                     FROM media_files  WHERE status = 'active')                AS total_files,
  (SELECT COALESCE(SUM(file_size), 0)  FROM media_files  WHERE status = 'active')                AS total_size_bytes,
  -- NOTE: total_plays and total_watch_time_ms scan ALL watch_events (no time bound).
  -- On large installations this may be slow; consider a summary table if needed.
  (SELECT COUNT(*)                     FROM watch_events WHERE event_type IN ('stop','scrobble')) AS total_plays,
  (SELECT COALESCE(SUM(duration_ms),0) FROM watch_events WHERE event_type IN ('stop','scrobble')) AS total_watch_time_ms`

type AnalyticsOverviewRow struct {
	TotalItems       int64
	TotalFiles       int64
	TotalSizeBytes   int64
	TotalPlays       int64
	TotalWatchTimeMS int64
}

func (q *Queries) GetAnalyticsOverview(ctx context.Context) (AnalyticsOverviewRow, error) {
	var i AnalyticsOverviewRow
	err := q.db.QueryRow(ctx, getAnalyticsOverview).Scan(
		&i.TotalItems, &i.TotalFiles, &i.TotalSizeBytes, &i.TotalPlays, &i.TotalWatchTimeMS,
	)
	return i, err
}

// ── Analytics: per-library stats ──────────────────────────────────────────────

const getLibraryAnalytics = `
SELECT
  l.id,
  l.name,
  l.type,
  COUNT(DISTINCT mi.id)                                                            AS item_count,
  COALESCE(SUM(mf.file_size), 0)                                                  AS total_size_bytes,
  COUNT(mf.id) FILTER (WHERE mf.resolution_h >= 2160)                             AS res_4k,
  COUNT(mf.id) FILTER (WHERE mf.resolution_h >= 1080 AND mf.resolution_h < 2160) AS res_1080p,
  COUNT(mf.id) FILTER (WHERE mf.resolution_h >= 720  AND mf.resolution_h < 1080) AS res_720p,
  COUNT(mf.id) FILTER (WHERE mf.resolution_h < 720 OR mf.resolution_h IS NULL)   AS res_sd
FROM libraries l
LEFT JOIN media_items mi ON mi.library_id = l.id AND mi.deleted_at IS NULL
LEFT JOIN media_files mf ON mf.media_item_id = mi.id AND mf.status = 'active'
WHERE l.deleted_at IS NULL
GROUP BY l.id, l.name, l.type
ORDER BY l.name`

type LibraryAnalyticsRow struct {
	ID             uuid.UUID
	Name           string
	Type           string
	ItemCount      int64
	TotalSizeBytes int64
	Res4K          int64
	Res1080p       int64
	Res720p        int64
	ResSD          int64
}

func (q *Queries) GetLibraryAnalytics(ctx context.Context) ([]LibraryAnalyticsRow, error) {
	rows, err := q.db.Query(ctx, getLibraryAnalytics)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []LibraryAnalyticsRow
	for rows.Next() {
		var i LibraryAnalyticsRow
		if err := rows.Scan(
			&i.ID, &i.Name, &i.Type, &i.ItemCount, &i.TotalSizeBytes,
			&i.Res4K, &i.Res1080p, &i.Res720p, &i.ResSD,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// ── Analytics: codec & container breakdown ────────────────────────────────────

const getVideoCodecBreakdown = `
SELECT COALESCE(video_codec, 'unknown') AS codec, COUNT(*) AS count
FROM media_files
WHERE status = 'active'
GROUP BY video_codec
ORDER BY count DESC
LIMIT 10`

type CodecCountRow struct {
	Codec string
	Count int64
}

func (q *Queries) GetVideoCodecBreakdown(ctx context.Context) ([]CodecCountRow, error) {
	rows, err := q.db.Query(ctx, getVideoCodecBreakdown)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CodecCountRow
	for rows.Next() {
		var i CodecCountRow
		if err := rows.Scan(&i.Codec, &i.Count); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

const getContainerBreakdown = `
SELECT COALESCE(container, 'unknown') AS container, COUNT(*) AS count
FROM media_files
WHERE status = 'active'
GROUP BY container
ORDER BY count DESC
LIMIT 10`

type ContainerCountRow struct {
	Container string
	Count     int64
}

func (q *Queries) GetContainerBreakdown(ctx context.Context) ([]ContainerCountRow, error) {
	rows, err := q.db.Query(ctx, getContainerBreakdown)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ContainerCountRow
	for rows.Next() {
		var i ContainerCountRow
		if err := rows.Scan(&i.Container, &i.Count); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// ── Analytics: play activity ──────────────────────────────────────────────────

const getPlaysPerDay = `
SELECT DATE(occurred_at) AS date, COUNT(*) AS count
FROM watch_events
WHERE event_type IN ('stop', 'scrobble')
  AND occurred_at >= NOW() - INTERVAL '30 days'
GROUP BY DATE(occurred_at)
ORDER BY date`

type DayCountRow struct {
	Date  time.Time
	Count int64
}

func (q *Queries) GetPlaysPerDay(ctx context.Context) ([]DayCountRow, error) {
	rows, err := q.db.Query(ctx, getPlaysPerDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []DayCountRow
	for rows.Next() {
		var i DayCountRow
		if err := rows.Scan(&i.Date, &i.Count); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

const getTopPlayed = `
SELECT mi.id, mi.title, mi.year, mi.type, mi.poster_path, COUNT(we.id) AS play_count
FROM watch_events we
JOIN media_items mi ON mi.id = we.media_id
WHERE we.event_type IN ('stop', 'scrobble')
  AND we.occurred_at > NOW() - INTERVAL '90 days'
  AND mi.deleted_at IS NULL
GROUP BY mi.id, mi.title, mi.year, mi.type, mi.poster_path
ORDER BY play_count DESC
LIMIT 10`

type TopPlayedRow struct {
	ID         uuid.UUID
	Title      string
	Year       pgtype.Int4
	Type       string
	PosterPath pgtype.Text
	PlayCount  int64
}

func (q *Queries) GetTopPlayed(ctx context.Context) ([]TopPlayedRow, error) {
	rows, err := q.db.Query(ctx, getTopPlayed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []TopPlayedRow
	for rows.Next() {
		var i TopPlayedRow
		if err := rows.Scan(&i.ID, &i.Title, &i.Year, &i.Type, &i.PosterPath, &i.PlayCount); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// ── Sessions: media item lookup ───────────────────────────────────────────────

const getMediaItemsForSessions = `
SELECT id, title, year, type, poster_path, duration_ms
FROM media_items
WHERE id = ANY($1) AND deleted_at IS NULL`

type SessionMediaItem struct {
	ID         uuid.UUID
	Title      string
	Year       pgtype.Int4
	Type       string
	PosterPath pgtype.Text
	DurationMS pgtype.Int8
}

func (q *Queries) GetMediaItemsForSessions(ctx context.Context, ids []uuid.UUID) ([]SessionMediaItem, error) {
	rows, err := q.db.Query(ctx, getMediaItemsForSessions, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []SessionMediaItem
	for rows.Next() {
		var i SessionMediaItem
		if err := rows.Scan(&i.ID, &i.Title, &i.Year, &i.Type, &i.PosterPath, &i.DurationMS); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// ── Sessions: file-path lookup ────────────────────────────────────────────────

const getMediaItemByFilePath = `
SELECT mi.id, mi.title, mi.year, mi.type, mi.poster_path, mi.duration_ms
FROM media_files mf
JOIN media_items mi ON mi.id = mf.media_item_id
WHERE mf.file_path = $1 AND mi.deleted_at IS NULL
LIMIT 1`

func (q *Queries) GetMediaItemByFilePath(ctx context.Context, filePath string) (*SessionMediaItem, error) {
	var i SessionMediaItem
	err := q.db.QueryRow(ctx, getMediaItemByFilePath, filePath).Scan(
		&i.ID, &i.Title, &i.Year, &i.Type, &i.PosterPath, &i.DurationMS,
	)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

// ── Analytics: bandwidth per day ─────────────────────────────────────────────

const getBandwidthPerDay = `
SELECT DATE(we.occurred_at) AS date,
       COALESCE(SUM(
         COALESCE(mf_direct.bitrate, mf_any.bitrate)::BIGINT * we.duration_ms / 8000
       ), 0) AS bytes
FROM watch_events we
LEFT JOIN media_files mf_direct
       ON mf_direct.id = we.file_id AND mf_direct.bitrate IS NOT NULL
LEFT JOIN LATERAL (
  SELECT bitrate FROM media_files
  WHERE media_item_id = we.media_id AND status = 'active' AND bitrate IS NOT NULL
  ORDER BY bitrate DESC LIMIT 1
) mf_any ON TRUE
WHERE we.event_type IN ('stop', 'scrobble')
  AND we.occurred_at >= NOW() - INTERVAL '30 days'
  AND we.duration_ms IS NOT NULL
  AND COALESCE(mf_direct.bitrate, mf_any.bitrate) IS NOT NULL
GROUP BY DATE(we.occurred_at)
ORDER BY date`

type DayBytesRow struct {
	Date  time.Time
	Bytes int64
}

func (q *Queries) GetBandwidthPerDay(ctx context.Context) ([]DayBytesRow, error) {
	rows, err := q.db.Query(ctx, getBandwidthPerDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []DayBytesRow
	for rows.Next() {
		var i DayBytesRow
		if err := rows.Scan(&i.Date, &i.Bytes); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

const getRecentPlays = `
SELECT mi.title, mi.year, mi.type, we.occurred_at, we.client_name, we.duration_ms
FROM watch_events we
JOIN media_items mi ON mi.id = we.media_id
WHERE we.event_type IN ('stop', 'scrobble')
  AND we.occurred_at > NOW() - INTERVAL '30 days'
ORDER BY we.occurred_at DESC
LIMIT 20`

type RecentPlayRow struct {
	Title      string
	Year       pgtype.Int4
	Type       string
	OccurredAt time.Time
	ClientName pgtype.Text
	DurationMS pgtype.Int8
}

func (q *Queries) GetRecentPlays(ctx context.Context) ([]RecentPlayRow, error) {
	rows, err := q.db.Query(ctx, getRecentPlays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []RecentPlayRow
	for rows.Next() {
		var i RecentPlayRow
		if err := rows.Scan(
			&i.Title, &i.Year, &i.Type, &i.OccurredAt, &i.ClientName, &i.DurationMS,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
