-- name: CreateArrService :one
INSERT INTO arr_services (
    name, kind, base_url, api_key,
    default_quality_profile_id, default_root_folder, default_tags,
    minimum_availability, series_type, season_folder, language_profile_id,
    is_default, enabled
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: GetArrService :one
SELECT * FROM arr_services WHERE id = $1;

-- name: ListArrServices :many
SELECT * FROM arr_services ORDER BY kind, name;

-- name: ListEnabledArrServicesByKind :many
SELECT * FROM arr_services WHERE kind = $1 AND enabled = TRUE ORDER BY is_default DESC, name;

-- name: GetDefaultArrServiceByKind :one
SELECT * FROM arr_services WHERE kind = $1 AND is_default = TRUE AND enabled = TRUE;

-- name: UpdateArrService :one
UPDATE arr_services
SET name                       = $2,
    base_url                   = $3,
    api_key                    = $4,
    default_quality_profile_id = $5,
    default_root_folder        = $6,
    default_tags               = $7,
    minimum_availability       = $8,
    series_type                = $9,
    season_folder              = $10,
    language_profile_id        = $11,
    is_default                 = $12,
    enabled                    = $13,
    updated_at                 = NOW()
WHERE id = $1
RETURNING *;

-- name: ClearArrServiceDefault :exec
-- Clears is_default on every service of a kind so a new default can be set.
-- Wrap with SetArrServiceDefault in a tx for atomicity.
UPDATE arr_services SET is_default = FALSE, updated_at = NOW()
WHERE kind = $1 AND is_default = TRUE;

-- name: SetArrServiceDefault :exec
UPDATE arr_services SET is_default = TRUE, updated_at = NOW()
WHERE id = $1;

-- name: DeleteArrService :exec
DELETE FROM arr_services WHERE id = $1;
