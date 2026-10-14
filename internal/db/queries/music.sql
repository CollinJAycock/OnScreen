-- ── Loudness analysis (migration 00048) ──────────────────────────────────────

-- name: ListAlbumsNeedingLoudness :many
-- Work queue for the opt-in loudness_analysis task: albums holding at least
-- one active track file that has no ReplayGain from tags and no current
-- measurement (none at all, or one taken of different bytes — the file's
-- size or content hash has changed since). The album is the unit of work
-- because album gain needs every track's loudness. A measurement that failed
-- or found silence is current too, so such a file isn't retried until it
-- changes. Newest files first, like the integrity probe. library_id scopes
-- an admin-triggered run; NULL sweeps every library.
SELECT al.id
FROM media_files mf
JOIN media_items t  ON t.id = mf.media_item_id AND t.type = 'track' AND t.deleted_at IS NULL
JOIN media_items al ON al.id = t.parent_id AND al.type = 'album' AND al.deleted_at IS NULL
WHERE mf.status = 'active'
  AND (mf.replaygain_source IS NULL OR mf.replaygain_source = 'analyzed')
  AND NOT EXISTS (
      SELECT 1 FROM media_file_loudness l
      WHERE l.file_id = mf.id
        AND l.file_size = mf.file_size
        AND l.file_hash IS NOT DISTINCT FROM mf.file_hash
  )
  AND (sqlc.narg('library_id')::uuid IS NULL OR t.library_id = sqlc.narg('library_id')::uuid)
GROUP BY al.id
ORDER BY MAX(mf.created_at) DESC
LIMIT sqlc.arg('lim')::int;

-- name: CountAlbumsNeedingLoudness :one
-- Same filter as ListAlbumsNeedingLoudness; backs the task's progress line.
SELECT COUNT(DISTINCT al.id)
FROM media_files mf
JOIN media_items t  ON t.id = mf.media_item_id AND t.type = 'track' AND t.deleted_at IS NULL
JOIN media_items al ON al.id = t.parent_id AND al.type = 'album' AND al.deleted_at IS NULL
WHERE mf.status = 'active'
  AND (mf.replaygain_source IS NULL OR mf.replaygain_source = 'analyzed')
  AND NOT EXISTS (
      SELECT 1 FROM media_file_loudness l
      WHERE l.file_id = mf.id
        AND l.file_size = mf.file_size
        AND l.file_hash IS NOT DISTINCT FROM mf.file_hash
  )
  AND (sqlc.narg('library_id')::uuid IS NULL OR t.library_id = sqlc.narg('library_id')::uuid);

-- name: ListAlbumLoudnessFiles :many
-- Every active track file of one album, with its stored measurement (if
-- any) and whether that measurement is of the file's current bytes. Files
-- with ReplayGain tags are listed too: their loudness counts towards the
-- album gain of the album's analysed files, though their own values are
-- never touched.
SELECT mf.id, mf.media_item_id, mf.file_path, mf.file_size, mf.file_hash,
       mf.duration_ms, mf.replaygain_source,
       l.integrated_lufs, l.true_peak, l.error AS loudness_error,
       (l.file_id IS NOT NULL
        AND l.file_size = mf.file_size
        AND l.file_hash IS NOT DISTINCT FROM mf.file_hash)::boolean AS measured
FROM media_files mf
JOIN media_items t ON t.id = mf.media_item_id AND t.type = 'track' AND t.deleted_at IS NULL
LEFT JOIN media_file_loudness l ON l.file_id = mf.id
WHERE t.parent_id = $1
  AND mf.status = 'active'
ORDER BY t.disc_number NULLS FIRST, t.index NULLS LAST, mf.created_at;

-- name: UpsertFileLoudness :exec
-- Records one measurement (or the reason there is none). file_size and
-- file_hash are the file as measured; see media_file_loudness.
INSERT INTO media_file_loudness (
    file_id, integrated_lufs, true_peak, duration_ms, file_size, file_hash, error, analyzed_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, NOW()
)
ON CONFLICT (file_id) DO UPDATE
SET integrated_lufs = EXCLUDED.integrated_lufs,
    true_peak       = EXCLUDED.true_peak,
    duration_ms     = EXCLUDED.duration_ms,
    file_size       = EXCLUDED.file_size,
    file_hash       = EXCLUDED.file_hash,
    error           = EXCLUDED.error,
    analyzed_at     = NOW();

