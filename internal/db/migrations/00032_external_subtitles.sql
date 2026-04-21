-- +goose Up
-- External subtitles fetched from third-party providers (e.g. OpenSubtitles).
-- Stored as on-disk WebVTT files alongside our other cache artifacts so the
-- player can request them without re-extracting from a media container.
--
-- One row per (file, language, source_id) — providers can offer multiple
-- variants of a language (e.g. SDH vs non-SDH) and we keep them disambiguated.
CREATE TABLE external_subtitles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id         UUID NOT NULL REFERENCES media_files(id) ON DELETE CASCADE,
    language        TEXT NOT NULL,                    -- ISO 639-1 (e.g. "en")
    title           TEXT,                             -- provider-supplied label
    forced          BOOLEAN NOT NULL DEFAULT FALSE,
    sdh             BOOLEAN NOT NULL DEFAULT FALSE,   -- hearing-impaired flag
    source          TEXT NOT NULL,                    -- "opensubtitles" | "user_upload" | ...
    source_id       TEXT,                             -- provider's id for dedup (e.g. OS file_id)
    storage_path    TEXT NOT NULL,                    -- absolute path to .vtt on disk
    rating          REAL,                             -- provider rating (0..10)
    download_count  INT,                              -- provider download_count, for UI sorting
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (file_id, source, source_id)
);

CREATE INDEX idx_external_subtitles_file ON external_subtitles(file_id);
CREATE INDEX idx_external_subtitles_file_lang ON external_subtitles(file_id, language);

-- +goose Down
DROP TABLE external_subtitles;
