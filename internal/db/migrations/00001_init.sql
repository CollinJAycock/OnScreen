-- +goose Up
-- +goose StatementBegin

-- ── Extensions ────────────────────────────────────────────────────────────────
CREATE EXTENSION IF NOT EXISTS "pgcrypto";   -- gen_random_uuid()
-- pgvector is optional (Phase 5); skip if not installed
DO $$ BEGIN
  CREATE EXTENSION IF NOT EXISTS "vector";
EXCEPTION WHEN OTHERS THEN
  RAISE NOTICE 'pgvector not available, skipping';
END $$;

-- ── Libraries ─────────────────────────────────────────────────────────────────
CREATE TABLE libraries (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('movie', 'show', 'music', 'photo')),
    scan_paths  TEXT[] NOT NULL,
    agent       TEXT NOT NULL DEFAULT 'tmdb',
    language    TEXT NOT NULL DEFAULT 'en',
    -- Scheduling (NULL = disabled; managed via admin UI)
    scan_interval               INTERVAL NOT NULL DEFAULT '1 day',
    scan_last_completed_at      TIMESTAMPTZ,
    metadata_refresh_interval   INTERVAL NOT NULL DEFAULT '7 days',
    metadata_last_refreshed_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

-- ── Media Items ───────────────────────────────────────────────────────────────
CREATE TABLE media_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id      UUID NOT NULL REFERENCES libraries(id),
    type            TEXT NOT NULL CHECK (type IN ('movie', 'show', 'season', 'episode', 'track', 'album', 'artist')),
    title           TEXT NOT NULL,
    sort_title      TEXT NOT NULL,
    original_title  TEXT,
    year            INT,
    summary         TEXT,
    tagline         TEXT,
    rating          NUMERIC(3,1),
    audience_rating NUMERIC(3,1),
    content_rating  TEXT,
    duration_ms     BIGINT,
    genres          TEXT[],
    tags            TEXT[],
    -- External agent IDs
    tmdb_id         INT,
    tvdb_id         INT,
    imdb_id         TEXT,
    musicbrainz_id  UUID,
    -- Hierarchy (season/episode/track relationship)
    parent_id       UUID REFERENCES media_items(id),
    index           INT,    -- season number, episode number, or track number
    -- Full-text search (generated, always up-to-date)
    search_vector   TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('english',
            COALESCE(title, '') || ' ' ||
            COALESCE(original_title, '') || ' ' ||
            COALESCE(summary, ''))
    ) STORED,
    -- Embedding (reserved for Phase 5 — added via ALTER TABLE when pgvector is available)
    -- Artwork (relative paths from MEDIA_PATH; NULL until metadata agent runs)
    poster_path     TEXT,   -- e.g. "Movie Title (2020)/poster.jpg"
    fanart_path     TEXT,   -- e.g. "Movie Title (2020)/fanart.jpg"
    thumb_path      TEXT,   -- episode/track thumbnail
    -- Timestamps
    originally_available_at DATE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX idx_media_items_library    ON media_items(library_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_media_items_parent     ON media_items(parent_id)  WHERE deleted_at IS NULL;
CREATE INDEX idx_media_items_tmdb       ON media_items(tmdb_id)    WHERE tmdb_id IS NOT NULL;
CREATE INDEX idx_media_items_search     ON media_items USING gin(search_vector);
-- Phase 5: CREATE INDEX idx_media_items_embedding ON media_items USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- ── Media Files ───────────────────────────────────────────────────────────────
CREATE TABLE media_files (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_item_id    UUID NOT NULL REFERENCES media_items(id),
    file_path        TEXT NOT NULL UNIQUE,
    file_size        BIGINT NOT NULL,
    container        TEXT,           -- mkv, mp4, avi
    video_codec      TEXT,           -- h264, hevc, av1
    audio_codec      TEXT,           -- aac, ac3, eac3, truehd
    resolution_w     INT,
    resolution_h     INT,
    bitrate          BIGINT,
    hdr_type         TEXT,           -- sdr, hdr10, hdr10plus, dolby_vision, hlg
    frame_rate       NUMERIC(6,3),
    audio_streams    JSONB,          -- [{codec, channels, language, title}]
    subtitle_streams JSONB,          -- [{codec, language, title, forced}]
    chapters         JSONB,          -- [{title, start_ms, end_ms}]
    -- SHA-256; only recomputed when mtime or size changes (ADR-011)
    file_hash        TEXT,
    -- Three-state file lifecycle (ADR-011)
    status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'missing', 'deleted')),
    missing_since    TIMESTAMPTZ,    -- set when status→'missing'; cleared on restore
    scanned_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_media_files_item    ON media_files(media_item_id);
CREATE INDEX idx_media_files_hash    ON media_files(file_hash) WHERE file_hash IS NOT NULL;
CREATE INDEX idx_media_files_missing ON media_files(missing_since) WHERE status = 'missing';

-- ── Users ─────────────────────────────────────────────────────────────────────
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username        TEXT NOT NULL UNIQUE,
    email           TEXT UNIQUE,
    password_hash   TEXT,           -- NULL for Plex-only accounts
    is_admin        BOOLEAN NOT NULL DEFAULT false,
    pin             TEXT,           -- 4-digit restricted profile PIN (bcrypt cost 12)
    -- Plex account linking (ADR-003)
    plex_id         BIGINT UNIQUE,
    plex_token      TEXT,           -- AES-256-GCM encrypted at rest
    plex_username   TEXT,
    plex_token_validated_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Sessions ──────────────────────────────────────────────────────────────────
