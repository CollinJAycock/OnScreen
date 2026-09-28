-- User-reported item problems ("Report a problem") and the admin Library
-- health page. Table: migration 00026_media_issues.sql.

-- name: CreateMediaIssue :one
-- The partial unique index uq_media_issues_open_user_item_kind refuses a
-- second OPEN report of the same kind by the same user on the same item
-- (23505); the handler maps that to 409.
INSERT INTO media_issues (item_id, file_id, user_id, kind, note)
VALUES (sqlc.arg('item_id'), sqlc.narg('file_id'), sqlc.arg('user_id'), sqlc.arg('kind'), sqlc.narg('note'))
RETURNING *;

-- name: GetMediaIssue :one
SELECT * FROM media_issues WHERE id = $1;

-- name: CountOpenMediaIssuesForUser :one
-- Backs the per-user cap on open reports.
SELECT count(*)::int AS open_count
FROM media_issues
WHERE user_id = $1 AND status = 'open';

-- name: GetOpenMediaIssueForUserItemKind :one
-- The caller's existing open report of this kind on this item, if any.
SELECT * FROM media_issues
WHERE user_id = $1 AND item_id = $2 AND kind = $3 AND status = 'open';

-- name: ListMediaIssuesForUserItem :many
-- The caller's own reports on one item, newest first, so the dialog can say
-- "You reported this 2 days ago" (and how an admin closed an older one).
SELECT * FROM media_issues
WHERE user_id = $1 AND item_id = $2
ORDER BY created_at DESC
LIMIT 20;

-- name: ListMediaIssuesAdmin :many
-- Admin Library health queue. The item's parent / grandparent give episodes
-- and seasons their show context; poster falls back up the hierarchy so an
-- episode row still gets artwork. Items soft-deleted since the report are
-- left out (their reports are removed when the item is purged).
SELECT mi2.id, mi2.item_id, mi2.file_id, mi2.user_id, mi2.kind, mi2.note, mi2.status,
       mi2.created_at, mi2.resolved_at, mi2.resolved_by, mi2.resolution_note,
       m.library_id, m.type AS item_type, m.title AS item_title, m.year AS item_year,
       m.index AS item_index,
       COALESCE(m.poster_path, p.poster_path, gp.poster_path) AS poster_path,
       p.title AS parent_title, p.index AS parent_index, gp.title AS grandparent_title,
       u.username AS reporter_username,
       rb.username AS resolved_by_username,
       mf.file_path AS file_path
FROM media_issues mi2
JOIN media_items m ON m.id = mi2.item_id
JOIN users u ON u.id = mi2.user_id
LEFT JOIN media_items p ON p.id = m.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
LEFT JOIN users rb ON rb.id = mi2.resolved_by
LEFT JOIN media_files mf ON mf.id = mi2.file_id
WHERE m.deleted_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR mi2.status = sqlc.narg('status')::text)
ORDER BY mi2.created_at DESC, mi2.id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountMediaIssuesAdmin :one
-- Total for ListMediaIssuesAdmin's filter, for paging.
SELECT count(*)::int AS total
FROM media_issues mi2
JOIN media_items m ON m.id = mi2.item_id
WHERE m.deleted_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR mi2.status = sqlc.narg('status')::text);

-- name: CloseMediaIssue :one
-- Resolves or dismisses an OPEN report. No row = the report doesn't exist or
-- was already closed (the handler tells the two apart with GetMediaIssue).
UPDATE media_issues
SET status          = sqlc.arg('status'),
    resolved_at     = NOW(),
    resolved_by     = sqlc.narg('resolved_by'),
    resolution_note = sqlc.narg('resolution_note')
WHERE id = sqlc.arg('id') AND status = 'open'
RETURNING *;

-- name: ListDamagedMediaFiles :many
-- Active files the integrity probe marked damaged, most recently checked
-- first, with the owning item's show context for display.
SELECT mf.id AS file_id, mf.media_item_id AS item_id, mf.file_path,
       mf.integrity_detail, mf.integrity_checked_at,
       m.library_id, m.type AS item_type, m.title AS item_title, m.year AS item_year,
       m.index AS item_index,
       p.title AS parent_title, p.index AS parent_index, gp.title AS grandparent_title
FROM media_files mf
JOIN media_items m ON m.id = mf.media_item_id
LEFT JOIN media_items p ON p.id = m.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
WHERE mf.integrity_status = 'damaged'
  AND mf.status = 'active'
  AND m.deleted_at IS NULL
ORDER BY mf.integrity_checked_at DESC NULLS LAST, mf.id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountDamagedMediaFiles :one
-- Same filter as ListDamagedMediaFiles.
SELECT count(*)::int AS damaged_count
FROM media_files mf
JOIN media_items m ON m.id = mf.media_item_id
WHERE mf.integrity_status = 'damaged'
  AND mf.status = 'active'
  AND m.deleted_at IS NULL;
