-- +goose Up
-- +goose StatementBegin
-- Per-user listening speed for an audiobook. book_id is always the audiobook
-- item (a chapter's speed is its book's). A book with no row plays at the
-- user's most recently set speed, so a 1.5x listener stays at 1.5x on a new
-- book; updated_at orders that lookup.
CREATE TABLE public.user_audiobook_rate (
    user_id uuid NOT NULL,
    book_id uuid NOT NULL,
    playback_rate real NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT user_audiobook_rate_pkey PRIMARY KEY (user_id, book_id),
    CONSTRAINT user_audiobook_rate_user_id_fkey FOREIGN KEY (user_id)
        REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT user_audiobook_rate_book_id_fkey FOREIGN KEY (book_id)
        REFERENCES public.media_items(id) ON DELETE CASCADE,
    CONSTRAINT user_audiobook_rate_range_check
        CHECK (playback_rate >= 0.5 AND playback_rate <= 3.0)
);
CREATE INDEX idx_user_audiobook_rate_recent
    ON public.user_audiobook_rate USING btree (user_id, updated_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- A user's bookmarks in an audiobook.
--
--   book_id:     the audiobook, which lists them.
--   item_id:     the playable item the position is in: the book itself for a
--                single-file book, the chapter for a multi-file one.
--   position_ms: offset into item_id's file.
--   note:        optional text, at most 500 characters ('' when none).
--
-- Deleting the account or the book deletes its bookmarks.
CREATE TABLE public.audiobook_bookmarks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    book_id uuid NOT NULL,
    item_id uuid NOT NULL,
    position_ms bigint NOT NULL,
    note text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT audiobook_bookmarks_pkey PRIMARY KEY (id),
    CONSTRAINT audiobook_bookmarks_user_id_fkey FOREIGN KEY (user_id)
        REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT audiobook_bookmarks_book_id_fkey FOREIGN KEY (book_id)
        REFERENCES public.media_items(id) ON DELETE CASCADE,
    CONSTRAINT audiobook_bookmarks_item_id_fkey FOREIGN KEY (item_id)
        REFERENCES public.media_items(id) ON DELETE CASCADE,
    CONSTRAINT audiobook_bookmarks_position_check CHECK (position_ms >= 0),
    CONSTRAINT audiobook_bookmarks_note_length_check CHECK (char_length(note) <= 500)
);
CREATE INDEX idx_audiobook_bookmarks_user_book
    ON public.audiobook_bookmarks USING btree (user_id, book_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.audiobook_bookmarks;
DROP TABLE IF EXISTS public.user_audiobook_rate;
-- +goose StatementEnd
