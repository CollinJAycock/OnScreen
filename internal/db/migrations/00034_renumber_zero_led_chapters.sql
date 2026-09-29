-- +goose Up
-- +goose StatementBegin
-- Renumber the chapters of audiobooks with a chapter file whose name starts
-- with a zero ("00 - Prologue.mp3", "000 Opening Credits.mp3").
--
-- Chapters were first numbered (8b9b50b4) with a leading 0 read as "no
-- number", so such a file took its track tag's number instead, often 1, and
-- the real chapter 1 was then refused as a duplicate and left unnumbered,
-- sorting last. Numbering is fill-only, so a rescan never corrects a number
-- already stored. Clearing the numbers of just those books lets the next
-- scan number them again under the fixed rule, where a leading 0 is the
-- place before chapter 1. Other books keep their numbers. Until that scan,
-- the affected books list their chapters by title.
UPDATE media_items AS c
SET index = NULL
WHERE c.type = 'audiobook_chapter'
  AND c.index IS NOT NULL
  AND c.parent_id IN (
      SELECT ch.parent_id
      FROM media_items ch
      JOIN media_files f ON f.media_item_id = ch.id
      WHERE ch.type = 'audiobook_chapter'
        AND f.file_path ~ '(^|[/\\])\s*0+([\s._-][^/\\]*)?$'
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Nothing to restore: the next scan numbers the chapters either way.
SELECT 1;
-- +goose StatementEnd
