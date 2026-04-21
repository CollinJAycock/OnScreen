-- +goose Up
-- Intro and credits markers for episodic content (shows/episodes).
-- Movies are excluded by application-layer policy.
--
-- Sources:
--   auto    — detected via audio fingerprint (intro) or blackdetect (credits)
--   manual  — set by an admin in the UI (wins over auto)
--   chapter — lifted from an embedded chapter track at scan time
--
-- Each (media_item_id, kind) pair is unique, so marking replaces the prior
-- value rather than accumulating duplicates.
CREATE TABLE intro_markers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_item_id   UUID NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('intro','credits')),
    start_ms        BIGINT NOT NULL CHECK (start_ms >= 0),
    end_ms          BIGINT NOT NULL CHECK (end_ms > start_ms),
    source          TEXT NOT NULL CHECK (source IN ('auto','manual','chapter')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (media_item_id, kind)
);

CREATE INDEX idx_intro_markers_media ON intro_markers(media_item_id);

-- Default detection mode: on_scan. Admin can switch to off or manual via settings.
INSERT INTO server_settings (key, value)
VALUES ('intro_detection_mode', 'on_scan')
ON CONFLICT (key) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS intro_markers;
DELETE FROM server_settings WHERE key = 'intro_detection_mode';
