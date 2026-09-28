-- +goose Up
-- +goose StatementBegin
-- The squashed init only allowed three notification types
-- (new_content / scan_complete / system), but the media-request workflow has
-- always sent request_created / request_approved / request_declined /
-- request_available. Every one of those INSERTs failed the CHECK, and
-- notification.Service.Notify only logs a failed insert, so no requester ever
-- heard about their request. Replace the fixed list with a format check: the
-- set of types is owned by the Go constants in internal/notification (see
-- types.go, whose test also holds every constant to this same pattern), so a
-- new type can't silently fail again, while junk (empty, spaces, upper case,
-- unbounded length) is still refused.
ALTER TABLE public.notifications
    DROP CONSTRAINT IF EXISTS notifications_type_check;
ALTER TABLE public.notifications
    ADD CONSTRAINT notifications_type_check CHECK (type ~ '^[a-z][a-z0-9_]{0,63}$');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Rows of the newer types can't satisfy the old constraint; drop them rather
-- than fail the rollback (notifications are a 30-day inbox, not a record).
DELETE FROM public.notifications
    WHERE type <> ALL (ARRAY['new_content'::text, 'scan_complete'::text, 'system'::text]);
ALTER TABLE public.notifications
    DROP CONSTRAINT IF EXISTS notifications_type_check;
ALTER TABLE public.notifications
    ADD CONSTRAINT notifications_type_check CHECK ((type = ANY (ARRAY['new_content'::text, 'scan_complete'::text, 'system'::text])));
-- +goose StatementEnd
