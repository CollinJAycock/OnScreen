-- name: GetLibrary :one
SELECT id, name, type, scan_paths, agent, language,
       scan_interval, scan_last_completed_at,
       metadata_refresh_interval, metadata_last_refreshed_at,
       created_at, updated_at, deleted_at, is_private, auto_grant_new_users
FROM libraries
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListLibraries :many
SELECT id, name, type, scan_paths, agent, language,
       scan_interval, scan_last_completed_at,
       metadata_refresh_interval, metadata_last_refreshed_at,
       created_at, updated_at, deleted_at, is_private, auto_grant_new_users
FROM libraries
WHERE deleted_at IS NULL
ORDER BY name;

-- name: CreateLibrary :one
INSERT INTO libraries (name, type, scan_paths, agent, language,
                       scan_interval, metadata_refresh_interval,
                       is_private, auto_grant_new_users)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, name, type, scan_paths, agent, language,
          scan_interval, scan_last_completed_at,
          metadata_refresh_interval, metadata_last_refreshed_at,
          created_at, updated_at, deleted_at, is_private, auto_grant_new_users;

-- name: UpdateLibrary :one
-- is_private uses COALESCE so admins can update other fields without
-- having to re-send the privacy flag — pass NULL to preserve the
-- current value, true/false to flip it.
UPDATE libraries
SET name                      = $2,
    scan_paths                = $3,
    agent                     = $4,
    language                  = $5,
    scan_interval             = $6,
    metadata_refresh_interval = $7,
    is_private                = COALESCE(sqlc.narg('is_private')::bool, is_private),
    auto_grant_new_users      = COALESCE(sqlc.narg('auto_grant_new_users')::bool, auto_grant_new_users),
    updated_at                = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, name, type, scan_paths, agent, language,
          scan_interval, scan_last_completed_at,
          metadata_refresh_interval, metadata_last_refreshed_at,
          created_at, updated_at, deleted_at, is_private, auto_grant_new_users;

-- name: SoftDeleteLibrary :exec
UPDATE libraries SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

-- name: MarkLibraryScanCompleted :exec
UPDATE libraries
SET scan_last_completed_at = NOW(), updated_at = NOW()
WHERE id = $1;

-- name: MarkLibraryMetadataRefreshed :exec
UPDATE libraries
SET metadata_last_refreshed_at = NOW(), updated_at = NOW()
WHERE id = $1;

-- name: ListLibrariesDueForScan :many
SELECT id, name, type, scan_paths, agent, language,
       scan_interval, scan_last_completed_at,
       metadata_refresh_interval, metadata_last_refreshed_at,
       created_at, updated_at, deleted_at, is_private, auto_grant_new_users
FROM libraries
WHERE deleted_at IS NULL
  AND scan_interval IS NOT NULL
  AND (scan_last_completed_at IS NULL
       OR scan_last_completed_at + scan_interval < NOW());

-- name: ListLibrariesDueForMetadataRefresh :many
SELECT id, name, type, scan_paths, agent, language,
       scan_interval, scan_last_completed_at,
       metadata_refresh_interval, metadata_last_refreshed_at,
       created_at, updated_at, deleted_at, is_private, auto_grant_new_users
FROM libraries
WHERE deleted_at IS NULL
  AND metadata_refresh_interval IS NOT NULL
  AND (metadata_last_refreshed_at IS NULL
       OR metadata_last_refreshed_at + metadata_refresh_interval < NOW());

-- name: CountLibraries :one
SELECT COUNT(*) FROM libraries WHERE deleted_at IS NULL;

-- name: IsLibraryAnime :one
-- Targeted single-bool lookup the show-enricher uses to decide
-- whether AniList runs primary or fallback. The library type is the
-- single source of truth — `anime` libraries use AniList primary,
-- everything else uses TMDB primary with AniList as fallback.
SELECT (type = 'anime')::bool FROM libraries WHERE id = $1 AND deleted_at IS NULL;

-- name: IsLibraryManga :one
-- Same shape as IsLibraryAnime but for the book / manga split.
-- Book libraries use whatever book agent ships in v2.x; manga
-- libraries route through AniList for mangaka, demographic, magazine,
-- and reading direction.
SELECT (type = 'manga')::bool FROM libraries WHERE id = $1 AND deleted_at IS NULL;