-- name: SetAnalyzedReplayGain :execrows
-- Writes measured ReplayGain values into the same columns tag values live
-- in, so every client's normalisation picks them up. Refuses a file whose
-- values came from tags (zero rows): a tag is never overwritten.
UPDATE media_files
SET replaygain_track_gain = $2,
    replaygain_track_peak = $3,
    replaygain_album_gain = $4,
    replaygain_album_peak = $5,
    replaygain_source     = 'analyzed'
WHERE id = $1
  AND (replaygain_source IS NULL OR replaygain_source = 'analyzed');

-- ── Featured artist credits (migration 00048) ────────────────────────────────

-- name: FindArtistByMusicBrainzID :one
-- The library's artist carrying a MusicBrainz artist id, oldest first. Used
-- before the name match when a featured credit carries an MBID, so "Beyoncé"
-- and "Beyonce" resolve to one artist.
SELECT id, title
FROM media_items
WHERE library_id = $1
  AND type = 'artist'
  AND parent_id IS NULL
  AND deleted_at IS NULL
  AND (musicbrainz_artist_id = sqlc.arg('mbid')::uuid OR musicbrainz_id = sqlc.arg('mbid')::uuid)
ORDER BY created_at
LIMIT 1;

-- name: DeleteTrackArtistsExcept :exec
-- Drops the track's credits that its tags no longer name. keep is the
-- artist ids the latest read produced (empty clears them all).
DELETE FROM track_artists
WHERE track_id = sqlc.arg('track_id')
  AND NOT (artist_id = ANY(sqlc.arg('keep')::uuid[]));

-- name: UpsertTrackArtist :exec
INSERT INTO track_artists (track_id, artist_id, role, position)
VALUES ($1, $2, $3, $4)
ON CONFLICT (track_id, artist_id) DO UPDATE
SET role = EXCLUDED.role, position = EXCLUDED.position;

-- name: TrackCreditsScanned :one
SELECT EXISTS (SELECT 1 FROM track_credit_scans WHERE file_id = $1)::boolean;

-- name: MarkTrackCreditsScanned :exec
INSERT INTO track_credit_scans (file_id, scanned_at)
VALUES ($1, NOW())
ON CONFLICT (file_id) DO UPDATE SET scanned_at = NOW();

-- name: ListArtistAppearances :many
-- Tracks an artist is credited on without being their album's artist: the
-- artist page's "Appears on". The content-rating ceiling and the label
-- restriction apply to each track (a label on its album or album artist
-- hides it too); library access is the handler's.
SELECT t.id, t.library_id, t.title, t.index, t.disc_number, t.duration_ms,
       ta.role,
       al.id AS album_id, al.title AS album_title, al.year AS album_year,
       al.poster_path AS album_poster_path, al.updated_at AS album_updated_at,
       ar.id AS album_artist_id, ar.title AS album_artist_title
