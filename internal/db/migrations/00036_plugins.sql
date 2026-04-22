-- +goose Up
-- Plugins are external MCP servers OnScreen calls out to. Each plugin
-- registers as exactly one role (notification, metadata, task) and is
-- invoked only in that role.
--
-- transport: only "http" (HTTP+SSE) is supported in v1; the column exists
-- so adding stdio later doesn't require a migration.
--
-- allowed_hosts: jsonb array of hostnames the plugin is permitted to dial
-- when OnScreen forwards its requests through the egress proxy. Empty array
-- means the plugin endpoint itself is the only allowed host (the common case).
CREATE TABLE plugins (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('notification', 'metadata', 'task')),
    transport     TEXT NOT NULL DEFAULT 'http' CHECK (transport IN ('http')),
    endpoint_url  TEXT NOT NULL,
    allowed_hosts JSONB NOT NULL DEFAULT '[]'::jsonb,
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Lookup-by-role is the hot path (Dispatch fans out to all enabled plugins
-- of a given role).
CREATE INDEX plugins_role_enabled ON plugins (role) WHERE enabled = TRUE;

-- +goose Down
DROP TABLE IF EXISTS plugins;
