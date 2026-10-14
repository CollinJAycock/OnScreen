-- +goose Up
-- +goose StatementBegin
-- Personal API keys: long-lived credentials for scripts and integrations
-- (Home Assistant, Kometa, cron jobs) that can't run the interactive
-- password + refresh-token flow. A key acts as its user: the auth middleware
-- builds the request's identity from the users row on every request, so the
-- user's library access, rating ceiling and label restriction always apply
-- as they are NOW, never as they were when the key was made.
--
-- Only a SHA-256 of the key is stored (key_hash, unique — the lookup the
-- middleware does per request); the full key is shown once at creation.
-- key_prefix is the first characters of the key ("osk_AbC12xYz") so a user can
-- tell their keys apart in the list. scope 'read_only' refuses every method
-- but GET/HEAD/OPTIONS. last_used_at is approximate: the middleware writes it
-- at most every few minutes per key, never per request. expires_at NULL =
-- no expiry. Deleting the user deletes their keys.
CREATE TABLE public.api_keys (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    name text NOT NULL,
    key_hash text NOT NULL,
    key_prefix text NOT NULL,
    scope text DEFAULT 'full'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_used_at timestamp with time zone,
    expires_at timestamp with time zone,
    CONSTRAINT api_keys_pkey PRIMARY KEY (id),
    CONSTRAINT api_keys_key_hash_key UNIQUE (key_hash),
    CONSTRAINT api_keys_user_id_fkey FOREIGN KEY (user_id)
        REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT api_keys_scope_check
        CHECK (scope = ANY (ARRAY['full'::text, 'read_only'::text]))
);
CREATE INDEX idx_api_keys_user ON public.api_keys USING btree (user_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Ebook reading position, per user and book, synced across devices. locator
-- is opaque to the server (an EPUB CFI, or a page number for CBZ/CBR/PDF);
-- progression is 0..1 through the whole book. client_updated_at is the
-- client's own clock for the position (clamped to the server's clock on
-- write), and a write only replaces the row when it is newer by that clock —
-- so a device flushing a stale position late can't rewind a newer one.
-- Audiobooks keep using watch_progress; this table is for read-at-your-pace
-- formats only.
CREATE TABLE public.reading_progress (
    user_id uuid NOT NULL,
    media_id uuid NOT NULL,
    locator text NOT NULL,
    progression double precision NOT NULL,
    device_name text,
    client_updated_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT reading_progress_pkey PRIMARY KEY (user_id, media_id),
    CONSTRAINT reading_progress_user_id_fkey FOREIGN KEY (user_id)
        REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT reading_progress_media_id_fkey FOREIGN KEY (media_id)
        REFERENCES public.media_items(id) ON DELETE CASCADE,
    CONSTRAINT reading_progress_progression_check
        CHECK (progression >= 0 AND progression <= 1)
);
-- FK cascade from media_items deletes needs a media_id-leading index.
CREATE INDEX idx_reading_progress_media ON public.reading_progress USING btree (media_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-library override of the server-wide automatic subtitle download
-- setting (subtitle_auto_download in server_settings). NULL in either column
-- means "follow the server setting". A library with no row follows it too.
CREATE TABLE public.library_subtitle_auto_download (
    library_id uuid NOT NULL,
    enabled boolean,
    languages text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT library_subtitle_auto_download_pkey PRIMARY KEY (library_id),
    CONSTRAINT library_subtitle_auto_download_library_id_fkey FOREIGN KEY (library_id)
        REFERENCES public.libraries(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
-- One row per (item, language) the automatic subtitle download has tried.
-- status:
--   retry      — nothing suitable found yet; try again at next_attempt_at
--                (1, 7 then 30 days after attempts 1, 2 and 3)
--   downloaded — a subtitle was downloaded; never searched again
--   gave_up    — four searches found nothing; never searched again
--   present    — the file already had a subtitle in that language (embedded,
--                a sidecar file, or an earlier download) when the task looked;
--                no search was made. Looked at again after next_attempt_at
--                (30 days), in case that subtitle has since been removed.
CREATE TABLE public.subtitle_auto_attempts (
    media_id uuid NOT NULL,
    language text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    status text NOT NULL,
    last_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    next_attempt_at timestamp with time zone,
    last_result text,
    CONSTRAINT subtitle_auto_attempts_pkey PRIMARY KEY (media_id, language),
    CONSTRAINT subtitle_auto_attempts_media_id_fkey FOREIGN KEY (media_id)
        REFERENCES public.media_items(id) ON DELETE CASCADE,
    CONSTRAINT subtitle_auto_attempts_status_check
        CHECK (status = ANY (ARRAY['retry'::text, 'present'::text, 'downloaded'::text, 'gave_up'::text]))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.subtitle_auto_attempts;
DROP TABLE IF EXISTS public.library_subtitle_auto_download;
DROP TABLE IF EXISTS public.reading_progress;
DROP TABLE IF EXISTS public.api_keys;
-- +goose StatementEnd
