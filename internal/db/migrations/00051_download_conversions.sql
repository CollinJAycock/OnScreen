-- +goose Up
-- +goose StatementBegin
-- Lower-quality ("converted") downloads. A user picks a quality from the
-- transcode ladder (1080p 8 Mbps … 480p 1 Mbps) and the server encodes the
-- file once into a progressive MP4 in the download cache
-- (CACHE_PATH/downloads/<conversion id>.mp4).
--
-- download_conversions is the shared encode: one row per (file, quality)
-- while it is queued, running or ready, so a second user asking for the same
-- file at the same quality reuses the first user's file instead of encoding
-- it again. updated_at doubles as the running encode's heartbeat; a running
-- row whose heartbeat stops (the server died mid-encode) goes back to the
-- queue. expires_at is when a ready file may be evicted; the
-- download_cache_cleanup task evicts expired files and, past the cache size
-- limit, the least recently used ones, marking their rows 'expired'.
CREATE TABLE public.download_conversions (
    id uuid DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
    media_item_id uuid NOT NULL REFERENCES public.media_items(id) ON DELETE CASCADE,
    media_file_id uuid NOT NULL REFERENCES public.media_files(id) ON DELETE CASCADE,
    quality text NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    progress real DEFAULT 0 NOT NULL,
    error text,
    size_bytes bigint,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    started_at timestamp with time zone,
    completed_at timestamp with time zone,
    last_used_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone,
    CONSTRAINT download_conversions_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'ready'::text, 'failed'::text, 'expired'::text]))),
    CONSTRAINT download_conversions_quality_check CHECK ((quality = ANY (ARRAY['1080p_8mbps'::text, '720p_4mbps'::text, '720p_2mbps'::text, '480p_1mbps'::text])))
);

-- At most one live encode per (file, quality): the reuse key. Failed and
-- expired rows fall out of it, so asking again starts a fresh encode.
CREATE UNIQUE INDEX download_conversions_live
    ON public.download_conversions USING btree (media_file_id, quality)
    WHERE (status = ANY (ARRAY['queued'::text, 'running'::text, 'ready'::text]));

-- The runner's work list.
CREATE INDEX download_conversions_queued
    ON public.download_conversions USING btree (created_at)
    WHERE (status = 'queued'::text);

-- download_jobs is one user's request: what they asked for, pointing at the
-- shared encode. Its status is the conversion's; a job whose conversion was
-- cleaned away (conversion_id NULL) reads as 'expired'.
CREATE TABLE public.download_jobs (
    id uuid DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    media_item_id uuid NOT NULL REFERENCES public.media_items(id) ON DELETE CASCADE,
    media_file_id uuid NOT NULL REFERENCES public.media_files(id) ON DELETE CASCADE,
    quality text NOT NULL,
    conversion_id uuid REFERENCES public.download_conversions(id) ON DELETE SET NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT download_jobs_quality_check CHECK ((quality = ANY (ARRAY['1080p_8mbps'::text, '720p_4mbps'::text, '720p_2mbps'::text, '480p_1mbps'::text])))
);

CREATE INDEX download_jobs_user ON public.download_jobs USING btree (user_id, created_at DESC);
CREATE INDEX download_jobs_conversion ON public.download_jobs USING btree (conversion_id);

-- ON DELETE CASCADE lookups from media_items / media_files (a library purge
-- deletes items by the thousand; see 00002_fk_cascade_indexes). The partial
-- reuse index above can't serve the cascade's unqualified media_file_id
-- predicate, hence the plain one.
CREATE INDEX download_conversions_item ON public.download_conversions USING btree (media_item_id);
CREATE INDEX download_conversions_file ON public.download_conversions USING btree (media_file_id);
CREATE INDEX download_jobs_item ON public.download_jobs USING btree (media_item_id);
CREATE INDEX download_jobs_file ON public.download_jobs USING btree (media_file_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Chapter thumbnails, generated with an item's seek-bar sprites into the same
-- trickplay directory (chapter_000.jpg, …), one per chapter of the file the
-- sprites were cut from (trickplay_status.file_id). NULL = not generated yet
-- (items whose sprites predate chapter thumbnails, which the opt-in
-- chapter_thumbnails task backfills); 0 = generated, the file has no chapters.
ALTER TABLE public.trickplay_status
    ADD COLUMN chapter_thumb_count integer;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.trickplay_status
    DROP COLUMN IF EXISTS chapter_thumb_count;
DROP TABLE IF EXISTS public.download_jobs;
DROP TABLE IF EXISTS public.download_conversions;
-- +goose StatementEnd
