-- name: GetLibrary :one
SELECT id, name, type, scan_paths, agent, language,
       scan_interval, scan_last_completed_at,
       metadata_refresh_interval, metadata_last_refreshed_at,
       created_at, updated_at, deleted_at
FROM libraries
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListLibraries :many
SELECT id, name, type, scan_paths, agent, language,
       scan_interval, scan_last_completed_at,
       metadata_refresh_interval, metadata_last_refreshed_at,
       created_at, updated_at, deleted_at
FROM libraries
WHERE deleted_at IS NULL
ORDER BY name;

-- name: CreateLibrary :one
INSERT INTO libraries (name, type, scan_paths, agent, language,
                       scan_interval, metadata_refresh_interval)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, name, type, scan_paths, agent, language,
          scan_interval, scan_last_completed_at,
          metadata_refresh_interval, metadata_last_refreshed_at,
          created_at, updated_at, deleted_at;

-- name: UpdateLibrary :one
UPDATE libraries
SET name                      = $2,
    scan_paths                = $3,
    agent                     = $4,
    language                  = $5,
    scan_interval             = $6,
    metadata_refresh_interval = $7,
    updated_at                = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, name, type, scan_paths, agent, language,
          scan_interval, scan_last_completed_at,
          metadata_refresh_interval, metadata_last_refreshed_at,
          created_at, updated_at, deleted_at;

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
       created_at, updated_at, deleted_at
FROM libraries
WHERE deleted_at IS NULL
  AND scan_interval IS NOT NULL
  AND (scan_last_completed_at IS NULL
       OR scan_last_completed_at + scan_interval < NOW());

-- name: ListLibrariesDueForMetadataRefresh :many
SELECT id, name, type, scan_paths, agent, language,
       scan_interval, scan_last_completed_at,
       metadata_refresh_interval, metadata_last_refreshed_at,
       created_at, updated_at, deleted_at
FROM libraries
WHERE deleted_at IS NULL
  AND metadata_refresh_interval IS NOT NULL
  AND (metadata_last_refreshed_at IS NULL
       OR metadata_last_refreshed_at + metadata_refresh_interval < NOW());

-- name: CountLibraries :one
SELECT COUNT(*) FROM libraries WHERE deleted_at IS NULL;
