-- +goose Up
-- +goose StatementBegin
-- The Dolby Vision profile from a file's DOVI configuration record, NULL when
-- the file has none (or was probed before this column existed).
--
-- hdr_type used to read 'dolby_vision' for any file with that record, and
-- every client refuses those. But most Dolby Vision releases carry a base
-- layer every player can show: profiles 7 and 8.1 sit on HDR10, 8.4 on HLG,
-- and some files carry the record over plain SDR video with no Dolby Vision
-- data at all. The scanner now sets hdr_type from the base layer and keeps
-- 'dolby_vision' only for profile 5, which has no compatible base. The
-- profile is kept here so a client that can show Dolby Vision can later be
-- offered it.
--
-- A row with hdr_type 'dolby_vision' and no dv_profile was classified the
-- old way; the scanner re-reads those files once (see healDynamicRange).
ALTER TABLE public.media_files ADD COLUMN IF NOT EXISTS dv_profile smallint;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.media_files DROP COLUMN IF EXISTS dv_profile;
-- +goose StatementEnd