FROM track_artists ta
JOIN media_items t  ON t.id = ta.track_id AND t.type = 'track' AND t.deleted_at IS NULL
JOIN media_items al ON al.id = t.parent_id AND al.deleted_at IS NULL
LEFT JOIN media_items ar ON ar.id = al.parent_id AND ar.deleted_at IS NULL
WHERE ta.artist_id = sqlc.arg('artist_id')
  AND COALESCE(al.parent_id, '00000000-0000-0000-0000-000000000000'::uuid) <> sqlc.arg('artist_id')
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(t.content_rating) <= sqlc.narg('max_rating_rank'))
  AND media_item_labels_permitted(t.labels, t.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY COALESCE(al.original_year, al.year) DESC NULLS LAST, al.title, al.id,
         t.disc_number NULLS FIRST, t.index NULLS LAST
LIMIT sqlc.arg('result_limit')::int;

-- ── Similar artists / instant mix ────────────────────────────────────────────

-- name: ListArtistCoListens :many
-- Co-listening aggregated to the artist level: for every track of the seed
-- artist, the item_cooccurrence pairs it forms with tracks of other artists
-- in the same library, summed per other artist. Scores are distinct-listener
-- counts per track pair, so an artist with many tracks collects more; the
-- caller normalises by track counts.
WITH seed_tracks AS (
    SELECT t.id
    FROM media_items t
    JOIN media_items al ON al.id = t.parent_id
    WHERE al.parent_id = sqlc.arg('artist_id')
      AND t.type = 'track'
      AND t.deleted_at IS NULL
),
co AS (
    SELECT ic.item_b AS other_id, ic.score FROM item_cooccurrence ic JOIN seed_tracks s ON s.id = ic.item_a
    UNION ALL
    SELECT ic.item_a AS other_id, ic.score FROM item_cooccurrence ic JOIN seed_tracks s ON s.id = ic.item_b
)
SELECT ar.id AS artist_id, SUM(co.score)::bigint AS score
FROM co
JOIN media_items t  ON t.id = co.other_id AND t.type = 'track' AND t.deleted_at IS NULL
JOIN media_items al ON al.id = t.parent_id
JOIN media_items ar ON ar.id = al.parent_id AND ar.type = 'artist' AND ar.deleted_at IS NULL
WHERE ar.id <> sqlc.arg('artist_id')
  AND ar.library_id = sqlc.arg('library_id')
GROUP BY ar.id
ORDER BY score DESC
LIMIT 200;

-- name: ListArtistProfiles :many
-- Every artist of a music library the caller may see, with what similarity
-- is judged on: the genres of the artist and its albums (lower-cased,
-- de-duplicated), the median release year of its albums (original year
-- first), and its track count. Artists with no tracks of their own (a
-- featured-only artist) have a track count of 0.
SELECT ar.id, ar.title, ar.poster_path, ar.updated_at,
       COALESCE((
           SELECT array_agg(DISTINCT lower(btrim(g)))
           FROM (
               SELECT unnest(ar.genres) AS g
               UNION ALL
               SELECT unnest(al.genres)
               FROM media_items al
               WHERE al.parent_id = ar.id AND al.type = 'album' AND al.deleted_at IS NULL
           ) gs
           WHERE btrim(g) <> ''
       ), '{}')::text[] AS genres,
       COALESCE((
           SELECT percentile_disc(0.5) WITHIN GROUP (ORDER BY COALESCE(al.original_year, al.year))
           FROM media_items al
           WHERE al.parent_id = ar.id AND al.type = 'album' AND al.deleted_at IS NULL
             AND COALESCE(al.original_year, al.year) IS NOT NULL
       ), 0)::int AS median_year,
       (
           SELECT COUNT(*)
           FROM media_items t
           JOIN media_items al ON al.id = t.parent_id
           WHERE al.parent_id = ar.id AND al.deleted_at IS NULL
             AND t.type = 'track' AND t.deleted_at IS NULL
       )::bigint AS track_count
FROM media_items ar
WHERE ar.library_id = sqlc.arg('library_id')
  AND ar.type = 'artist'
  AND ar.parent_id IS NULL
  AND ar.deleted_at IS NULL
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(ar.content_rating) <= sqlc.narg('max_rating_rank'))
  AND media_item_labels_permitted(ar.labels, ar.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[]);

-- name: ListMixTracksForArtists :many
-- Up to per_artist random tracks of each listed artist: tracks on its own
-- albums and tracks it is credited on. Carries everything a player queue
-- needs (the best active file and its ReplayGain values) and the caller's
-- own rating, so very low-rated tracks can be left out. The rating ceiling
-- and label restriction apply per track; library access is the handler's.
WITH cand AS (
    SELECT t.id AS track_id, al.parent_id AS mix_artist_id
    FROM media_items t
    JOIN media_items al ON al.id = t.parent_id AND al.deleted_at IS NULL
    WHERE al.parent_id = ANY(sqlc.arg('artist_ids')::uuid[])
      AND t.type = 'track' AND t.deleted_at IS NULL
    UNION
    SELECT ta.track_id, ta.artist_id
    FROM track_artists ta
    JOIN media_items t ON t.id = ta.track_id AND t.type = 'track' AND t.deleted_at IS NULL
    WHERE ta.artist_id = ANY(sqlc.arg('artist_ids')::uuid[])
),
ranked AS (
    SELECT c.track_id, c.mix_artist_id,
           row_number() OVER (PARTITION BY c.mix_artist_id ORDER BY random()) AS rn
    FROM cand c
)
SELECT t.id, t.title, t.duration_ms, t.index,
       r.mix_artist_id,
       al.id AS album_id, al.title AS album_title, al.poster_path AS album_poster_path,
       ar.id AS artist_id, ar.title AS artist_title,
       f.id AS file_id,
       f.replaygain_track_gain, f.replaygain_track_peak,
       f.replaygain_album_gain, f.replaygain_album_peak,
       ur.score AS user_score
