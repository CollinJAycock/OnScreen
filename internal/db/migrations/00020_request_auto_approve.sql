-- +goose Up
-- +goose StatementBegin
-- Per-user auto-approval of media requests, split by media type so an admin
-- can trust someone's movie picks without also handing them whole TV series
-- (which can be hundreds of episodes of disk).
--
--   auto_approve_movies / auto_approve_tv: when true, a request of that type
--       is forwarded to Radarr/Sonarr at creation instead of waiting in the
--       admin queue. Both default false: existing accounts keep today's
--       behaviour and nobody gains the permission by a schema change. New
--       full accounts take their starting values from the admin-set
--       server_settings defaults (Settings ▸ Requests); managed household
--       profiles always start false. A content-rating ceiling
--       (max_content_rating) overrides both — a capped account's requests
--       always go to the admin queue — and admins are always auto-approved,
--       so neither of those cases depends on these columns.
ALTER TABLE public.users
    ADD COLUMN auto_approve_movies boolean DEFAULT false NOT NULL,
    ADD COLUMN auto_approve_tv boolean DEFAULT false NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Marks a request that was approved by the requester's standing permission
-- rather than by an admin. Such rows keep decided_by NULL (no admin made the
-- call) with decided_at set; this flag is what tells them apart from a row an
-- admin approved. False when an auto-approval was attempted but the send to
-- Radarr/Sonarr failed — that request stays pending for an admin as before.
ALTER TABLE public.media_requests
    ADD COLUMN auto_approved boolean DEFAULT false NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.media_requests
    DROP COLUMN IF EXISTS auto_approved;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE public.users
    DROP COLUMN IF EXISTS auto_approve_movies,
    DROP COLUMN IF EXISTS auto_approve_tv;
-- +goose StatementEnd
