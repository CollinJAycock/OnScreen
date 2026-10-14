-- +goose Up
-- +goose StatementBegin
-- Optimized versions ("Optimize…"): an admin asks for a lower-bitrate,
-- universally playable copy of a movie or episode ahead of time, so a weak
-- device or a viewer outside the home network direct-plays it instead of
-- holding a live transcode for the whole film.
--
-- They are encodes of the same kind as converted downloads (migration 00051):
-- one progressive MP4 (H.264 + AAC, moov atom up front) per (file, profile),
-- run by the same queue, runner, encoder choice and transcode-slot
-- accounting. So rather than a second, near-identical table they are
-- download_conversions rows with purpose = 'optimize':
--
--   * their output lives in CACHE_PATH/optimized, not the download cache, and
--     the download cleanup never evicts it — expires_at stays NULL;
--   * they never have download_jobs; an admin manages them directly;
--   * they go when the source file goes (the media_file_id cascade), when the
--     source is replaced in place (source_file_hash no longer matches the
--     file's hash — the cleanup task removes them, and playback never offers
--     one in the meantime), or when an admin deletes them.
--
-- width / height / bitrate_kbps / duration_ms describe the finished file (set
-- when it is ready) so the play decision can judge it without probing.
ALTER TABLE public.download_conversions
    ADD COLUMN purpose text DEFAULT 'download'::text NOT NULL,
    ADD COLUMN requested_by uuid REFERENCES public.users(id) ON DELETE SET NULL,
    ADD COLUMN source_file_hash text,
    ADD COLUMN width integer,
    ADD COLUMN height integer,
    ADD COLUMN bitrate_kbps integer,
    ADD COLUMN duration_ms bigint;

ALTER TABLE public.download_conversions
    ADD CONSTRAINT download_conversions_purpose_check
        CHECK ((purpose = ANY (ARRAY['download'::text, 'optimize'::text])));

-- Each purpose has its own quality vocabulary: the download ladder rungs, or
-- the three optimize profiles.
ALTER TABLE public.download_conversions
    DROP CONSTRAINT download_conversions_quality_check;
ALTER TABLE public.download_conversions
    ADD CONSTRAINT download_conversions_quality_check CHECK ((
        (purpose = 'download'::text AND quality = ANY (ARRAY['1080p_8mbps'::text, '720p_4mbps'::text, '720p_2mbps'::text, '480p_1mbps'::text]))
        OR
        (purpose = 'optimize'::text AND quality = ANY (ARRAY['mobile_720p'::text, 'tv_1080p'::text, 'original_compatible'::text]))
    ));

-- The admin's list of optimized versions and jobs.
CREATE INDEX download_conversions_optimize
    ON public.download_conversions USING btree (created_at DESC)
    WHERE (purpose = 'optimize'::text);

-- ON DELETE SET NULL lookup from users (see 00002_fk_cascade_indexes).
CREATE INDEX download_conversions_requested_by
    ON public.download_conversions USING btree (requested_by);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM public.download_conversions WHERE purpose = 'optimize';
DROP INDEX IF EXISTS public.download_conversions_requested_by;
DROP INDEX IF EXISTS public.download_conversions_optimize;
ALTER TABLE public.download_conversions
    DROP CONSTRAINT IF EXISTS download_conversions_quality_check;
ALTER TABLE public.download_conversions
    ADD CONSTRAINT download_conversions_quality_check
        CHECK ((quality = ANY (ARRAY['1080p_8mbps'::text, '720p_4mbps'::text, '720p_2mbps'::text, '480p_1mbps'::text])));
ALTER TABLE public.download_conversions
    DROP CONSTRAINT IF EXISTS download_conversions_purpose_check,
    DROP COLUMN IF EXISTS duration_ms,
    DROP COLUMN IF EXISTS bitrate_kbps,
    DROP COLUMN IF EXISTS height,
    DROP COLUMN IF EXISTS width,
    DROP COLUMN IF EXISTS source_file_hash,
    DROP COLUMN IF EXISTS requested_by,
    DROP COLUMN IF EXISTS purpose;
-- +goose StatementEnd
