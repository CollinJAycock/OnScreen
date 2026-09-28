-- +goose Up
-- +goose StatementBegin
-- Disc number of a music track within its album. Until now only the album's
-- disc_total was kept, so a track's position was `index` alone: album tracks
-- were ordered by track number, and the scanner matched an incoming file to
-- an existing sibling by that number, which folded disc 2 track 1 into disc 1
-- track 1 on every multi-disc album. Tracks now carry the disc too; children
-- are ordered by (disc, track) and sibling matching keys on both.
--
-- NULL means the file's tags carried no disc number (or the row predates this
-- column); ordering and matching read it as disc 1. Only tracks set it.
ALTER TABLE public.media_items
    ADD COLUMN disc_number integer,
    ADD CONSTRAINT media_items_disc_number_positive CHECK (disc_number > 0);
-- +goose StatementEnd

-- +goose StatementBegin
-- One item per position under a parent, where a position now includes the
-- disc. The old (parent_id, type, index) key refused disc 2 track 1 outright
-- once disc 1 track 1 existed. Seasons and episodes have no disc, so for them
-- the key is unchanged.
DROP INDEX IF EXISTS public.idx_media_items_parent_type_index;
CREATE UNIQUE INDEX idx_media_items_parent_type_index
    ON public.media_items USING btree (parent_id, type, COALESCE(disc_number, 1), index)
    WHERE parent_id IS NOT NULL AND index IS NOT NULL AND deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Restoring the disc-less key fails while any album holds the same track
-- number on two discs: the old schema cannot represent that album.
DROP INDEX IF EXISTS public.idx_media_items_parent_type_index;
CREATE UNIQUE INDEX idx_media_items_parent_type_index
    ON public.media_items USING btree (parent_id, type, index)
    WHERE parent_id IS NOT NULL AND index IS NOT NULL AND deleted_at IS NULL;
ALTER TABLE public.media_items
    DROP CONSTRAINT IF EXISTS media_items_disc_number_positive,
    DROP COLUMN IF EXISTS disc_number;
-- +goose StatementEnd
