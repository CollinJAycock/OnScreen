-- +goose Up
-- +goose StatementBegin
-- Imports of users' watch state, ratings, favorites, playlists and play
-- history from a Plex, Jellyfin or Emby server (internal/serverimport). One
-- row per run, dry runs included. The source's token is never stored: it
-- lives in memory for the run.
--
--   options    which kinds of data the run imports ({"watched": true, ...})
--   mapping    source account -> OnScreen user ([{account_id, account_name, user_id}])
--   phase      what the run is doing now, for the progress display
--   stats      match and write counts (see serverimport.Stats)
--   unmatched  a sample of source items no OnScreen item matched
--   updated_at the run's heartbeat: a running row that stops updating
--              belongs to a server that went away, and is failed so it
--              doesn't block the next import
CREATE TABLE public.import_jobs (
    id          uuid DEFAULT gen_random_uuid() NOT NULL,
    source_kind text NOT NULL,
    source_url  text NOT NULL,
    server_name text,
    status      text DEFAULT 'running' NOT NULL,
    dry_run     boolean DEFAULT false NOT NULL,
    options     jsonb DEFAULT '{}'::jsonb NOT NULL,
    mapping     jsonb DEFAULT '[]'::jsonb NOT NULL,
    phase       text DEFAULT '' NOT NULL,
    stats       jsonb DEFAULT '{}'::jsonb NOT NULL,
    unmatched   jsonb DEFAULT '[]'::jsonb NOT NULL,
    error       text,
    created_by  uuid,
    created_at  timestamp with time zone DEFAULT now() NOT NULL,
    updated_at  timestamp with time zone DEFAULT now() NOT NULL,
    finished_at timestamp with time zone,
    CONSTRAINT import_jobs_pkey PRIMARY KEY (id),
    CONSTRAINT import_jobs_created_by_fkey FOREIGN KEY (created_by)
        REFERENCES public.users(id) ON DELETE SET NULL,
    CONSTRAINT import_jobs_source_kind_check
        CHECK (source_kind = ANY (ARRAY['plex'::text, 'jellyfin'::text, 'emby'::text])),
    CONSTRAINT import_jobs_status_check
        CHECK (status = ANY (ARRAY['running'::text, 'succeeded'::text, 'failed'::text, 'cancelled'::text]))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX import_jobs_created_at_idx ON public.import_jobs (created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- One import at a time: two runs writing the same users' state at once
-- would race each other's "is the local state newer" checks.
CREATE UNIQUE INDEX import_jobs_one_running ON public.import_jobs ((true)) WHERE status = 'running';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.import_jobs;
-- +goose StatementEnd
