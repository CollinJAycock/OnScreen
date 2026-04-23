-- +goose Up
-- Phase A of Live TV: tuner devices + their channels + EPG sources + the
-- 14-day rolling program grid. DVR scheduling, recordings, and series rules
-- live in a later migration so this one can ship even if Phase B slips.
--
-- Design choices:
-- * `tuner_devices` rows carry their connection config in a JSONB blob so we
--   can add tuner backends (TVHeadend, etc.) without further migrations.
-- * `channels.tuner_id` cascades on tuner delete — unplugging an HDHomeRun
--   wipes its channels rather than leaving orphan rows that can't be tuned.
-- * `epg_programs` is the "EPG cache" — Schedules Direct caps grids at 14
--   days, and we trim with `ends_at < NOW() - 1 day` on each refresh, so this
--   table stays bounded regardless of how long the source is enabled.
-- * Programs are upserted by (channel_id, source_program_id); the source's
--   ID is the idempotency key so duplicate refreshes are no-ops.

CREATE TABLE tuner_devices (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type            TEXT NOT NULL CHECK (type IN ('hdhomerun', 'm3u')),
    name            TEXT NOT NULL,
    config          JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Concurrent-tune ceiling for the device. HDHomeRun discovery fills this
    -- in (e.g. 4 for HDHR4, 5 for HDHR5); M3U sources are effectively
    -- "infinity" but we store a number so the conflict resolver doesn't have
    -- to special-case the type.
    tune_count      INT NOT NULL DEFAULT 0,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    last_seen_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE channels (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tuner_id        UUID NOT NULL REFERENCES tuner_devices(id) ON DELETE CASCADE,
    -- Channel number as a string ("5", "5.1", "ESPN") because broadcast
    -- channels use dotted virtual numbers and IPTV sources use callsigns.
    number          TEXT NOT NULL,
    callsign        TEXT,
    name            TEXT NOT NULL,
    logo_url        TEXT,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    -- User-overridable sort; defaults to lexical-on-number which is wrong
    -- for "10" vs "2" but right for "5.1" vs "5.2". UI lets users reorder.
    sort_order      INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- A channel number is unique per tuner — the same "5" on two different
    -- HDHomeRuns are different channels even if both are NBC.
    UNIQUE (tuner_id, number)
);

CREATE INDEX idx_channels_tuner ON channels(tuner_id) WHERE enabled = TRUE;

CREATE TABLE epg_sources (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type                    TEXT NOT NULL CHECK (type IN ('schedules_direct', 'xmltv_url', 'xmltv_file')),
    name                    TEXT NOT NULL,
    -- Credentials/URLs/file paths live in JSONB so the same table covers
    -- Schedules Direct (username/password/lineup) and XMLTV (url or path).
    config                  JSONB NOT NULL DEFAULT '{}'::jsonb,
    refresh_interval_min    INT NOT NULL DEFAULT 360,  -- 6 hours
    enabled                 BOOLEAN NOT NULL DEFAULT TRUE,
    last_pull_at            TIMESTAMPTZ,
    last_error              TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE epg_programs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id          UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    -- Source-assigned program identifier. Schedules Direct programs are
    -- "EP012345670012" or "SH012345670000"; XMLTV uses programme/@channel +
    -- start. Used as the upsert key so re-pulling the grid is idempotent.
    source_program_id   TEXT NOT NULL,
    title               TEXT NOT NULL,
    subtitle            TEXT,
    description         TEXT,
    -- Categories are an array because broadcast EPG often tags one program
    -- with multiple ("Movie", "Action", "Sci-Fi"). Keeps a single-value
    -- index out of the way and lets `WHERE 'Sports' = ANY(category)` work.
    category            TEXT[] NOT NULL DEFAULT '{}',
    rating              TEXT,
    season_num          INT,
    episode_num         INT,
    original_air_date   DATE,
    starts_at           TIMESTAMPTZ NOT NULL,
    ends_at             TIMESTAMPTZ NOT NULL,
    -- Raw blob from the source for forward compat (artwork URLs, content
    -- advisories, cast that we don't have columns for yet).
    raw_data            JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (channel_id, source_program_id)
);

-- Guide-grid lookup: "for channel X, what's airing in the next N hours?"
-- The DVR scheduler hits this every minute too, so it's the hot path.
CREATE INDEX idx_epg_programs_channel_starts
    ON epg_programs(channel_id, starts_at);

-- "What's on right now across all channels?" for the channels-page now/next
-- display. Single index supports both sides of the BETWEEN.
CREATE INDEX idx_epg_programs_window
    ON epg_programs(starts_at, ends_at);

-- +goose Down
DROP TABLE epg_programs;
DROP TABLE epg_sources;
DROP TABLE channels;
DROP TABLE tuner_devices;
