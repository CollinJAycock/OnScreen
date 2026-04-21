-- +goose Up
-- Trickplay seekbar thumbnails. We generate sprite sheets + a WebVTT index
-- per item; this table tracks generation state so we can retry failures and
-- regenerate when the underlying file changes.
--
-- One row per media_item. file_id pins which file the artifacts were built
-- from; if the primary file changes the row is stale and regeneration runs.
CREATE TABLE trickplay_status (
    item_id              UUID PRIMARY KEY REFERENCES media_items(id) ON DELETE CASCADE,
    file_id              UUID REFERENCES media_files(id) ON DELETE SET NULL,
    status               TEXT NOT NULL DEFAULT 'pending'
                           CHECK (status IN ('pending', 'done', 'failed', 'skipped')),
    sprite_count         INT NOT NULL DEFAULT 0,
    interval_sec         INT NOT NULL DEFAULT 10,
    thumb_width          INT NOT NULL DEFAULT 320,
    thumb_height         INT NOT NULL DEFAULT 180,
    grid_cols            INT NOT NULL DEFAULT 10,
    grid_rows            INT NOT NULL DEFAULT 10,
    last_attempted_at    TIMESTAMPTZ,
    last_error           TEXT,
    generated_at         TIMESTAMPTZ
);

CREATE INDEX idx_trickplay_status_status ON trickplay_status(status)
    WHERE status IN ('pending', 'failed');

-- +goose Down
DROP TABLE trickplay_status;
