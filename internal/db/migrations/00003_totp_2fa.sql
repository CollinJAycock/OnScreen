-- +goose Up
-- +goose StatementBegin
-- ────────────────────────────────────────────────────────────────────────────
-- TOTP (RFC 6238) two-factor auth for local password accounts.
--
-- totp_secret holds the base32 TOTP secret encrypted at rest with the
-- server SECRET_KEY (auth.Encryptor, AES-256-GCM — same path as webhook
-- secrets / settings keys), stored as the base64 ciphertext string.
-- NULL = never set up.
--
-- totp_enabled flips true only after the user confirms the first code
-- during activation, so a half-finished setup (secret generated, QR
-- shown, app never scanned) doesn't lock the account out at next login.
--
-- Federated accounts (OIDC / SAML / LDAP) get 2FA at their IdP; this is
-- local-account-only. No constraint enforces that — the login flow
-- simply never sets totp_enabled on a federated user.
-- ────────────────────────────────────────────────────────────────────────────
ALTER TABLE public.users
    ADD COLUMN totp_secret text,
    ADD COLUMN totp_enabled boolean NOT NULL DEFAULT false;

-- Single-use recovery codes for when the authenticator device is lost.
-- We store only a SHA-256 hash of each code (codes are high-entropy, so
-- a fast hash is fine — same rationale as refresh-token hashing). used_at
-- marks consumption; a consumed code can never be replayed.
CREATE TABLE public.totp_recovery_codes (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    code_hash text NOT NULL,
    used_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT totp_recovery_codes_pkey PRIMARY KEY (id),
    CONSTRAINT totp_recovery_codes_user_id_fkey FOREIGN KEY (user_id)
        REFERENCES public.users(id) ON DELETE CASCADE
);

-- Cover the FK cascade column (deleting a user wipes their codes) and the
-- per-user lookup the verify path runs. Non-partial on purpose — see the
-- 00002 cascade-index lesson.
CREATE INDEX idx_totp_recovery_codes_user
    ON public.totp_recovery_codes USING btree (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.totp_recovery_codes;
ALTER TABLE public.users
    DROP COLUMN IF EXISTS totp_enabled,
    DROP COLUMN IF EXISTS totp_secret;
-- +goose StatementEnd
