-- +goose Up
-- +goose StatementBegin
-- Last.fm and Trakt links alongside ListenBrainz. Same rules as the
-- ListenBrainz token: credentials are AES-256-GCM-encrypted with the server
-- Encryptor (rotate-key re-seals them) and never returned to a client; no
-- credential means OnScreen sends that service nothing.
--
-- Last.fm: a session key from its token handshake. It doesn't expire; the
-- user revokes it from their Last.fm settings. The username is only for
-- display ("Linked as ...").
--
-- Trakt: an OAuth access + refresh token pair from the device-code flow. The
-- access token expires (trakt_expires_at) and is refreshed in place, so both
-- tokens are rewritten together.
ALTER TABLE public.user_scrobble
    ADD COLUMN lastfm_session_key  text,
    ADD COLUMN lastfm_username     text,
    ADD COLUMN trakt_access_token  text,
    ADD COLUMN trakt_refresh_token text,
    ADD COLUMN trakt_expires_at    timestamp with time zone,
    ADD COLUMN trakt_username      text;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.user_scrobble
    DROP COLUMN IF EXISTS lastfm_session_key,
    DROP COLUMN IF EXISTS lastfm_username,
    DROP COLUMN IF EXISTS trakt_access_token,
    DROP COLUMN IF EXISTS trakt_refresh_token,
    DROP COLUMN IF EXISTS trakt_expires_at,
    DROP COLUMN IF EXISTS trakt_username;
-- +goose StatementEnd
