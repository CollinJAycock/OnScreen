-- name: GetUser :one
SELECT id, username, email, password_hash, is_admin, pin,
       created_at, updated_at, google_id, google_avatar_url, github_id, discord_id,
       parent_user_id, avatar_url
FROM users
WHERE id = $1;

-- name: GetUserByUsername :one
SELECT id, username, email, password_hash, is_admin, pin,
       created_at, updated_at, google_id, google_avatar_url, github_id, discord_id,
       parent_user_id, avatar_url
FROM users
WHERE username = $1;

-- name: ListUsers :many
SELECT id, username, email, is_admin,
       created_at, updated_at
FROM users
ORDER BY username;

-- name: CreateUser :one
INSERT INTO users (username, email, password_hash, is_admin)
VALUES ($1, $2, $3, $4)
RETURNING id, username, email, password_hash, is_admin, pin,
          created_at, updated_at, google_id, google_avatar_url, github_id, discord_id,
          parent_user_id, avatar_url;

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
SELECT id, username, avatar_url, (pin IS NOT NULL) AS has_pin, created_at
FROM users
WHERE parent_user_id = $1
ORDER BY username;

-- name: CreateManagedProfile :one
INSERT INTO users (username, parent_user_id, avatar_url, pin, is_admin)
VALUES ($1, $2, $3, $4, false)
RETURNING id, username, avatar_url, created_at;

-- name: DeleteManagedProfile :exec
DELETE FROM users WHERE id = $1 AND parent_user_id = $2;

-- name: UpdateManagedProfile :one
UPDATE users SET username = $2, avatar_url = $3, updated_at = NOW()
WHERE id = $1 AND parent_user_id = $4
RETURNING id, username, avatar_url, created_at;

-- name: GetUserByEmail :one
SELECT id, username, email, password_hash, is_admin, pin,
       created_at, updated_at, google_id, google_avatar_url, github_id, discord_id,
       parent_user_id, avatar_url
FROM users
WHERE email = $1;

-- name: GetUserByGoogleID :one
SELECT id, username, email, password_hash, is_admin, pin,
       created_at, updated_at, google_id, google_avatar_url, github_id, discord_id,
       parent_user_id, avatar_url
FROM users
WHERE google_id = $1;

-- name: LinkGoogleAccount :exec
UPDATE users
SET google_id = $2, google_avatar_url = $3,
    email = COALESCE(email, $4),
    updated_at = NOW()
WHERE id = $1;

-- name: CreateGoogleUser :one
INSERT INTO users (username, email, google_id, google_avatar_url, is_admin)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, username, email, password_hash, is_admin, pin,
          created_at, updated_at, google_id, google_avatar_url, github_id, discord_id,
          parent_user_id, avatar_url;

-- name: GetUserByGitHubID :one
SELECT id, username, email, password_hash, is_admin, pin,
       created_at, updated_at, google_id, google_avatar_url, github_id, discord_id,
       parent_user_id, avatar_url
FROM users
WHERE github_id = $1;

-- name: LinkGitHubAccount :exec
UPDATE users
SET github_id = $2,
    email = COALESCE(email, $3),
    updated_at = NOW()
WHERE id = $1;

-- name: CreateGitHubUser :one
INSERT INTO users (username, email, github_id, is_admin)
VALUES ($1, $2, $3, $4)
RETURNING id, username, email, password_hash, is_admin, pin,
          created_at, updated_at, google_id, google_avatar_url, github_id, discord_id,
          parent_user_id, avatar_url;

-- name: GetUserByDiscordID :one
SELECT id, username, email, password_hash, is_admin, pin,
       created_at, updated_at, google_id, google_avatar_url, github_id, discord_id,
       parent_user_id, avatar_url
FROM users
WHERE discord_id = $1;

-- name: LinkDiscordAccount :exec
UPDATE users
SET discord_id = $2,
    email = COALESCE(email, $3),
    updated_at = NOW()
WHERE id = $1;

-- name: CreateDiscordUser :one
INSERT INTO users (username, email, discord_id, is_admin)
VALUES ($1, $2, $3, $4)
RETURNING id, username, email, password_hash, is_admin, pin,
          created_at, updated_at, google_id, google_avatar_url, github_id, discord_id,
          parent_user_id, avatar_url;
