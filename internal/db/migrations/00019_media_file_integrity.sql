-- +goose Up
-- +goose StatementBegin
-- Deep-decode integrity verdict for video files, written by the opt-in
-- integrity probe (a background job that software-decodes a few frames at
-- several offsets). Catches damaged/fake releases whose container header
-- parses fine but whose bitstream is undecodable past the first seconds —
-- the case the playback path can only discover by failing (hardware
-- decoders emit dead-chroma green frames instead of erroring; see
-- docs/dolby-vision.md's sibling, the Masters-of-the-Universe forensics).
--
--   integrity_status:      'unchecked' (default; probe never ran or verdict
--                          was reset by a content change), 'ok', 'damaged'.
--   integrity_checked_at:  when the probe last ran. NULL while unchecked.
--   integrity_detail:      human-readable reason for a 'damaged' verdict
--                          (offset, frames decoded, first decoder error);
--                          NULL otherwise. Surfaced to admins only.
--
-- A 'damaged' verdict makes the play decision return "damaged" and the
-- transcode-start endpoint refuse with FILE_DAMAGED instead of letting
-- users retry an unplayable title.
ALTER TABLE public.media_files
    ADD COLUMN integrity_status text DEFAULT 'unchecked'::text NOT NULL,
    ADD COLUMN integrity_checked_at timestamp with time zone,
    ADD COLUMN integrity_detail text;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE public.media_files
    ADD CONSTRAINT media_files_integrity_status_check
    CHECK (integrity_status = ANY (ARRAY['unchecked'::text, 'ok'::text, 'damaged'::text]));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.media_files
    DROP CONSTRAINT IF EXISTS media_files_integrity_status_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE public.media_files
    DROP COLUMN IF EXISTS integrity_status,
    DROP COLUMN IF EXISTS integrity_checked_at,
    DROP COLUMN IF EXISTS integrity_detail;
-- +goose StatementEnd
