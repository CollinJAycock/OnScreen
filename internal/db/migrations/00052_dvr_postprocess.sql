-- +goose Up
-- +goose StatementBegin
-- DVR post-processing and commercial detection.
--
-- dvr_postprocess_jobs is the queue AND the per-recording status the
-- recordings page shows. One row per recording (re-queueing reuses it); no
-- row means post-processing never ran, which is every recording made before
-- this migration and every recording made while post-processing is off.
--
--   kind    'full'   = the whole pipeline (remux / transcode, commercial
--                      detection, cut, script) queued when a capture finishes
--           'detect' = commercial detection only (the "Detect commercials"
--                      admin action); never rewrites the file
--   status  queued -> processing -> processed | failed
--
-- original_file_path is set when the capture was kept after processing
-- (delete-original off): it was moved somewhere the scanner skips, and the
-- retention purge removes it together with the processed file.
CREATE TABLE public.dvr_postprocess_jobs (
    recording_id       uuid PRIMARY KEY REFERENCES public.recordings(id) ON DELETE CASCADE,
    kind               text NOT NULL,
    status             text NOT NULL,
    message            text,
    original_file_path text,
    queued_at          timestamp with time zone DEFAULT now() NOT NULL,
    updated_at         timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT dvr_postprocess_jobs_kind_check CHECK (kind = ANY (ARRAY['full'::text, 'detect'::text])),
    CONSTRAINT dvr_postprocess_jobs_status_check CHECK (status = ANY (ARRAY['queued'::text, 'processing'::text, 'processed'::text, 'failed'::text]))
);

-- The worker claims the oldest queued job; the partial index keeps that a
-- tiny lookup however many finished jobs pile up.
CREATE INDEX idx_dvr_postprocess_jobs_queued ON public.dvr_postprocess_jobs USING btree (queued_at)
    WHERE status = 'queued';

-- Commercial breaks found in a recording. Keyed on the media FILE, not the
-- item: DVR items are found-or-created by title, so a daily news show's
-- recordings can share one item with a file per airing, and each airing has
-- its own breaks. Re-pointing a file to a processed copy keeps its id, so
-- the breaks follow it. Kept apart from intro_markers on purpose: that table
-- allows one row per (item, kind), and first-party clients render every
-- intro_markers row as "Skip intro" / "Skip credits" without checking kind.
CREATE TABLE public.commercial_markers (
    id            uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    media_file_id uuid NOT NULL REFERENCES public.media_files(id) ON DELETE CASCADE,
    start_ms      bigint NOT NULL,
    end_ms        bigint NOT NULL,
    source        text NOT NULL,
    created_at    timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT commercial_markers_start_check CHECK (start_ms >= 0),
    CONSTRAINT commercial_markers_range_check CHECK (end_ms > start_ms),
    CONSTRAINT commercial_markers_source_check CHECK (source = ANY (ARRAY['comskip'::text, 'heuristic'::text]))
);

CREATE INDEX idx_commercial_markers_file ON public.commercial_markers USING btree (media_file_id, start_ms);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.commercial_markers;
DROP TABLE IF EXISTS public.dvr_postprocess_jobs;
-- +goose StatementEnd
