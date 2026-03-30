-- +goose Up
CREATE TABLE collections (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users(id) ON DELETE CASCADE,  -- NULL = system/auto collection
    name        TEXT NOT NULL,
    description TEXT,
    type        TEXT NOT NULL CHECK (type IN ('auto_genre', 'playlist')),
    genre       TEXT,           -- for auto_genre collections
    poster_path TEXT,
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_collections_user ON collections(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_collections_type ON collections(type);

CREATE TABLE collection_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    collection_id UUID NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    media_item_id UUID NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    position      INT NOT NULL DEFAULT 0,
    added_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(collection_id, media_item_id)
);

CREATE INDEX idx_collection_items_collection ON collection_items(collection_id);

-- +goose Down
DROP TABLE IF EXISTS collection_items;
DROP TABLE IF EXISTS collections;
