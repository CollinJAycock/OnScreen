-- +goose Up
-- SAML: store the IdP entity ID + NameID pair, mirroring the OIDC
-- (issuer, subject) approach so two different IdPs can't collide on
-- the same NameID. The pair is the canonical SAML user key —
-- different IdPs may both emit "user@example.com" as a NameID;
-- scoping by entity URI keeps them distinct.
--
-- Held in separate columns from oidc_issuer/oidc_subject so an org
-- that has both protocols configured doesn't see SAML and OIDC
-- accounts with the same string collide. Mutually-exclusive in
-- practice but cheap insurance.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS saml_issuer  TEXT,
    ADD COLUMN IF NOT EXISTS saml_subject TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS users_saml_unique
    ON users (saml_issuer, saml_subject)
    WHERE saml_issuer IS NOT NULL AND saml_subject IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS users_saml_unique;
ALTER TABLE users
    DROP COLUMN IF EXISTS saml_subject,
    DROP COLUMN IF EXISTS saml_issuer;
