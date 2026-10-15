-- "Download my data" (POST /users/me/export, POST /users/{id}/export): the
-- rows the server holds about one user, for internal/userexport. Every query
-- names its columns so that no credential material — password hash, PIN,
-- TOTP secret or recovery codes, refresh-token hashes, API key hashes,
-- scrobble tokens — can reach the archive by a later column addition.
-- Items are referenced by id here and resolved by ExportItemRefs.

-- name: ExportUserProfile :one
SELECT id, username, email, is_admin, created_at, updated_at,
       parent_user_id, avatar_url, google_avatar_url,
       preferred_audio_lang, preferred_subtitle_lang, max_content_rating,
       max_video_bitrate_kbps, max_audio_bitrate_kbps, max_video_height,
       preferred_video_codec, forced_subtitles_only, episode_use_show_poster,
       max_concurrent_streams, max_stream_bitrate_kbps, max_remote_stream_bitrate_kbps,
       hub_layout, can_request, request_quota_movies, request_quota_tv,
       auto_approve_movies, auto_approve_tv, inherit_library_access,
       blocked_labels, allowed_labels, totp_enabled,
       (password_hash IS NOT NULL)::bool AS has_password,
       (pin IS NOT NULL)::bool AS has_pin,
       (google_id IS NOT NULL)::bool AS google_linked,
       (github_id IS NOT NULL)::bool AS github_linked,
       (discord_id IS NOT NULL)::bool AS discord_linked,
       (oidc_subject IS NOT NULL)::bool AS oidc_linked,
       (saml_subject IS NOT NULL)::bool AS saml_linked,
       (ldap_dn IS NOT NULL)::bool AS ldap_linked
FROM users
WHERE id = $1;

-- name: ExportUserSessions :many
-- Signed-in devices: no token material.
SELECT id, client_name, platform, device_id, ip_addr,
       created_at, last_seen, expires_at
FROM sessions
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: ExportUserScrobble :one
-- Link status only: whether each service is linked, and the account name it
-- reports. Never the tokens.
SELECT listenbrainz_enabled,
       (listenbrainz_token IS NOT NULL)::bool AS listenbrainz_linked,
       (lastfm_session_key IS NOT NULL)::bool AS lastfm_linked,
       lastfm_username,
       (trakt_access_token IS NOT NULL)::bool AS trakt_linked,
       trakt_username, created_at, updated_at
FROM user_scrobble
WHERE user_id = $1;

-- name: ExportWatchEvents :many
-- Play events, newest first, keyset-paged on (occurred_at, id). The handler
-- caps the total.
SELECT id, media_id, event_type, position_ms, duration_ms,
       client_name, client_ip, decision, occurred_at
FROM watch_events
WHERE user_id = sqlc.arg('user_id')
  AND (sqlc.narg('before_at')::timestamptz IS NULL
       OR (occurred_at, id) < (sqlc.narg('before_at')::timestamptz, sqlc.arg('before_id')::uuid))
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg('lim')::int;

-- name: ExportWatchProgress :many
SELECT media_id, position_ms, duration_ms, last_event_at, last_client_name,
       last_completed_at, mark_state, marked_at
FROM watch_progress
WHERE user_id = $1
ORDER BY last_event_at DESC NULLS LAST, media_id;

-- name: ExportWatchStatus :many
SELECT media_item_id, status, created_at, updated_at
FROM user_watch_status
WHERE user_id = $1
ORDER BY updated_at DESC, media_item_id;

-- name: ExportRatings :many
SELECT media_item_id, score, created_at, updated_at
FROM user_ratings
WHERE user_id = $1
ORDER BY updated_at DESC, media_item_id;

-- name: ExportFavorites :many
SELECT media_id, created_at
FROM user_favorites
WHERE user_id = $1
ORDER BY created_at DESC, media_id;

-- name: ExportReadingProgress :many
SELECT media_id, locator, progression, device_name, client_updated_at, updated_at
FROM reading_progress
WHERE user_id = $1
ORDER BY updated_at DESC, media_id;

-- name: ExportAudiobookRates :many
SELECT book_id, playback_rate, updated_at
FROM user_audiobook_rate
WHERE user_id = $1
ORDER BY updated_at DESC, book_id;

-- name: ExportAudiobookBookmarks :many
SELECT id, book_id, item_id, position_ms, note, created_at
FROM audiobook_bookmarks
WHERE user_id = $1
ORDER BY created_at DESC, id;

-- name: ExportUserCollections :many
-- Everything the user owns in the collections table: playlists, smart
-- playlists, and any other user-owned collection. (Admin-curated collections
-- are server-owned and not attributed to anyone.)
SELECT id, name, description, type, rules, created_at, updated_at
FROM collections
WHERE user_id = sqlc.arg('user_id')::uuid
ORDER BY created_at, id;

-- name: ExportUserCollectionItems :many
SELECT ci.collection_id, ci.media_item_id, ci.position, ci.added_at
FROM collection_items ci
JOIN collections c ON c.id = ci.collection_id
WHERE c.user_id = sqlc.arg('user_id')::uuid
ORDER BY ci.collection_id, ci.position, ci.media_item_id;

-- name: ExportMediaRequests :many
SELECT id, type, tmdb_id, title, year, status, seasons, decline_reason,
       decided_at, fulfilled_item_id, fulfilled_at, auto_approved,
       created_at, updated_at
FROM media_requests
WHERE user_id = $1
ORDER BY created_at DESC, id;

-- name: ExportMediaIssues :many
SELECT id, item_id, kind, note, status, created_at, resolved_at, resolution_note
FROM media_issues
WHERE user_id = $1
ORDER BY created_at DESC, id;

-- name: ExportNotifications :many
-- The in-app inbox, newest first, capped by the caller.
SELECT id, type, title, body, item_id, read, created_at
FROM notifications
WHERE user_id = $1
ORDER BY created_at DESC, id
LIMIT sqlc.arg('lim')::int;

-- name: ExportItemRefs :many
-- What an exported row's item id refers to: title, its place in a show or
-- album, and the external ids that make it meaningful outside this server.
-- visible is false when the item has been removed or the exporting viewer
-- can't see it (library access, rating ceiling, label restriction — the same
-- three gates as everywhere else; library_ids NULL = every library); the
-- archive then carries only the id.
SELECT mi.id, mi.type, mi.title, mi.year, mi.index,
       p.title AS parent_title, p.index AS parent_index,
       gp.title AS grandparent_title,
       mi.tmdb_id, mi.tvdb_id, mi.imdb_id, mi.musicbrainz_id, mi.anilist_id,
       gp.tmdb_id AS grandparent_tmdb_id, gp.tvdb_id AS grandparent_tvdb_id,
       gp.imdb_id AS grandparent_imdb_id,
       (mi.deleted_at IS NULL
        AND (sqlc.narg('library_ids')::uuid[] IS NULL
             OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
        AND (sqlc.narg('max_rating_rank')::int IS NULL
             OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
        AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
       )::bool AS visible
FROM media_items mi
LEFT JOIN media_items p ON p.id = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
WHERE mi.id = ANY(sqlc.arg('ids')::uuid[]);
