-- +goose Up
-- OIDC: store the issuer + subject pair so two different IdPs can't collide
-- on the same `sub` value. The pair is what RFC 9068 specifies as the
-- canonical user identifier.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS oidc_issuer  TEXT,
    ADD COLUMN IF NOT EXISTS oidc_subject TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS users_oidc_unique
    ON users (oidc_issuer, oidc_subject)
    WHERE oidc_issuer IS NOT NULL AND oidc_subject IS NOT NULL;

-- LDAP: distinguished name uniquely identifies a directory entry, so it's
-- the natural per-user key. We don't need an issuer-equivalent — a single
-- LDAPConfig is wired at a time.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS ldap_dn TEXT UNIQUE;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS ldap_dn;
DROP INDEX IF EXISTS users_oidc_unique;
ALTER TABLE users
    DROP COLUMN IF EXISTS oidc_subject,
    DROP COLUMN IF EXISTS oidc_issuer;
