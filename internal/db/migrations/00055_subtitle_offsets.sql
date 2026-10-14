-- +goose Up
-- +goose StatementBegin
-- Server-side subtitle timing offsets.
--
-- An offset corrects a mistimed subtitle for everyone who plays it: it is a
-- fix for the file, not a viewer preference (the web player keeps its own
-- session-only adjustment for that). Milliseconds, positive = cues appear
-- later, within ±10 minutes. The WebVTT endpoints shift every cue by it when
-- they serve the track, so clients get corrected timing without knowing the
-- offset exists.
--
-- External subtitles (downloaded, OCR'd) carry it on their own row.
ALTER TABLE public.external_subtitles
    ADD COLUMN offset_ms integer DEFAULT 0 NOT NULL,
    ADD CONSTRAINT external_subtitles_offset_ms_check
        CHECK (offset_ms BETWEEN -600000 AND 600000);

-- Embedded subtitle streams have no row of their own, so their offsets live
-- here, keyed by the media file and the stream's ABSOLUTE ffprobe index (the
-- index /media/subtitles/{file}/{index} takes). Sparse: no row means no
-- offset, and setting an offset back to 0 deletes the row. Goes with the
-- file when the file row is deleted.
CREATE TABLE public.subtitle_stream_offsets (
    file_id      uuid NOT NULL REFERENCES public.media_files(id) ON DELETE CASCADE,
    stream_index integer NOT NULL,
    offset_ms    integer NOT NULL,
    updated_at   timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (file_id, stream_index),
    CONSTRAINT subtitle_stream_offsets_stream_index_check CHECK (stream_index >= 0),
    CONSTRAINT subtitle_stream_offsets_offset_ms_check
        CHECK (offset_ms BETWEEN -600000 AND 600000 AND offset_ms <> 0)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.subtitle_stream_offsets;
ALTER TABLE public.external_subtitles DROP COLUMN IF EXISTS offset_ms;
-- +goose StatementEnd
