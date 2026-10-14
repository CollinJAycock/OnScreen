-- +goose Up
-- +goose StatementBegin
-- Names the files of a movie and stacks multi-part rips.
--
-- edition is the label a file's name gives it: Plex's {edition-Director's Cut}
-- (file or folder), Jellyfin's "Movie (2010) - Director's Cut.mkv" version
-- suffix, or a recognised edition tag after the year ("[Extended]",
-- "Unrated", "IMAX"). NULL when the name gives none. A label only: files are
-- still grouped into one movie by title and year (ADR-031).
--
-- part_index is the 1-based part number of a stacked rip ("Movie (2001)
-- cd1.mkv" + "cd2.mkv", part1/pt1/disc1/disk1/dvd1, -a/-b). Parts of one movie
-- in one folder with the same edition and extension play as one continuous
-- stream. A lone "cd1" keeps its number but plays as an ordinary file: the
-- stack is resolved when the files are read, not here.
--
-- Rows scanned before this migration are filled by the next scan from their
-- file names; no file is re-read.
ALTER TABLE public.media_files ADD COLUMN IF NOT EXISTS edition text;
ALTER TABLE public.media_files ADD COLUMN IF NOT EXISTS part_index smallint;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.media_files DROP COLUMN IF EXISTS part_index;
ALTER TABLE public.media_files DROP COLUMN IF EXISTS edition;
-- +goose StatementEnd
