-- +goose Up
-- People (cast/crew) and per-item credits.
--
-- Populated lazily: when a user first views an item's detail page, we fetch
-- credits from TMDB and persist them here. Subsequent requests serve from DB.
--
-- One row per person (deduped by tmdb_id), one row per credit. A person can
-- appear multiple times on the same item with different roles (e.g. wrote
-- and directed) — `role` plus `job` keep them distinct.
CREATE TABLE people (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tmdb_id        INTEGER UNIQUE,
    name           TEXT NOT NULL,
    profile_path   TEXT,                             -- TMDB-relative; resolved via /artwork/...
    bio            TEXT,
    birthday       DATE,
    deathday       DATE,
    place_of_birth TEXT,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_people_name ON people(LOWER(name));

CREATE TABLE media_credits (
    media_item_id UUID NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    person_id     UUID NOT NULL REFERENCES people(id)      ON DELETE CASCADE,
    role          TEXT NOT NULL,                           -- 'cast' | 'director' | 'writer' | 'producer' | 'creator'
    character     TEXT,                                    -- cast: character name
    job           TEXT NOT NULL DEFAULT '',                -- crew: TMDB job title (e.g. 'Screenplay'); '' for cast
    ord           INTEGER NOT NULL DEFAULT 0,              -- TMDB cast order; 0 for crew
    PRIMARY KEY (media_item_id, person_id, role, job)
);

CREATE INDEX idx_media_credits_person ON media_credits(person_id, role);
CREATE INDEX idx_media_credits_item   ON media_credits(media_item_id, role, ord);

-- +goose Down
DROP TABLE media_credits;
DROP TABLE people;