FROM ranked r
JOIN media_items t  ON t.id = r.track_id
JOIN media_items al ON al.id = t.parent_id
LEFT JOIN media_items ar ON ar.id = al.parent_id
JOIN LATERAL (
    SELECT mf.id, mf.replaygain_track_gain, mf.replaygain_track_peak,
           mf.replaygain_album_gain, mf.replaygain_album_peak
    FROM media_files mf
    WHERE mf.media_item_id = t.id AND mf.status = 'active'
    ORDER BY mf.lossless DESC NULLS LAST, mf.bitrate DESC NULLS LAST, mf.created_at
    LIMIT 1
) f ON true
LEFT JOIN user_ratings ur ON ur.media_item_id = t.id AND ur.user_id = sqlc.arg('user_id')
WHERE r.rn <= sqlc.arg('per_artist')::int
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(t.content_rating) <= sqlc.narg('max_rating_rank'))
  AND media_item_labels_permitted(t.labels, t.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[]);

-- name: ListMixTracksByGenres :many
-- A random sample of a library's tracks whose album (or the track itself)
-- carries one of the genres (compared lower-cased), leaving out the listed
-- artists' albums — their tracks are already in the mix. Same columns and
-- gates as ListMixTracksForArtists.
SELECT t.id, t.title, t.duration_ms, t.index,
       al.parent_id AS mix_artist_id,
       al.id AS album_id, al.title AS album_title, al.poster_path AS album_poster_path,
       ar.id AS artist_id, ar.title AS artist_title,
       f.id AS file_id,
       f.replaygain_track_gain, f.replaygain_track_peak,
       f.replaygain_album_gain, f.replaygain_album_peak,
       ur.score AS user_score
FROM media_items t
JOIN media_items al ON al.id = t.parent_id AND al.type = 'album' AND al.deleted_at IS NULL
LEFT JOIN media_items ar ON ar.id = al.parent_id
JOIN LATERAL (
    SELECT mf.id, mf.replaygain_track_gain, mf.replaygain_track_peak,
           mf.replaygain_album_gain, mf.replaygain_album_peak
    FROM media_files mf
    WHERE mf.media_item_id = t.id AND mf.status = 'active'
    ORDER BY mf.lossless DESC NULLS LAST, mf.bitrate DESC NULLS LAST, mf.created_at
    LIMIT 1
) f ON true
LEFT JOIN user_ratings ur ON ur.media_item_id = t.id AND ur.user_id = sqlc.arg('user_id')
WHERE t.library_id = sqlc.arg('library_id')
  AND t.type = 'track'
  AND t.deleted_at IS NULL
  AND NOT (COALESCE(al.parent_id, '00000000-0000-0000-0000-000000000000'::uuid) = ANY(sqlc.arg('exclude_artist_ids')::uuid[]))
  AND EXISTS (
      SELECT 1 FROM unnest(COALESCE(al.genres, '{}') || COALESCE(t.genres, '{}')) g
      WHERE lower(btrim(g)) = ANY(sqlc.arg('genres')::text[])
  )
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(t.content_rating) <= sqlc.narg('max_rating_rank'))
  AND media_item_labels_permitted(t.labels, t.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY random()
LIMIT sqlc.arg('result_limit')::int;

-- name: ListMixTracksInAlbum :many
-- An album's tracks for a mix seeded with the album (or one of its tracks),
-- in album order. Same columns and gates as ListMixTracksForArtists.
SELECT t.id, t.title, t.duration_ms, t.index,
       al.parent_id AS mix_artist_id,
       al.id AS album_id, al.title AS album_title, al.poster_path AS album_poster_path,
       ar.id AS artist_id, ar.title AS artist_title,
       f.id AS file_id,
       f.replaygain_track_gain, f.replaygain_track_peak,
       f.replaygain_album_gain, f.replaygain_album_peak,
       ur.score AS user_score
FROM media_items t
JOIN media_items al ON al.id = t.parent_id AND al.deleted_at IS NULL
LEFT JOIN media_items ar ON ar.id = al.parent_id
JOIN LATERAL (
    SELECT mf.id, mf.replaygain_track_gain, mf.replaygain_track_peak,
           mf.replaygain_album_gain, mf.replaygain_album_peak
    FROM media_files mf
    WHERE mf.media_item_id = t.id AND mf.status = 'active'
    ORDER BY mf.lossless DESC NULLS LAST, mf.bitrate DESC NULLS LAST, mf.created_at
    LIMIT 1
) f ON true
LEFT JOIN user_ratings ur ON ur.media_item_id = t.id AND ur.user_id = sqlc.arg('user_id')
WHERE t.parent_id = sqlc.arg('album_id')
  AND t.type = 'track'
  AND t.deleted_at IS NULL
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(t.content_rating) <= sqlc.narg('max_rating_rank'))
  AND media_item_labels_permitted(t.labels, t.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY t.disc_number NULLS FIRST, t.index NULLS LAST, t.title;
