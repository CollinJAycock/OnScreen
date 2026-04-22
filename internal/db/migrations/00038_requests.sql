-- +goose Up
-- arr_services holds connection details for each Radarr/Sonarr instance an admin
-- has registered. Multiple instances of the same kind are supported so a
-- separate 4K Radarr (or 1080p vs anime Sonarr) can coexist; users pick which
-- instance fulfills a request, falling back to the per-kind default.
CREATE TABLE arr_services (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                        TEXT NOT NULL,
    kind                        TEXT NOT NULL CHECK (kind IN ('radarr', 'sonarr')),
    base_url                    TEXT NOT NULL,
    api_key                     TEXT NOT NULL,
    -- IDs are upstream-arr concepts (numeric profile/folder selections).
    -- We store the snapshotted defaults; the live list is fetched on demand.
    default_quality_profile_id  INT,
    default_root_folder         TEXT,
    default_tags                JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- Radarr-only
    minimum_availability        TEXT,
    -- Sonarr-only
    series_type                 TEXT,
    season_folder               BOOLEAN,
    language_profile_id         INT,
    -- Marks the per-kind default (used when a request omits service_id).
    -- Enforced by a partial unique index below.
    is_default                  BOOLEAN NOT NULL DEFAULT FALSE,
    enabled                     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX arr_services_kind_default
    ON arr_services (kind)
    WHERE is_default = TRUE;

CREATE INDEX arr_services_kind_enabled
    ON arr_services (kind)
    WHERE enabled = TRUE;

-- media_requests is the user-facing request queue. tmdb_id + type identifies
-- the requested title (TMDB is the canonical metadata source already in use).
-- service_id is set at approval time (admin can override the user's choice).
-- fulfilled_item_id is set when the arr webhook reports the download landed
-- and the scanner has imported it into a library.
--
-- seasons is null = "all seasons" for shows; null for movies. We store as
-- jsonb int array so we can index into it with @> for "is season N pending?"
-- queries when fulfillment lands.
CREATE TABLE media_requests (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type                TEXT NOT NULL CHECK (type IN ('movie', 'show')),
    tmdb_id             INT NOT NULL,
    title               TEXT NOT NULL,
    year                INT,
    poster_url          TEXT,
    overview            TEXT,
    status              TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'approved', 'declined', 'downloading', 'available', 'failed')),
    seasons             JSONB,
    -- User's preferred service / overrides; admin can change at approval time.
    requested_service_id      UUID REFERENCES arr_services(id) ON DELETE SET NULL,
    quality_profile_id        INT,
    root_folder               TEXT,
    -- Resolution after admin action.
    service_id          UUID REFERENCES arr_services(id) ON DELETE SET NULL,
    decline_reason      TEXT,
    decided_by          UUID REFERENCES users(id) ON DELETE SET NULL,
    decided_at          TIMESTAMPTZ,
    -- Set when scanner imports the downloaded file.
    fulfilled_item_id   UUID REFERENCES media_items(id) ON DELETE SET NULL,
    fulfilled_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Avoid duplicate active requests for the same title from the same user.
-- A user can re-request after their previous request was declined or fulfilled.
CREATE UNIQUE INDEX media_requests_unique_active
    ON media_requests (user_id, type, tmdb_id)
    WHERE status IN ('pending', 'approved', 'downloading');

-- Hot paths: list-by-user (paged), list-by-status (admin queue), webhook
-- fulfillment lookup by tmdb_id+type.
CREATE INDEX media_requests_user_created
    ON media_requests (user_id, created_at DESC);

CREATE INDEX media_requests_status_created
    ON media_requests (status, created_at DESC);

CREATE INDEX media_requests_tmdb_lookup
    ON media_requests (type, tmdb_id)
    WHERE status IN ('approved', 'downloading');

-- +goose Down
DROP TABLE IF EXISTS media_requests;
DROP TABLE IF EXISTS arr_services;
