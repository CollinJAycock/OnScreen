-- +goose Up
-- Manga library type + reading_direction column on media_items.
--
-- Manga is a first-class library type alongside `book` rather than
-- a flag on top of it. Different mental model (read by chapter not
-- chapter-of-volume), different metadata source (AniList vs
-- OpenLibrary / Hardcover), and different reader UX (RTL pages,
-- two-page spreads, vertical-scroll for webtoons) — operators
-- organising on disk pick "Manga" knowing all three flip together.
--
-- reading_direction column carries one of:
--   'ltr' — Western convention; left-to-right pages
--   'rtl' — Japanese / manga convention; right-to-left pages
--   'ttb' — top-to-bottom; webtoons / manhwa / manhua vertical strip
-- Null means the reader picks based on library type (manga → rtl,
-- everything else → ltr). Stored on media_items so a per-book
-- override survives a library-default change.

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN (
        'movie', 'show', 'music', 'photo', 'dvr', 'audiobook',
        'podcast', 'home_video', 'book', 'anime', 'manga'
    ));

ALTER TABLE media_items
    ADD COLUMN reading_direction TEXT
    CHECK (reading_direction IN ('ltr', 'rtl', 'ttb'));

-- +goose Down
-- Reparent any manga libraries to 'book' before tightening the type
-- constraint so the down migration doesn't violate the new check.
UPDATE libraries SET type = 'book' WHERE type = 'manga';

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN (
        'movie', 'show', 'music', 'photo', 'dvr', 'audiobook',
        'podcast', 'home_video', 'book', 'anime'
    ));

ALTER TABLE media_items DROP COLUMN IF EXISTS reading_direction;
