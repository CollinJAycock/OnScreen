-- +goose Up
-- Anime as a first-class library type, parallel to home_video /
-- audiobook / podcast — not a flag on top of `show`. The mental
-- model maps to how users organise content on disk: a library is
-- "Anime" or "TV", not "TV with the anime checkbox flipped".
--
-- Library shape: scanner treats anime libraries the same as `show`
-- libraries (anime content is shows + episodes; the absolute-numbering
-- parser already handles fansub-style filenames). The library type
-- alone is what the enricher reads to flip AniList from fallback to
-- primary metadata source — no separate flag column.

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr', 'audiobook', 'podcast', 'home_video', 'book', 'anime'));

-- +goose Down
-- Reparent any anime libraries to 'show' before tightening the
-- constraint, so the down migration doesn't violate the new check.
UPDATE libraries SET type = 'show' WHERE type = 'anime';

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr', 'audiobook', 'podcast', 'home_video', 'book'));
