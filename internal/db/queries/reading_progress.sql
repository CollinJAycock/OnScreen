-- Ebook reading position synced across devices (migration 00050).

-- name: GetReadingProgress :one
SELECT user_id, media_id, locator, progression, device_name,
       client_updated_at, updated_at
FROM reading_progress
WHERE user_id = $1 AND media_id = $2;

-- name: UpsertReadingProgress :execrows
-- Newest position wins, by the client's clock (already clamped to the
-- server's by the handler). A write older than the stored row changes nothing
-- and reports 0 rows, so a phone flushing a queued position after the tablet
-- moved on can't rewind it. Equal timestamps keep the stored row too: the
-- same save sent twice is a no-op.
INSERT INTO reading_progress (user_id, media_id, locator, progression, device_name, client_updated_at, updated_at)
VALUES (@user_id, @media_id, @locator, @progression, sqlc.narg('device_name'), @client_updated_at, now())
ON CONFLICT (user_id, media_id) DO UPDATE
    SET locator           = EXCLUDED.locator,
        progression       = EXCLUDED.progression,
        device_name       = EXCLUDED.device_name,
        client_updated_at = EXCLUDED.client_updated_at,
        updated_at        = now()
    WHERE reading_progress.client_updated_at < EXCLUDED.client_updated_at;

-- name: MarkItemRead :exec
-- Finishing a book records the same manual "watched" mark POST
-- /items/{id}/watched writes, so the library grid's read badge and the
-- watched filter treat a finished book like a finished film. A book already
-- marked, with nothing since, is left alone.
INSERT INTO watch_progress (user_id, media_id, mark_state, marked_at)
VALUES (@user_id, @media_id, 'watched', now())
ON CONFLICT (user_id, media_id) DO UPDATE
    SET mark_state = 'watched',
        marked_at  = now()
    WHERE watch_progress.mark_state IS DISTINCT FROM 'watched'
       OR watch_progress.last_event_at > watch_progress.marked_at;
