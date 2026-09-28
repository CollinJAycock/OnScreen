-- +goose Up
-- +goose StatementBegin
-- User-reported playback / metadata problems ("Report a problem" on an item or
-- in the player), worked by admins on the Library health page.
--
--   item_id:     the reported item (movie, episode, show, …). Deleting the item
--                deletes its reports — there is nothing left to fix.
--   file_id:     the specific media file the reporter was playing, when known.
--                SET NULL on delete: a re-grab replaces the file, and the report
--                (usually resolved by then) should outlive it.
--   user_id:     the reporter. Deleting the account deletes its reports.
--   kind:        what is wrong — video, audio, subtitles, wrong_match, other.
--   note:        optional free text from the reporter, at most 1000 characters.
--   status:      open → resolved | dismissed (set by an admin).
--   resolved_at / resolved_by / resolution_note: who closed it, when and why.
--                resolved_by is SET NULL when that admin account is deleted.
--
-- The primary key defaults to gen_random_uuid() like every other table.
CREATE TABLE public.media_issues (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    item_id uuid NOT NULL,
    file_id uuid,
    user_id uuid NOT NULL,
    kind text NOT NULL,
    note text,
    status text DEFAULT 'open'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    resolved_at timestamp with time zone,
    resolved_by uuid,
    resolution_note text,
    CONSTRAINT media_issues_pkey PRIMARY KEY (id),
    CONSTRAINT media_issues_item_id_fkey FOREIGN KEY (item_id)
        REFERENCES public.media_items(id) ON DELETE CASCADE,
    CONSTRAINT media_issues_file_id_fkey FOREIGN KEY (file_id)
        REFERENCES public.media_files(id) ON DELETE SET NULL,
    CONSTRAINT media_issues_user_id_fkey FOREIGN KEY (user_id)
        REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT media_issues_resolved_by_fkey FOREIGN KEY (resolved_by)
        REFERENCES public.users(id) ON DELETE SET NULL,
    CONSTRAINT media_issues_kind_check
        CHECK (kind = ANY (ARRAY['video'::text, 'audio'::text, 'subtitles'::text, 'wrong_match'::text, 'other'::text])),
    CONSTRAINT media_issues_status_check
        CHECK (status = ANY (ARRAY['open'::text, 'resolved'::text, 'dismissed'::text])),
    CONSTRAINT media_issues_note_length_check
        CHECK (note IS NULL OR char_length(note) <= 1000),
    CONSTRAINT media_issues_resolution_note_length_check
        CHECK (resolution_note IS NULL OR char_length(resolution_note) <= 1000),
    -- An open report has no resolution; a closed one always records when.
    CONSTRAINT media_issues_resolved_pair_check
        CHECK ((status = 'open'::text) = (resolved_at IS NULL))
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Admin queue: filter by status, newest first.
CREATE INDEX idx_media_issues_status_created
    ON public.media_issues USING btree (status, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-item lookups (the reporter's own reports on the item page) and the
-- media_items delete cascade.
CREATE INDEX idx_media_issues_item ON public.media_issues USING btree (item_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-user open-report cap and the users delete cascade.
CREATE INDEX idx_media_issues_user_status ON public.media_issues USING btree (user_id, status);
-- +goose StatementEnd

-- +goose StatementBegin
-- One OPEN report per (user, item, kind): a second "video won't play" from the
-- same user on the same title is the same report. Enforced here so two
-- concurrent submits can't both pass the handler's pre-check.
CREATE UNIQUE INDEX uq_media_issues_open_user_item_kind
    ON public.media_issues USING btree (user_id, item_id, kind)
    WHERE status = 'open'::text;
-- +goose StatementEnd

-- +goose StatementBegin
-- FK SET NULL targets need an index too, or deleting a media file / admin
-- account seq-scans this table.
CREATE INDEX idx_media_issues_file ON public.media_issues USING btree (file_id)
    WHERE file_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_media_issues_resolved_by ON public.media_issues USING btree (resolved_by)
    WHERE resolved_by IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Library health lists damaged files; the integrity probe marks very few, so a
-- partial index keeps that lookup off a media_files seq scan.
CREATE INDEX idx_media_files_integrity_damaged
    ON public.media_files USING btree (integrity_checked_at DESC)
    WHERE integrity_status = 'damaged'::text;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.idx_media_files_integrity_damaged;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS public.media_issues;
-- +goose StatementEnd
