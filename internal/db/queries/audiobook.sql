-- name: GetAudiobookRate :one
-- The user's listening speed for one book.
SELECT playback_rate FROM user_audiobook_rate
WHERE user_id = @user_id AND book_id = @book_id;

-- name: GetLatestAudiobookRate :one
-- The speed the user set most recently on any book: what a book without its
-- own speed starts at.
SELECT playback_rate FROM user_audiobook_rate
WHERE user_id = @user_id
ORDER BY updated_at DESC
LIMIT 1;

-- name: SetAudiobookRate :exec
INSERT INTO user_audiobook_rate (user_id, book_id, playback_rate, updated_at)
VALUES (@user_id, @book_id, @playback_rate, now())
ON CONFLICT (user_id, book_id) DO UPDATE
    SET playback_rate = EXCLUDED.playback_rate,
        updated_at    = now();

-- name: ListAudiobookBookmarks :many
-- A user's bookmarks in one book, in listening order: a single-file book's
-- by position, a multi-file book's by chapter then position. Bookmarks in a
-- chapter that has since been removed are left out.
SELECT b.id, b.item_id, i.title AS item_title, i.index AS item_index,
       b.position_ms, b.note, b.created_at
FROM audiobook_bookmarks b
JOIN media_items i ON i.id = b.item_id AND i.deleted_at IS NULL
WHERE b.user_id = @user_id AND b.book_id = @book_id
ORDER BY CASE WHEN b.item_id = b.book_id THEN NULL ELSE i.index END NULLS FIRST,
         b.position_ms, b.created_at, b.id;

-- name: CreateAudiobookBookmark :one
-- Adds a bookmark unless the user already has max_per_book in this book; no
-- row comes back when the cap is reached.
INSERT INTO audiobook_bookmarks (user_id, book_id, item_id, position_ms, note)
SELECT @user_id, @book_id, @item_id, @position_ms, @note
WHERE (SELECT count(*) FROM audiobook_bookmarks
       WHERE user_id = @user_id AND book_id = @book_id) < @max_per_book::bigint
RETURNING id, created_at;

-- name: UpdateAudiobookBookmarkNote :execrows
UPDATE audiobook_bookmarks SET note = @note
WHERE id = @id AND user_id = @user_id;

-- name: DeleteAudiobookBookmark :execrows
DELETE FROM audiobook_bookmarks
WHERE id = @id AND user_id = @user_id;
