-- +goose Up
-- +goose StatementBegin
-- Live Radarr/Sonarr download status for approved requests, written by the
-- arr_request_sync task (internal/requests, SyncDownloads) and by the arr
-- webhook's targeted sync. Until now a request sat at 'downloading' from
-- approval until a file landed, with nothing in between.
--
--   arr_item_id: the Radarr movie id / Sonarr series id the request was
--       added as. Filled from the add response at approval; requests
--       approved before this column existed (or whose add hit "already
--       exists") are resolved lazily by TMDB / TVDB id on their first sync.
--       Only meaningful together with service_id.
--   download_state: the RequestDownload contract the web client renders —
--       searching (monitored, nothing grabbed), queued, downloading,
--       import_pending (downloaded, waiting for the import / library scan),
--       stalled (the download client or *arr reports a problem), failed (the
--       grab or import failed with nothing newer grabbed). NULL = never
--       synced, or cleared because the request became available.
--   download_progress: fraction downloaded, 0..1 (NULL = unknown).
--   download_eta: expected completion (NULL = unknown).
--   download_size_bytes: total size of the grabbed release(s).
--   download_message: the *arr app's own explanation (warning, failure
--       reason), bounded by the sync.
--   download_updated_at: when the sync last evaluated the request. The sync
--       deliberately leaves updated_at alone — that stays the time of the last
--       status change — so it has its own clock; it also orders the sync's
--       round-robin (least recently checked first).
ALTER TABLE public.media_requests
    ADD COLUMN arr_item_id integer,
    ADD COLUMN download_state text,
    ADD COLUMN download_progress real,
    ADD COLUMN download_eta timestamp with time zone,
    ADD COLUMN download_size_bytes bigint,
    ADD COLUMN download_message text,
    ADD COLUMN download_updated_at timestamp with time zone,
    ADD CONSTRAINT media_requests_download_state_check CHECK (download_state = ANY (ARRAY[
        'searching'::text, 'queued'::text, 'downloading'::text,
        'import_pending'::text, 'stalled'::text, 'failed'::text])),
    ADD CONSTRAINT media_requests_download_progress_check CHECK (download_progress >= 0 AND download_progress <= 1),
    ADD CONSTRAINT media_requests_download_size_check CHECK (download_size_bytes >= 0),
    ADD CONSTRAINT media_requests_arr_item_id_check CHECK (arr_item_id > 0);
-- +goose StatementEnd

-- +goose StatementBegin
-- The sync's work list: approved / downloading requests plus recently failed
-- ones (a failed download can recover when the *arr app grabs another
-- release), least recently checked first. Small by construction — only
-- in-flight requests are indexed.
CREATE INDEX media_requests_download_sync
    ON public.media_requests USING btree (download_updated_at NULLS FIRST, created_at)
    WHERE status = ANY (ARRAY['approved'::text, 'downloading'::text, 'failed'::text]);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.media_requests_download_sync;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE public.media_requests
    DROP CONSTRAINT IF EXISTS media_requests_download_state_check,
    DROP CONSTRAINT IF EXISTS media_requests_download_progress_check,
    DROP CONSTRAINT IF EXISTS media_requests_download_size_check,
    DROP CONSTRAINT IF EXISTS media_requests_arr_item_id_check,
    DROP COLUMN IF EXISTS arr_item_id,
    DROP COLUMN IF EXISTS download_state,
    DROP COLUMN IF EXISTS download_progress,
    DROP COLUMN IF EXISTS download_eta,
    DROP COLUMN IF EXISTS download_size_bytes,
    DROP COLUMN IF EXISTS download_message,
    DROP COLUMN IF EXISTS download_updated_at;
-- +goose StatementEnd
