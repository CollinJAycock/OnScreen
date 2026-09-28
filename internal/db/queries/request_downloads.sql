-- name: ListMediaRequestsForDownloadSync :many
-- Live Radarr/Sonarr download status (migration 00025), written by
-- requests.Service.SyncDownloads. The sync's work list: every approved /
-- downloading request routed to an arr service, plus failed ones that failed
-- after @failed_since (a failed download recovers if the *arr app grabs
-- another release; past the window the request is left for an admin). Least
-- recently checked first, so when a run's per-service lookup budget runs out
-- the skipped rows go first next time. Served by media_requests_download_sync.
SELECT * FROM media_requests
WHERE service_id IS NOT NULL
  AND status IN ('approved', 'downloading', 'failed')
  AND (status <> 'failed' OR updated_at > @failed_since::timestamptz)
ORDER BY download_updated_at ASC NULLS FIRST, created_at ASC
LIMIT @max_rows;

-- name: SetMediaRequestArrItem :exec
-- Links a request to the Radarr movie / Sonarr series it was added as.
UPDATE media_requests
SET arr_item_id = @arr_item_id
WHERE id = @id;

-- name: UpdateMediaRequestDownload :execrows
-- Records the sync's latest view of an in-flight request without changing
-- its status. Rows that became available / were declined meanwhile are left
-- alone (0 rows). updated_at is deliberately untouched — it stays "when the
-- request last changed status" (only FailMediaRequestDownload and
-- RecoverMediaRequestDownload move it); download_updated_at is the sync's
-- own clock.
UPDATE media_requests
SET download_state      = @download_state,
    download_progress   = @download_progress,
    download_eta        = @download_eta,
    download_size_bytes = @download_size_bytes,
    download_message    = @download_message,
    download_updated_at = NOW()
WHERE id = @id
  AND status IN ('approved', 'downloading', 'failed');

-- name: FailMediaRequestDownload :execrows
-- The transition INTO failed. Conditional on the row still being in flight,
-- so of any number of concurrent syncs that observe the same failure exactly
-- one gets rows=1 — and only that one notifies the requester and admins.
UPDATE media_requests
SET status              = 'failed',
    download_state      = 'failed',
    download_progress   = @download_progress,
    download_eta        = NULL,
    download_size_bytes = @download_size_bytes,
    download_message    = @download_message,
    download_updated_at = NOW(),
    updated_at          = NOW()
WHERE id = @id
  AND status IN ('approved', 'downloading');

-- name: RecoverMediaRequestDownload :execrows
-- A failed request whose title the *arr app grabbed or imported again goes
-- back to downloading. Skipped (0 rows) when the requester has since opened a
-- new active request for the same title — for a show, one whose seasons
-- overlap this one's (NULL seasons = every season, overlapping everything;
-- migration 00028): two such active rows would break
-- media_requests_unique_active or the service's overlap rule, and the new
-- request supersedes this one.
UPDATE media_requests AS mr
SET status              = 'downloading',
    download_state      = @download_state,
    download_progress   = @download_progress,
    download_eta        = @download_eta,
    download_size_bytes = @download_size_bytes,
    download_message    = @download_message,
    download_updated_at = NOW(),
    updated_at          = NOW()
WHERE mr.id = @id
  AND mr.status = 'failed'
  AND NOT EXISTS (
      SELECT 1 FROM media_requests o
      WHERE o.user_id = mr.user_id
        AND o.type = mr.type
        AND o.tmdb_id = mr.tmdb_id
        AND o.id <> mr.id
        AND o.status IN ('pending', 'approved', 'downloading')
        AND (o.type = 'movie'
             OR o.seasons IS NULL
             OR mr.seasons IS NULL
             OR EXISTS (
                 SELECT 1 FROM jsonb_array_elements(o.seasons) AS os(season)
                 WHERE mr.seasons @> jsonb_build_array(os.season)
             ))
  );

-- name: ListUsernamesByIDs :many
-- Requester names for a page of requests (the admin queue shows who asked).
SELECT id, username FROM users
WHERE id = ANY(@ids::uuid[]);
