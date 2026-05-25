-- +goose Up
-- +goose StatementBegin
-- Per-node configuration, keyed by a stable node identity (the NODE_ID env var,
-- defaulting to the host's name). Unlike server_settings — which every node in a
-- multi-site deployment shares because it replicates — each node reads ONLY its
-- own row here, so these values are logically per-node even though the table
-- itself replicates physically with the rest of the database.
--
-- This lets node/site-specific config that must NOT be the same fleet-wide
-- (bind addresses, filesystem paths, SITE_ID, per-worker hardware toggles, the
-- embedded-worker role) be managed from the admin UI instead of the environment.
-- The irreducible bootstrap set (DATABASE_URL, SECRET_KEY, NODE_ID itself) still
-- has to come from the environment — it's needed before this table is reachable.
CREATE TABLE public.node_settings (
    node_id    text NOT NULL,
    config     jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT node_settings_pkey PRIMARY KEY (node_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.node_settings;
-- +goose StatementEnd
