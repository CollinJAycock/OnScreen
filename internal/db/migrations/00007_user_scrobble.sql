-- +goose Up
-- +goose StatementBegin
-- Per-user external-scrobble configuration (ListenBrainz today; Last.fm
-- planned). One row per user that has linked a service. Tokens are stored
-- AES-256-GCM-encrypted with the server Encryptor (the same scheme as webhook
-- and TOTP secrets) and decrypted in-process only when submitting a listen —
-- never returned to a client. Strictly opt-in: no row, or enabled = false,
-- means OnScreen submits nothing.
CREATE TABLE public.user_scrobble (
    user_id              uuid NOT NULL,
    listenbrainz_token   text,
    listenbrainz_enabled boolean DEFAULT false NOT NULL,
    created_at           timestamp with time zone DEFAULT now() NOT NULL,
    updated_at           timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT user_scrobble_pkey PRIMARY KEY (user_id),
    CONSTRAINT user_scrobble_user_fk FOREIGN KEY (user_id)
        REFERENCES public.users (id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.user_scrobble;
-- +goose StatementEnd
