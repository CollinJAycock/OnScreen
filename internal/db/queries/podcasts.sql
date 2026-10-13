-- Podcast RSS subscriptions (internal/podcast, migration 00039).

-- name: CreatePodcastFeed :one
INSERT INTO podcast_feeds (
    library_id, feed_url, title, author, description, image_url, site_url,
    language, folder, auto_download, keep_latest, etag, last_modified,
    last_checked_at, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now(), $14
)
RETURNING *;

-- name: GetPodcastFeed :one
SELECT * FROM podcast_feeds WHERE id = $1;

-- name: ListPodcastFeeds :many
-- Every feed, or a library's feeds when library_id is given, with how many
-- episodes each lists and has downloaded.
SELECT f.*,
       (SELECT count(*) FROM podcast_feed_episodes e WHERE e.feed_id = f.id)::bigint AS episode_count,
       (SELECT count(*) FROM podcast_feed_episodes e
         WHERE e.feed_id = f.id AND e.status = 'downloaded')::bigint AS downloaded_count
FROM podcast_feeds f
WHERE sqlc.narg(library_id)::uuid IS NULL OR f.library_id = sqlc.narg(library_id)::uuid
ORDER BY lower(f.title), f.id;

-- name: UpdatePodcastFeedSettings :one
UPDATE podcast_feeds
SET auto_download = $2,
    keep_latest   = $3,
    updated_at    = now()
WHERE id = $1
RETURNING *;

-- name: UpdatePodcastFeedFromRefresh :exec
-- A refresh that got the feed: its current details and validators.
UPDATE podcast_feeds
SET title           = $2,
    author          = $3,
    description     = $4,
    image_url       = $5,
    site_url        = $6,
    language        = $7,
    etag            = $8,
    last_modified   = $9,
    last_checked_at = now(),
    last_error      = NULL,
    updated_at      = now()
WHERE id = $1;

-- name: SetPodcastFeedChecked :exec
-- A refresh that found nothing new (304) or failed (last_error set).
UPDATE podcast_feeds
SET last_checked_at = now(),
    last_error      = sqlc.narg(last_error)
WHERE id = $1;

-- name: SetPodcastFeedShowItem :exec
UPDATE podcast_feeds
SET show_item_id = $2
WHERE id = $1 AND show_item_id IS DISTINCT FROM $2;

-- name: DeletePodcastFeed :exec
DELETE FROM podcast_feeds WHERE id = $1;

-- name: ListPodcastEpisodeGUIDs :many
SELECT guid FROM podcast_feed_episodes WHERE feed_id = $1;

-- name: UpsertPodcastEpisode :one
-- Adds an episode a refresh found, or updates the details of one already
-- listed. Download state (status, file_path, …) is never touched here.
INSERT INTO podcast_feed_episodes (
    feed_id, guid, title, description, published_at, duration_ms,
    enclosure_url, enclosure_type, enclosure_length, season, episode, image_url
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
ON CONFLICT (feed_id, guid) DO UPDATE
SET title            = EXCLUDED.title,
    description      = EXCLUDED.description,
    published_at     = EXCLUDED.published_at,
    duration_ms      = EXCLUDED.duration_ms,
    enclosure_url    = EXCLUDED.enclosure_url,
    enclosure_type   = EXCLUDED.enclosure_type,
    enclosure_length = EXCLUDED.enclosure_length,
    season           = EXCLUDED.season,
    episode          = EXCLUDED.episode,
    image_url        = EXCLUDED.image_url,
    updated_at       = now()
RETURNING *;

-- name: GetPodcastEpisode :one
SELECT * FROM podcast_feed_episodes WHERE id = $1;

-- name: ListPodcastEpisodes :many
-- A feed's episodes, newest first.
SELECT * FROM podcast_feed_episodes
WHERE feed_id = $1
ORDER BY published_at DESC NULLS LAST, created_at DESC, id
LIMIT $2 OFFSET $3;

-- name: CountPodcastEpisodes :one
SELECT count(*) FROM podcast_feed_episodes WHERE feed_id = $1;

-- name: ListLatestPodcastEpisodes :many
-- A feed's newest episodes, for the first download after subscribing.
SELECT * FROM podcast_feed_episodes
WHERE feed_id = $1
ORDER BY published_at DESC NULLS LAST, created_at DESC, id
LIMIT $2;

-- name: ListDownloadedPodcastEpisodes :many
-- A feed's downloaded episodes, newest first by publish date: retention
-- keeps the head (the newest N episodes).
SELECT * FROM podcast_feed_episodes
WHERE feed_id = $1 AND status = 'downloaded'
ORDER BY published_at DESC NULLS LAST, downloaded_at DESC NULLS LAST, id;

-- name: ClaimPodcastEpisodeDownload :one
-- Claims an episode for download. Returns no row when it's downloading or
-- downloaded already, so two requests can't fetch the same episode.
UPDATE podcast_feed_episodes
SET status     = 'downloading',
    file_path  = $2,
    error      = NULL,
    updated_at = now()
WHERE id = $1 AND status IN ('available', 'failed', 'removed')
RETURNING *;

-- name: FinishPodcastEpisodeDownload :exec
UPDATE podcast_feed_episodes
SET status        = 'downloaded',
    downloaded_at = now(),
    updated_at    = now()
WHERE id = $1;

-- name: FailPodcastEpisodeDownload :exec
UPDATE podcast_feed_episodes
SET status     = 'failed',
    file_path  = NULL,
    error      = $2,
    updated_at = now()
WHERE id = $1;

-- name: MarkPodcastEpisodeRemoved :exec
-- The file was deleted (retention or by hand). Auto-download skips removed
-- episodes; a manual download brings one back.
UPDATE podcast_feed_episodes
SET status        = 'removed',
    file_path     = NULL,
    media_item_id = NULL,
    updated_at    = now()
WHERE id = $1;

-- name: ResetStalePodcastDownloads :execrows
-- Downloads a crash or restart left claimed: give them back as failed so a
-- retry can claim them.
UPDATE podcast_feed_episodes
SET status     = 'failed',
    file_path  = NULL,
    error      = 'download interrupted',
    updated_at = now()
WHERE status = 'downloading' AND updated_at < $1;

-- name: GetPodcastEpisodeByFilePath :one
-- The scanner's lookup: the episode, and its feed, that a downloaded file
-- belongs to.
SELECT e.id, e.feed_id, e.title, e.description, e.published_at, e.duration_ms,
       e.season, e.episode, e.media_item_id,
       f.library_id, f.title AS feed_title, f.description AS feed_description,
       f.folder AS feed_folder, f.show_item_id
FROM podcast_feed_episodes e
JOIN podcast_feeds f ON f.id = e.feed_id
WHERE e.file_path = $1
LIMIT 1;

-- name: LinkPodcastEpisodeItem :exec
UPDATE podcast_feed_episodes
SET media_item_id = $2
WHERE id = $1 AND media_item_id IS DISTINCT FROM $2;

-- name: GetPodcastFeedByFolder :one
-- The scanner's lookup for a file in a feed's folder that isn't one of its
-- downloads (dropped in by hand): it joins the feed's show.
SELECT id, title, description, show_item_id
FROM podcast_feeds
WHERE folder = $1
ORDER BY created_at, id
LIMIT 1;
