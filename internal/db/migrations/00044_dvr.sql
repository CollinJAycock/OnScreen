-- +goose Up
-- DVR schema: user-defined recording rules (schedules) and the concrete
-- recording instances they produce. Tuner conflict resolution happens at
-- match time — schedules are just intent; recordings are what we'll
-- actually try to capture.
--
-- Finalized recordings link into media_items (item_id) so the existing
-- player, watch-history, hub, and deletion paths all work with DVR
-- content for free. This is why we chose dedicated DVR library type in
-- the design — when a recording finalizes, the worker creates a
-- media_items row of type 'episode' or 'movie' in that library and
-- stashes the UUID here.

CREATE TABLE schedules (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type              TEXT NOT NULL CHECK (type IN ('once', 'series', 'channel_block')),

    -- For 'once': program_id is the exact EPG row to record.
    -- For 'series': title_match + channel_id; recorder searches EPG for
    --               matching programs each cycle.
    -- For 'channel_block': channel_id + time_start/time_end (local time
    --               per user TZ); records everything in that window.
    program_id        UUID REFERENCES epg_programs(id) ON DELETE SET NULL,
    channel_id        UUID REFERENCES channels(id) ON DELETE CASCADE,
    title_match       TEXT,

    -- Series-mode flag: only record programs with original_air_date >=
    -- NOW() - 7 days (or no original_air_date). Prevents "Seinfeld" rule
    -- from capturing every rerun on every cable channel.
    new_only          BOOLEAN NOT NULL DEFAULT FALSE,

    -- channel_block time-of-day bounds. Stored as "HH:MM" strings (local
    -- time per the user's TZ). Simple and human-editable.
    time_start        TEXT,
    time_end          TEXT,

    -- Padding extends captures to absorb clock drift + program overruns.
    -- 60s pre / 180s post is the Jellyfin/Plex-compatible default:
    -- enough headroom for broadcasters to slip schedule by 2 min without
    -- clipping the finale.
    padding_pre_sec   INT NOT NULL DEFAULT 60,
    padding_post_sec  INT NOT NULL DEFAULT 180,

    -- Conflict priority: higher wins when tuner-count runs short. Also
    -- exposed in the UI as a dropdown (Normal / High / Low -> 50/100/10).
    priority          INT NOT NULL DEFAULT 50,

    -- Auto-delete finalized recordings after this many days. NULL means
    -- "keep forever" — user must manually delete.
    retention_days    INT,

    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_schedules_user ON schedules(user_id);
CREATE INDEX idx_schedules_channel ON schedules(channel_id) WHERE enabled = TRUE;

CREATE TABLE recordings (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_id       UUID REFERENCES schedules(id) ON DELETE SET NULL,
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_id        UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    program_id        UUID REFERENCES epg_programs(id) ON DELETE SET NULL,

    -- Denormalized so the recording row survives program/channel renames
    -- and deletes. Display in the recordings UI comes from these fields
    -- regardless of whether EPG data is still present.
    title             TEXT NOT NULL,
    subtitle          TEXT,
    season_num        INT,
    episode_num       INT,

    status            TEXT NOT NULL CHECK (status IN (
        'scheduled', 'recording', 'completed', 'failed', 'cancelled', 'superseded'
    )),
    -- Times include padding. The worker arms itself to starts_at;
    -- the actual program boundaries are program_id-scoped.
    starts_at         TIMESTAMPTZ NOT NULL,
    ends_at           TIMESTAMPTZ NOT NULL,

    -- Absolute path where the MPEG-TS is being captured, nulled out when
    -- the recording is superseded or cancelled before it starts.
    file_path         TEXT,

    -- Set when status transitions to 'completed' — points at the
    -- media_items row so /watch/{id} plays the recording identically
    -- to any other library item.
    item_id           UUID REFERENCES media_items(id) ON DELETE SET NULL,

    error             TEXT,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Idempotency: one recording per (user, program) pair. Lets the
    -- matcher re-run safely and ensures cancelling then re-scheduling
    -- the same program doesn't create duplicates.
    UNIQUE (user_id, program_id)
);

CREATE INDEX idx_recordings_status ON recordings(status, starts_at);
CREATE INDEX idx_recordings_user ON recordings(user_id, starts_at DESC);
CREATE INDEX idx_recordings_schedule ON recordings(schedule_id) WHERE schedule_id IS NOT NULL;

-- +goose Down
DROP TABLE recordings;
DROP TABLE schedules;
