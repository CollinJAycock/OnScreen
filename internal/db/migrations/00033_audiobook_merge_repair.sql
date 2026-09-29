-- +goose Up
-- +goose StatementBegin
-- The moment this server began repairing audiobooks merged by the old
-- post-scan dedupe, which compared books by original_title (the author)
-- and so folded every book by one author into one (fixed in 1fcc4b9d).
-- RestoreMergedAudiobooks only considers books soft-deleted before this
-- row's updated_at: the repair then happens once per library, and a book
-- deleted afterwards (by a user, or by the phantom prune) is never
-- brought back. The value is unused.
INSERT INTO public.server_settings (key, value, updated_at)
VALUES ('audiobook_merge_repair_cutoff', '', now())
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM public.server_settings WHERE key = 'audiobook_merge_repair_cutoff';
-- +goose StatementEnd
