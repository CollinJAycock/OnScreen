-- +goose Up
-- +goose StatementBegin
-- Outbound notification agents (Settings → Notifications): server-wide,
-- admin-configured channels — a household Discord / Telegram group, an ntfy
-- or Gotify server, an email list — that hear about request, issue and ops
-- events (internal/notifyagents).
--
--   kind:    discord | telegram | ntfy | gotify | email. Fixed at creation —
--            the secret is only meaningful for the kind it was entered for.
--   name:    admin label, 1..100 characters.
--   enabled: a disabled agent keeps its config but receives nothing (the
--            admin "Send test" still works so it can be verified first).
--   events:  subscribed event keys (notifyagents.Events); validated by the
--            API, not here, so adding an event needs no migration.
--   config:  the NON-secret per-kind fields as a JSON object — Telegram
--            chat_id, ntfy server_url + topic, Gotify server_url, email
--            recipients. Returned to the admin UI as-is.
--   secret:  the credential, AES-256-GCM encrypted with the settings
--            encryptor and bound (associated data) to this row's id — the
--            Discord webhook URL, the Telegram bot token, the ntfy access
--            token or the Gotify app token. NULL when the kind has none (email,
--            an open ntfy topic). Never returned by the API.
--   allow_private_network: opt-in for a self-hosted ntfy / Gotify on the LAN.
--            Off, delivery refuses private, loopback, link-local and CGNAT
--            destinations after DNS resolution. Discord and Telegram are
--            public services and may never set it (CHECK below).
--   last_success_at / last_error / last_error_at: delivery status for the
--            UI. last_error is sanitised (secret-bearing URLs stripped) and
--            bounded by the dispatcher.
CREATE TABLE public.notification_agents (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    kind text NOT NULL,
    name text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    events text[] DEFAULT '{}'::text[] NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL,
    secret text,
    allow_private_network boolean DEFAULT false NOT NULL,
    last_success_at timestamp with time zone,
    last_error text,
    last_error_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT notification_agents_pkey PRIMARY KEY (id),
    CONSTRAINT notification_agents_kind_check
        CHECK (kind = ANY (ARRAY['discord'::text, 'telegram'::text, 'ntfy'::text, 'gotify'::text, 'email'::text])),
    CONSTRAINT notification_agents_name_check
        CHECK (char_length(name) >= 1 AND char_length(name) <= 100),
    CONSTRAINT notification_agents_config_object_check
        CHECK (jsonb_typeof(config) = 'object'::text),
    CONSTRAINT notification_agents_last_error_length_check
        CHECK (last_error IS NULL OR char_length(last_error) <= 500),
    CONSTRAINT notification_agents_private_network_check
        CHECK (NOT allow_private_network OR kind = ANY (ARRAY['ntfy'::text, 'gotify'::text]))
);
-- Every event fans out to the enabled agents subscribed to it
-- (enabled AND $event = ANY(events)). The table holds a handful of rows, so
-- there is deliberately no index beyond the primary key.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.notification_agents;
-- +goose StatementEnd
