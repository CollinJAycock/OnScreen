-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: ListUsers :many
SELECT id, username, email, is_admin,
       created_at, updated_at
FROM users
ORDER BY username;

-- name: CreateUser :one
INSERT INTO users (username, email, password_hash, is_admin)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: SetUserPIN :exec
UPDATE users SET pin = $2, updated_at = NOW() WHERE id = $1;

-- name: ClearUserPIN :exec
UPDATE users SET pin = NULL, updated_at = NOW() WHERE id = $1;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: SetUserAdmin :exec
UPDATE users SET is_admin = $2, updated_at = NOW() WHERE id = $1;

-- name: CountAdmins :one
SELECT COUNT(*) FROM users WHERE is_admin = true;

-- name: ListSwitchableUsers :many
SELECT id, username, is_admin, (pin IS NOT NULL) AS has_pin, avatar_url, parent_user_id
FROM users
ORDER BY username;

-- name: ListManagedProfiles :many
SELECT id, username, avatar_url, (pin IS NOT NULL) AS has_pin, created_at, max_content_rating
FROM users
WHERE parent_user_id = $1
ORDER BY username;

-- name: CreateManagedProfile :one
INSERT INTO users (username, parent_user_id, avatar_url, pin, is_admin)
VALUES ($1, $2, $3, $4, false)
RETURNING id, username, avatar_url, created_at;

-- name: DeleteManagedProfile :exec
DELETE FROM users WHERE id = $1 AND parent_user_id = $2;

-- name: DeleteManagedProfileAdmin :exec
DELETE FROM users WHERE id = $1 AND parent_user_id IS NOT NULL;

-- name: UpdateManagedProfile :one
UPDATE users SET username = $2, avatar_url = $3, updated_at = NOW()
WHERE id = $1 AND parent_user_id = $4
RETURNING id, username, avatar_url, created_at;

-- name: UpdateManagedProfileAdmin :one
UPDATE users SET username = $2, avatar_url = $3, updated_at = NOW()
WHERE id = $1 AND parent_user_id IS NOT NULL
RETURNING id, username, avatar_url, parent_user_id, created_at;

-- name: ListAllManagedProfiles :many
SELECT u.id, u.username, u.avatar_url, (u.pin IS NOT NULL) AS has_pin, u.created_at,
       u.parent_user_id AS owner_id, p.username AS owner_username, u.max_content_rating
FROM users u
JOIN users p ON p.id = u.parent_user_id
ORDER BY p.username, u.username;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByGoogleID :one
SELECT * FROM users WHERE google_id = $1;

-- name: LinkGoogleAccount :exec
UPDATE users
SET google_id = $2, google_avatar_url = $3,
    email = COALESCE(email, $4),
    updated_at = NOW()
WHERE id = $1;

-- name: CreateGoogleUser :one
INSERT INTO users (username, email, google_id, google_avatar_url, is_admin)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserByGitHubID :one
SELECT * FROM users WHERE github_id = $1;

-- name: LinkGitHubAccount :exec
UPDATE users
SET github_id = $2,
    email = COALESCE(email, $3),
    updated_at = NOW()
WHERE id = $1;

-- name: CreateGitHubUser :one
INSERT INTO users (username, email, github_id, is_admin)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserByDiscordID :one
SELECT * FROM users WHERE discord_id = $1;

-- name: LinkDiscordAccount :exec
UPDATE users
SET discord_id = $2,
    email = COALESCE(email, $3),
    updated_at = NOW()
WHERE id = $1;

-- name: CreateDiscordUser :one
INSERT INTO users (username, email, discord_id, is_admin)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserByOIDCSubject :one
SELECT * FROM users WHERE oidc_issuer = $1 AND oidc_subject = $2;

-- name: LinkOIDCAccount :exec
UPDATE users
SET oidc_issuer = $2,
    oidc_subject = $3,
    email = COALESCE(email, $4),
    updated_at = NOW()
WHERE id = $1;

-- name: CreateOIDCUser :one
INSERT INTO users (username, email, oidc_issuer, oidc_subject, is_admin)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserBySAMLSubject :one
-- Mirror of GetUserByOIDCSubject — keyed on (saml_issuer, saml_subject).
SELECT * FROM users WHERE saml_issuer = $1 AND saml_subject = $2;

-- name: LinkSAMLAccount :exec
-- Same shape as LinkOIDCAccount; used when a SAML login matches an
-- existing email-only stub user (provisioned via invite, etc.) and we
-- want to attach the SAML identity instead of creating a duplicate.
UPDATE users
SET saml_issuer = $2,
    saml_subject = $3,
    email = COALESCE(email, $4),
    updated_at = NOW()
WHERE id = $1;

-- name: CreateSAMLUser :one
-- JIT provisioning for a SAML login with no matching account.
INSERT INTO users (username, email, saml_issuer, saml_subject, is_admin)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserByLDAPDN :one
SELECT * FROM users WHERE ldap_dn = $1;

-- name: LinkLDAPAccount :exec
UPDATE users
SET ldap_dn = $2,
    email = COALESCE(email, $3),
    updated_at = NOW()
WHERE id = $1;

-- name: CreateLDAPUser :one
INSERT INTO users (username, email, ldap_dn, is_admin)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserPreferences :one
-- Client reads this on login to seed player defaults (language,
-- quality cap, codec preference). All nullable fields = "no preference"
-- → client falls back to its own logic or server defaults.
SELECT preferred_audio_lang, preferred_subtitle_lang, max_content_rating,
       max_video_bitrate_kbps, max_audio_bitrate_kbps, max_video_height,
       preferred_video_codec, forced_subtitles_only
FROM users
WHERE id = $1;

-- name: UpdateUserPreferences :exec
-- Language and content-rating preferences.
UPDATE users
SET preferred_audio_lang = $2,
    preferred_subtitle_lang = $3,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateUserQualityProfile :exec
-- Quality profile update. Pass NULL to clear any of these; client
-- then falls back to its own defaults.
UPDATE users
SET max_video_bitrate_kbps = $2,
    max_audio_bitrate_kbps = $3,
    max_video_height = $4,
    preferred_video_codec = $5,
    forced_subtitles_only = $6,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateUserContentRating :exec
UPDATE users
SET max_content_rating = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: BumpSessionEpoch :exec
-- Invalidates all outstanding PASETO access tokens for a user by bumping
-- their session_epoch counter. Called from admin demote, delete, and any
-- other future path that needs "kick this user out NOW" semantics.
UPDATE users
SET session_epoch = session_epoch + 1,
    updated_at = NOW()
WHERE id = $1;

-- name: GetSessionEpoch :one
-- Cheap lookup hit by the auth middleware on every authenticated request
-- so tokens whose epoch doesn't match the current DB row get rejected.
-- Indexed by the existing users PK — no extra index needed.
SELECT session_epoch FROM users WHERE id = $1;

-- name: CreateFirstAdmin :one
-- Atomic "first user is admin" gate — creates the row only if the
-- users table is empty. Returns (zero, pgx.ErrNoRows) when a user
-- already exists, letting the caller fall back to the normal admin-
-- only CreateUser path. Closes the race where two concurrent POST
-- /auth/register requests could each see count=0 and both become
-- admin.
INSERT INTO users (username, email, password_hash, is_admin)
SELECT $1, $2, $3, true
WHERE NOT EXISTS (SELECT 1 FROM users)
RETURNING *;