-- Refresh token store. Access tokens (Paseto) are stateless and not stored.
-- token_hash = SHA-256(raw_refresh_token). Raw token is never persisted.
CREATE TABLE sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,  -- SHA-256 of the raw refresh token
    client_id    TEXT,
    client_name  TEXT,
    device_id    TEXT,
    platform     TEXT,
    ip_addr      INET,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL,
    last_seen    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sessions_user    ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);  -- for cleanup job

-- ── Watch Events (immutable event log, partitioned by month) ─────────────────
-- Partitions are created by the partition worker (ADR-002), not pg_partman.
CREATE TABLE watch_events (
    id          UUID NOT NULL DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    media_id    UUID NOT NULL REFERENCES media_items(id),
    file_id     UUID REFERENCES media_files(id),
    session_id  UUID,           -- groups events from one playback session
    event_type  TEXT NOT NULL CHECK (event_type IN ('play', 'pause', 'resume', 'stop', 'seek', 'scrobble')),
    position_ms BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT,
    client_id   TEXT,
    client_name TEXT,
    client_ip   INET,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, occurred_at)   -- partition key must be in PK (PG constraint)
) PARTITION BY RANGE (occurred_at);

CREATE INDEX idx_watch_events_user_media ON watch_events(user_id, media_id, occurred_at DESC);
CREATE INDEX idx_watch_events_session    ON watch_events(session_id) WHERE session_id IS NOT NULL;

-- ── Watch State (derived materialized view — never written directly) ──────────
CREATE MATERIALIZED VIEW watch_state AS
SELECT DISTINCT ON (user_id, media_id)
    user_id,
    media_id,
    position_ms,
    duration_ms,
    CASE
        WHEN position_ms::float / NULLIF(duration_ms, 0) > 0.9 THEN 'watched'
        WHEN position_ms > 0                                    THEN 'in_progress'
        ELSE                                                         'unwatched'
    END AS status,
    occurred_at AS last_watched_at
FROM watch_events
WHERE event_type IN ('stop', 'scrobble')
ORDER BY user_id, media_id, occurred_at DESC;

CREATE UNIQUE INDEX ON watch_state(user_id, media_id);
CREATE INDEX        ON watch_state(user_id, status);

-- ── Hub Cache (materialized views, refreshed CONCURRENTLY) ────────────────────
CREATE MATERIALIZED VIEW hub_recently_added AS
SELECT
    l.id    AS library_id,
    m.id    AS media_id,
    m.type,
    m.title,
    m.year,
    m.rating,
    m.poster_path,
    m.created_at
FROM media_items m
JOIN libraries l ON l.id = m.library_id
WHERE m.deleted_at IS NULL
  AND m.type IN ('movie', 'show')
ORDER BY m.created_at DESC;

CREATE UNIQUE INDEX ON hub_recently_added(library_id, media_id);
CREATE INDEX        ON hub_recently_added(library_id, created_at DESC);

-- ── Webhook Endpoints & Failures ──────────────────────────────────────────────
CREATE TABLE webhook_endpoints (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    url         TEXT NOT NULL,
    secret      TEXT,               -- HMAC-SHA256 signing secret (AES-256-GCM encrypted)
    events      TEXT[] NOT NULL,    -- ['play', 'stop', 'pause', 'resume', 'scrobble']
    enabled     BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Populated when all 3 delivery attempts fail (ADR-009).
-- Operators can inspect + replay via admin UI.
CREATE TABLE webhook_failures (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint_id     UUID NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
    url             TEXT NOT NULL,
    payload         JSONB NOT NULL,
    last_error      TEXT NOT NULL,
    attempt_count   INT NOT NULL DEFAULT 3,
    failed_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhook_failures_endpoint ON webhook_failures(endpoint_id);
CREATE INDEX idx_webhook_failures_failed   ON webhook_failures(failed_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_webhook_failures_failed;
DROP INDEX IF EXISTS idx_webhook_failures_endpoint;
DROP TABLE IF EXISTS webhook_failures;
DROP TABLE IF EXISTS webhook_endpoints;
DROP MATERIALIZED VIEW IF EXISTS hub_recently_added;
DROP MATERIALIZED VIEW IF EXISTS watch_state;
DROP TABLE IF EXISTS watch_events;
DROP INDEX IF EXISTS idx_sessions_expires;
DROP INDEX IF EXISTS idx_sessions_user;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
DROP INDEX IF EXISTS idx_media_files_missing;
DROP INDEX IF EXISTS idx_media_files_hash;
DROP INDEX IF EXISTS idx_media_files_item;
DROP TABLE IF EXISTS media_files;
DROP INDEX IF EXISTS idx_media_items_search;
DROP INDEX IF EXISTS idx_media_items_tmdb;
DROP INDEX IF EXISTS idx_media_items_parent;
DROP INDEX IF EXISTS idx_media_items_library;
DROP TABLE IF EXISTS media_items;
DROP TABLE IF EXISTS libraries;

-- +goose StatementEnd
