-- name: GetMediaItem :one
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id,
       musicbrainz_id, musicbrainz_release_id, musicbrainz_release_group_id,
       musicbrainz_artist_id, musicbrainz_album_artist_id,
       disc_total, track_total, original_year, compilation, release_type,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetMediaItemByTMDBID :one
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1 AND tmdb_id = $2 AND deleted_at IS NULL
LIMIT 1;

-- name: FindTopLevelItemByTitleYear :one
-- Direct equality lookup matching the unique partial index
-- idx_media_items_library_type_title_year. Used by the scanner's hierarchy
-- find-or-create path so fuzzy full-text search can't miss a show whose
-- title is also present in episode filenames (which would otherwise crowd
-- the LIMITed SearchMediaItems result set).
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND title = $3
  AND COALESCE(year, 0) = COALESCE(sqlc.narg('year')::int, 0)
  AND parent_id IS NULL
  AND deleted_at IS NULL
LIMIT 1;

-- name: FindTopLevelItemsByTitleFlexible :many
-- Scanner fallback for FindTopLevelItemByTitleYear: matches on title OR
-- original_title (case-insensitive) and ignores year. Used when the scanner
-- has no year (raw filename) but enrichment may have set a year on the
-- existing row, or when enrichment renamed the row to a canonical TMDB
-- title. Caller filters by year as a tiebreaker.
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND parent_id IS NULL
  AND deleted_at IS NULL
  AND (lower(title) = lower($3) OR lower(coalesce(original_title, '')) = lower($3))
ORDER BY (tmdb_id IS NOT NULL OR tvdb_id IS NOT NULL) DESC,
         (poster_path IS NOT NULL) DESC,
         created_at ASC
LIMIT 5;

-- name: ListDuplicateTopLevelItems :many
-- Lists groups of top-level media items (movies, shows) in the same library
-- that share a normalized title. Normalization handles the common duplicate
-- causes observed in real libraries: trailing year ("Family Guy 1999"),
-- apostrophes ("Bob's" vs "Bobs"), colons/hyphens ("Dune: Prophecy" vs
-- "Dune Prophecy"), & vs "and" ("Law & Order" vs "Law and Order"), and
-- HTML-escaped ampersands ("Love &amp; Death" vs "Love & Death").
-- Returns one row per loser with the survivor_id. Survivor is the most
-- enriched row (has external IDs > has poster > has year > oldest). Rows
-- whose year conflicts with the survivor's year are NOT merged, so
-- two distinct shows that happen to share a title (e.g. "Heroes" 2006 and
-- "Heroes" 2024) stay separate.
WITH normalized AS (
    SELECT id, library_id, type, year, tmdb_id, tvdb_id, poster_path, created_at,
           lower(
               regexp_replace(
                 regexp_replace(
                   regexp_replace(
                     regexp_replace(
                       -- Strip a release-group prefix in square
                       -- brackets ("[ToonsHub] Frieren...",
                       -- "[QWERTY] 8 Out Of 10 Cats") before any
                       -- other normalization. The scanner pulls
                       -- these in from filenames when the canonical
                       -- show row hasn't been created yet, and the
                       -- prefix is never part of the actual title —
                       -- always safe to drop. Country suffixes like
                       -- "(US)" or " IE" intentionally stay because
                       -- "The Zoo (Ireland)" and "The Zoo" are
                       -- different productions (year mismatch
                       -- already keeps them apart for cases like
                       -- "Heroes 2006" vs "Heroes 2024", but the
                       -- non-parenthesised country tag would
                       -- accidentally merge two real shows).
                       unaccent(replace(replace(
                         regexp_replace(
                           -- Use `title` as the dedup source rather than
                           -- preferring original_title. TMDB-enriched
                           -- rows often have an original_title in the
                           -- production language (e.g. Japanese for an
                           -- anime, German for a foreign film); after
                           -- the `[^a-zA-Z0-9]+` strip below that becomes
                           -- the empty string, the row is excluded by
                           -- `WHERE norm <> ''`, and a duplicate row
                           -- whose title was scanned in English never
                           -- gets folded into the canonical row. title
                           -- is NOT NULL in the schema and is the
                           -- user-facing English label across both rows
                           -- so dedup keys converge correctly.
                           title,
                           '^\s*\[[^\]]+\]\s*', '', 'i'
                         ),
                         '&amp;', '&'), '''', '')
                       ),
                       '^\s*(the|a|an)\s+', '', 'i'
                     ),
                     '[\s\-]+[\(\[]?(19|20)\d{2}[\)\]]?\s*$', ''
                   ),
                   '\s+(and|&)\s+', 'and', 'gi'
                 ),
                 '[^a-zA-Z0-9]+', '', 'g'
               )
           ) AS norm
    FROM media_items
    WHERE type = $1
      AND parent_id IS NULL
      AND deleted_at IS NULL
      AND (sqlc.narg('library_id')::uuid IS NULL OR library_id = sqlc.narg('library_id'))
),
ranked AS (
    SELECT id, library_id, norm, year,
           FIRST_VALUE(id)   OVER w AS survivor_id,
           FIRST_VALUE(year) OVER w AS survivor_year,
           ROW_NUMBER()      OVER w AS rn
    FROM normalized
    WHERE norm <> ''
    WINDOW w AS (
        PARTITION BY library_id, norm
        ORDER BY (tmdb_id IS NOT NULL OR tvdb_id IS NOT NULL) DESC,
                 (poster_path IS NOT NULL) DESC,
                 (year IS NOT NULL) DESC,
                 created_at ASC,
                 id ASC
    )
)
SELECT id AS loser_id, survivor_id::uuid AS survivor_id
FROM ranked
WHERE rn > 1
  AND (year IS NULL OR survivor_year IS NULL OR year = survivor_year);

-- name: ListPrefixDuplicateTopLevelItems :many
-- Second-pass dedupe for the "folder name kept the official subtitle" case
-- where the unenriched row's normalized title starts with the enriched row's
-- normalized title at a word boundary (e.g. "adventure time with finn and
-- jake" → "adventure time" 2010). Conservative on purpose: the loser must
-- have NO external IDs and NO year, so a real spin-off that has been
-- enriched (e.g. "Naruto Shippuden" with its own tmdb_id) won't be folded
-- into the parent show.
WITH normalized AS (
    SELECT id, library_id, type, year, tmdb_id, tvdb_id, poster_path, created_at,
           lower(
               regexp_replace(
                 regexp_replace(
                   regexp_replace(
                     regexp_replace(
                       -- Strip a release-group prefix in square
                       -- brackets ("[ToonsHub] Frieren...",
                       -- "[QWERTY] 8 Out Of 10 Cats") before any
                       -- other normalization. The scanner pulls
                       -- these in from filenames when the canonical
                       -- show row hasn't been created yet, and the
                       -- prefix is never part of the actual title —
                       -- always safe to drop. Country suffixes like
                       -- "(US)" or " IE" intentionally stay because
                       -- "The Zoo (Ireland)" and "The Zoo" are
                       -- different productions (year mismatch
                       -- already keeps them apart for cases like
                       -- "Heroes 2006" vs "Heroes 2024", but the
                       -- non-parenthesised country tag would
                       -- accidentally merge two real shows).
                       unaccent(replace(replace(
                         regexp_replace(
                           -- See ListDuplicateTopLevelItems for why this is
                           -- `title` and not `coalesce(original_title, title)`:
                           -- CJK / non-Latin original titles strip to empty
                           -- and exclude their row from dedup entirely.
                           title,
                           '^\s*\[[^\]]+\]\s*', '', 'i'
                         ),
                         '&amp;', '&'), '''', '')
                       ),
                       '^\s*(the|a|an)\s+', '', 'i'
                     ),
                     '[\s\-]+[\(\[]?(19|20)\d{2}[\)\]]?\s*$', ''
                   ),
                   '\s+(and|&)\s+', 'and', 'gi'
                 ),
                 '[^a-zA-Z0-9]+', '', 'g'
               )
           ) AS norm
    FROM media_items
    WHERE type = $1
      AND parent_id IS NULL
      AND deleted_at IS NULL
      AND (sqlc.narg('library_id')::uuid IS NULL OR library_id = sqlc.narg('library_id'))
),
losers AS (
    SELECT id, library_id, norm
    FROM normalized
    WHERE tmdb_id IS NULL AND tvdb_id IS NULL AND year IS NULL
      AND norm <> ''
),
survivors AS (
    SELECT id, library_id, norm, poster_path, created_at
    FROM normalized
    WHERE (tmdb_id IS NOT NULL OR tvdb_id IS NOT NULL)
      AND norm <> ''
)
SELECT DISTINCT ON (l.id)
       l.id  AS loser_id,
       s.id::uuid AS survivor_id
FROM losers l
JOIN survivors s
  ON s.library_id = l.library_id
 AND l.norm LIKE s.norm || ' %'
ORDER BY l.id,
         length(s.norm) DESC,
         (s.poster_path IS NOT NULL) DESC,
         s.created_at ASC,
         s.id ASC;

-- name: ListDuplicateChildItems :many
-- Lists duplicate parented media items (e.g. albums under an artist) that
-- share a normalized title within the same parent. Used by music dedupe to
-- collapse variant album rows caused by inconsistent tag spellings across
-- tracks (e.g. "Abbey Road" vs "Abbey Road (Remastered)"). Normalization
-- matches ListDuplicateTopLevelItems: strips articles, trailing years,
-- ampersand/and, and non-alphanumeric characters. Survivor is the most
-- enriched row (external ids > poster > year > oldest). Year mismatches
-- block the merge so a re-release with a different year stays distinct.
WITH normalized AS (
    SELECT id, parent_id, type, year, tmdb_id, tvdb_id, musicbrainz_id,
           poster_path, created_at,
           lower(
               regexp_replace(
                 regexp_replace(
                   regexp_replace(
                     regexp_replace(
                       unaccent(replace(replace(coalesce(NULLIF(original_title, ''), title), '&amp;', '&'), '''', '')),
                       '^\s*(the|a|an)\s+', '', 'i'
                     ),
                     '[\s\-]+[\(\[]?(19|20)\d{2}[\)\]]?\s*$', ''
                   ),
                   '\s+(and|&)\s+', 'and', 'gi'
                 ),
                 '[^a-zA-Z0-9]+', '', 'g'
               )
           ) AS norm
    FROM media_items
    WHERE type = $1
      AND parent_id IS NOT NULL
      AND deleted_at IS NULL
      AND (sqlc.narg('parent_id')::uuid IS NULL OR parent_id = sqlc.narg('parent_id'))
),
ranked AS (
    SELECT id, parent_id, norm, year,
           FIRST_VALUE(id)   OVER w AS survivor_id,
           FIRST_VALUE(year) OVER w AS survivor_year,
           ROW_NUMBER()      OVER w AS rn
    FROM normalized
    WHERE norm <> ''
    WINDOW w AS (
        PARTITION BY parent_id, norm
        ORDER BY (tmdb_id IS NOT NULL OR tvdb_id IS NOT NULL OR musicbrainz_id IS NOT NULL) DESC,
                 (poster_path IS NOT NULL) DESC,
                 (year IS NOT NULL) DESC,
                 created_at ASC,
                 id ASC
    )
)
SELECT id AS loser_id, survivor_id::uuid AS survivor_id
FROM ranked
WHERE rn > 1
  AND (year IS NULL OR survivor_year IS NULL OR year = survivor_year);

-- name: ListCollabArtistMerges :many
-- Finds artists whose title matches a collaboration pattern and whose
-- primary OR secondary name already exists as a separate standalone
-- artist in the same library. Returns (loser_id, survivor_id) pairs so
-- the caller can reparent children and soft-delete losers via the
-- existing merge plumbing. Conservative: only merges when at least one
-- side is an actual row — so "Simon & Garfunkel" (no "Simon" row, no
-- "Garfunkel" row) is left alone.
--
-- Two-sided matching: tries the LEFT name (everything before the first
-- separator) first, then falls back to the RIGHT name (everything after
-- the LAST separator). The right-side fallback catches "X & Famous"
-- rows where the famous guest is the canonical the library knows about
-- and X is a one-off feature; without it, "Glen Campbell & Elton John"
-- stays orphaned forever because no Glen Campbell row exists.
--
-- Separator set: comma, slash, "&", "and", "feat", "ft", "featuring",
-- "with", and " - " (whitespace-bounded hyphen, for the "Bo Diddley -
-- Muddy Waters - Little Walter" multi-artist tag style). Naked
-- in-name hyphens like Wu-Tang Clan or Jay-Z are unaffected because
-- the separator requires whitespace on both sides.
--
-- DISTINCT ON dedupes per-loser when both halves match an existing
-- canonical; the ORDER BY makes the left match win, matching the
-- existing precedence convention.
WITH collabs AS (
    SELECT c.id AS collab_id,
           c.library_id,
           unaccent(regexp_replace(
               c.title,
               '(\s*,\s*|\s*/\s*|\s+(&|and|feat\.?|ft\.?|featuring|with)\s+|\s+-\s+).+$',
               '',
               'i'
           )) AS left_primary,
           unaccent(regexp_replace(
               c.title,
               '^.+(\s*,\s*|\s*/\s*|\s+(&|and|feat\.?|ft\.?|featuring|with)\s+|\s+-\s+)',
               '',
               'i'
           )) AS right_primary
    FROM media_items c
    WHERE c.type = 'artist'
      AND c.parent_id IS NULL
      AND c.deleted_at IS NULL
      AND c.title ~* '(\s*,\s*|\s*/\s*|\s+(&|and|feat\.?|ft\.?|featuring|with)\s+|\s+-\s+)'
      AND (sqlc.narg('library_id')::uuid IS NULL OR c.library_id = sqlc.narg('library_id'))
)
SELECT DISTINCT ON (c.collab_id)
       c.collab_id AS loser_id,
       p.id::uuid  AS survivor_id
FROM collabs c
JOIN media_items p
  ON p.type = 'artist'
 AND p.parent_id IS NULL
 AND p.deleted_at IS NULL
 AND p.library_id = c.library_id
 AND p.id <> c.collab_id
 AND (
     lower(unaccent(p.title)) = lower(c.left_primary)
     OR lower(unaccent(p.title)) = lower(c.right_primary)
 )
ORDER BY c.collab_id,
         -- left-match first (TRUE sorts before FALSE under DESC).
         (lower(unaccent(p.title)) = lower(c.left_primary)) DESC;

-- name: ReparentMediaItem :exec
UPDATE media_items
SET parent_id  = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: ReparentMediaFilesByItem :exec
-- Reassigns every media_file pointing at $1 to point at $2 instead.
-- Used when merging two duplicate episode rows.
UPDATE media_files
SET media_item_id = $2,
    scanned_at    = NOW()
WHERE media_item_id = $1;

-- name: ListMediaItems :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
ORDER BY sort_title
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByTMDBIDs :many
-- Returns the (id, library_id, tmdb_id) for every top-level media item that
-- matches one of the supplied TMDB IDs for the given type. Used by Discover
-- to mark search results as already-in-library in a single round-trip rather
-- than per-result. Library scope is library-agnostic — Discover surfaces
-- "available somewhere" regardless of which specific library the title is in.
SELECT id, library_id, tmdb_id
FROM media_items
WHERE type = $1
  AND tmdb_id = ANY(sqlc.arg('tmdb_ids')::int[])
  AND parent_id IS NULL
  AND deleted_at IS NULL;

-- name: ListMediaItemsMissingArt :many
-- Returns top-level items (movies + shows) that have no poster so the
-- maintenance backfill can re-run metadata enrichment on them. Seasons and
-- episodes are excluded — enriching a show cascades down to them.
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE type IN ('movie', 'show')
  AND poster_path IS NULL
  AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $1;

-- name: ListMediaItemChildren :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE parent_id = $1 AND deleted_at IS NULL
ORDER BY index
LIMIT 1000;

-- name: CreateMediaItem :one
INSERT INTO media_items (
    library_id, type, title, sort_title, original_title, year,
    summary, tagline, rating, audience_rating, content_rating, duration_ms,
    genres, tags, tmdb_id, tvdb_id, imdb_id,
    musicbrainz_id, musicbrainz_release_id, musicbrainz_release_group_id,
    musicbrainz_artist_id, musicbrainz_album_artist_id,
    disc_total, track_total, original_year, compilation, release_type,
    parent_id, index,
    poster_path, fanart_path, thumb_path,
    originally_available_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12,
    $13, $14, $15, $16, $17,
    $18, $19, $20,
    $21, $22,
    $23, $24, $25, $26, $27,
    $28, $29,
    $30, $31, $32,
    $33
)
RETURNING id, library_id, type, title, sort_title, original_title, year,
          summary, tagline, rating, audience_rating, content_rating, duration_ms,
          genres, tags, tmdb_id, tvdb_id, imdb_id,
          musicbrainz_id, musicbrainz_release_id, musicbrainz_release_group_id,
          musicbrainz_artist_id, musicbrainz_album_artist_id,
          disc_total, track_total, original_year, compilation, release_type,
          parent_id, index, poster_path, fanart_path, thumb_path,
          originally_available_at, created_at, updated_at, deleted_at;

-- name: ListUnmatchedTopLevelItems :many
-- Top-level (parent_id IS NULL) movies + shows that have NO external IDs
-- (tmdb_id / tvdb_id / imdb_id all NULL). Used by the admin "re-enrich
-- unmatched" tool to recover items the scanner couldn't match — typically
-- shows whose stored title still has a `[release-group]` prefix that
-- poisoned the TMDB search query before the cleanTitle bracket-strip
-- landed. Caller cleans the title via Go-side cleanTitle() and re-queues
-- enrichment per item.
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE parent_id IS NULL
  AND deleted_at IS NULL
  AND type IN ('movie', 'show')
  AND tmdb_id IS NULL
  AND tvdb_id IS NULL
  AND imdb_id IS NULL
  AND (sqlc.narg('library_id')::uuid IS NULL OR library_id = sqlc.narg('library_id'))
ORDER BY created_at ASC
LIMIT sqlc.arg('result_limit')::int;

-- name: UpdateMediaItemTitle :exec
-- Narrow update used by the admin re-enrich-unmatched tool: rewrites only
-- the title + sort_title without touching the metadata fields that
-- UpdateMediaItemMetadata would overwrite. Lets the operator clean a
-- bracket-prefixed title before the enricher runs, so even if TMDB still
-- can't match the show, the title is at least readable.
UPDATE media_items
SET title      = $2,
    sort_title = $3,
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateMediaItemMetadata :one
UPDATE media_items
SET title                   = $2,
    sort_title              = $3,
    original_title          = $4,
    year                    = $5,
    summary                 = $6,
    tagline                 = $7,
    rating                  = $8,
    audience_rating         = $9,
    content_rating          = $10,
    duration_ms             = $11,
    genres                  = $12,
    tags                    = $13,
    -- COALESCE on the art paths: the enricher only sets these when
    -- it has a new URL to download. Without this guard, a nil from
    -- the agent (e.g. TheAudioDB miss + CAA unreachable for an
    -- obscure 2026 collector's edition) wiped whatever the scanner
    -- had already extracted from the disk-side cover.jpg /
    -- folder.jpg. Mirrors the existing tmdb_id / tvdb_id behaviour.
    poster_path             = COALESCE($14, poster_path),
    fanart_path             = COALESCE($15, fanart_path),
    thumb_path              = COALESCE($16, thumb_path),
    originally_available_at = $17,
    tmdb_id                 = COALESCE($18, tmdb_id),
    tvdb_id                 = COALESCE($19, tvdb_id),
    updated_at              = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, library_id, type, title, sort_title, original_title, year,
          summary, tagline, rating, audience_rating, content_rating, duration_ms,
          genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
          parent_id, index, poster_path, fanart_path, thumb_path,
          originally_available_at, created_at, updated_at, deleted_at;

-- name: SoftDeleteMediaItem :exec
UPDATE media_items SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1;

-- name: SoftDeleteMediaItemsByLibrary :exec
UPDATE media_items SET deleted_at = NOW(), updated_at = NOW()
WHERE library_id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteMediaItemIfAllFilesDeleted :exec
UPDATE media_items
SET deleted_at = NOW(), updated_at = NOW()
WHERE media_items.id = $1
  AND NOT EXISTS (
      SELECT 1 FROM media_files
      WHERE media_files.media_item_id = $1 AND media_files.status != 'deleted'
  );

-- name: RestoreMediaItemAncestry :exec
-- Clears deleted_at on $1 and every ancestor reachable via parent_id,
-- resurrecting a previously soft-deleted item and the containers above it.
-- Called by the scanner when a file for this item transitions from
-- missing/deleted back to active, so a transient missing window (e.g. a
-- disconnected NAS) doesn't permanently hide a show that still has files
-- on disk. A no-op when the chain is already alive.
WITH RECURSIVE ancestry AS (
    SELECT mi.id AS ancestor_id, mi.parent_id
    FROM media_items mi
    WHERE mi.id = $1
    UNION ALL
    SELECT mi.id AS ancestor_id, mi.parent_id
    FROM media_items mi
    JOIN ancestry a ON mi.id = a.parent_id
)
UPDATE media_items
SET deleted_at = NULL, updated_at = NOW()
WHERE media_items.id IN (SELECT ancestor_id FROM ancestry)
  AND media_items.deleted_at IS NOT NULL;

-- name: CountMediaItems :one
SELECT COUNT(*) FROM media_items
WHERE library_id = $1 AND type = $2 AND deleted_at IS NULL;

-- name: SearchMediaItems :many
-- websearch_to_tsquery is more forgiving than plainto_tsquery: it accepts
-- "quoted phrases", -negation, and OR — the syntax users intuitively try.
-- Trigram fallback (% operator) catches typos and foreign titles that the
-- english stemmer can't match. Final rank is the GREATEST of FTS and trigram
-- similarities so an exact lexical match still beats a fuzzy one.
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND deleted_at IS NULL
  AND (
        search_vector @@ websearch_to_tsquery('english', $2)
     OR title % $2
     OR (original_title IS NOT NULL AND original_title % $2)
  )
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY GREATEST(
    ts_rank(search_vector, websearch_to_tsquery('english', $2)),
    similarity(title, $2),
    COALESCE(similarity(original_title, $2), 0)
) DESC
LIMIT $3;

-- name: SearchMediaItemsGlobal :many
-- See SearchMediaItems for query semantics. Global variant drops the
-- library_id filter; per-user library access is enforced in the handler.
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE deleted_at IS NULL
  AND (
        search_vector @@ websearch_to_tsquery('english', $1)
     OR title % $1
     OR (original_title IS NOT NULL AND original_title % $1)
  )
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY GREATEST(
    ts_rank(search_vector, websearch_to_tsquery('english', $1)),
    similarity(title, $1),
    COALESCE(similarity(original_title, $1), 0)
) DESC
LIMIT $2;

-- name: ListMediaItemsByTitle :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY sort_title ASC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByTitleDesc :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY sort_title DESC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByYear :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY year ASC NULLS LAST, sort_title ASC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByYearDesc :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY year DESC NULLS LAST, sort_title ASC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByRating :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY rating DESC NULLS LAST, sort_title ASC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByRatingAsc :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY rating ASC NULLS LAST, sort_title ASC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByDateAdded :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByDateAddedAsc :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY created_at ASC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByTakenAt :many
-- Sort by originally_available_at DESC. Photos mirror EXIF DateTimeOriginal
-- onto this column at scan time, so this is the natural "Date taken" sort.
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY originally_available_at DESC NULLS LAST, created_at DESC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByTakenAtAsc :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY originally_available_at ASC NULLS LAST, created_at ASC
LIMIT $3 OFFSET $4;

-- name: CountMediaItemsFiltered :one
SELECT COUNT(*) FROM media_items
WHERE library_id = $1 AND type = $2 AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'));

-- name: ListDistinctGenres :many
SELECT DISTINCT g::text AS genre
FROM media_items, unnest(genres) AS g
WHERE library_id = $1 AND deleted_at IS NULL
ORDER BY genre;

-- name: ListGenresWithCounts :many
-- Returns each distinct genre and the number of root-type items that carry it.
-- Filtering by type avoids inflating counts when episodes inherit show genres.
SELECT g::text AS genre, COUNT(*)::bigint AS count
FROM media_items, unnest(genres) AS g
WHERE library_id = $1 AND type = $2 AND deleted_at IS NULL
GROUP BY g
ORDER BY g;

-- name: ListYearsWithCounts :many
-- Returns distinct release years and item counts for the given library/type.
-- NULL years are excluded so the browse UI doesn't show an empty bucket.
SELECT year::int AS year, COUNT(*)::bigint AS count
FROM media_items
WHERE library_id = $1 AND type = $2 AND deleted_at IS NULL AND year IS NOT NULL
GROUP BY year
ORDER BY year DESC;

-- name: ListHubRecentlyAdded :many
SELECT library_id, media_id, type, title, year, rating, poster_path, created_at
FROM hub_recently_added
WHERE library_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: RefreshHubRecentlyAdded :exec
REFRESH MATERIALIZED VIEW CONCURRENTLY hub_recently_added;

-- name: ListRecentlyAdded :many
-- "Recently Added" hub row, one row per logical content event:
--   - For TV libraries: one tile per show, ordered by the most-recently-
--     added episode's created_at. The tile renders as the show (poster +
--     show title); the row's `created_at` is the latest episode's
--     timestamp so a show that just received a new episode bubbles to
--     the top. New episodes for already-known shows surface alongside
--     brand-new shows.
--   - For movies / albums / photos: one row per item ordered by
--     created_at (unchanged from pre-v2.1).
--
-- Performance: the show-anchored query (per-show MAX(episode.created_at)
-- via GROUP BY) is dramatically cheaper than the episode-anchored
-- alternative (window function over a 500-row episode pre-filter, joined
-- to grandparent). The TV branch only iterates shows-in-scope (~hundreds
-- per library), aggregating their episodes via the existing partial
-- parent_id index. Pre-v2.1 the row was just `type = 'show'` ordered by
-- show.created_at; we keep that shape and swap the sort key to the
-- max-episode timestamp so "new content for an existing show" surfaces.
WITH show_recency AS (
    SELECT show.id, show.library_id, show.type, show.title, show.sort_title,
           show.original_title, show.year, show.summary, show.tagline,
           show.rating, show.audience_rating, show.content_rating, show.duration_ms,
           show.genres, show.tags, show.tmdb_id, show.tvdb_id, show.imdb_id,
           show.musicbrainz_id, show.parent_id, show.index, show.poster_path,
           show.fanart_path, show.thumb_path, show.originally_available_at,
           show.updated_at, show.deleted_at,
           show.poster_path AS fallback_poster,
           COALESCE(MAX(episode.created_at), show.created_at) AS recent_at
    FROM media_items show
    LEFT JOIN media_items season ON season.parent_id = show.id
        AND season.type = 'season' AND season.deleted_at IS NULL
    LEFT JOIN media_items episode ON episode.parent_id = season.id
        AND episode.type = 'episode' AND episode.deleted_at IS NULL
    WHERE show.type = 'show' AND show.deleted_at IS NULL
      AND show.poster_path IS NOT NULL
      AND (sqlc.narg('library_id')::uuid IS NULL OR show.library_id = sqlc.narg('library_id'))
      AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(show.content_rating) <= sqlc.narg('max_rating_rank'))
    GROUP BY show.id
)
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at,
       fallback_poster
FROM (
    SELECT id, library_id, type, title, sort_title, original_title, year,
           summary, tagline, rating, audience_rating, content_rating, duration_ms,
           genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
           parent_id, index, poster_path, fanart_path, thumb_path,
           originally_available_at, recent_at AS created_at, updated_at, deleted_at,
           fallback_poster
    FROM show_recency

    UNION ALL

    SELECT id, library_id, type, title, sort_title, original_title, year,
           summary, tagline, rating, audience_rating, content_rating, duration_ms,
           genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
           parent_id, index, poster_path, fanart_path, thumb_path,
           originally_available_at, created_at, updated_at, deleted_at,
           poster_path AS fallback_poster
    FROM media_items
    WHERE deleted_at IS NULL
      AND type IN ('movie', 'album', 'photo')
      AND poster_path IS NOT NULL
      AND (sqlc.narg('library_id')::uuid IS NULL OR library_id = sqlc.narg('library_id'))
      AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
) combined
ORDER BY created_at DESC
LIMIT sqlc.arg('limit');

-- name: ListContinueWatching :many
-- For movies, every in-progress row passes through. For episodes,
-- only the most-recently-watched episode per show is kept — the
-- user wanted Continue Watching TV Shows to surface one tile per
-- show, not a wall of three episodes from the same series. The
-- "show key" is grandparent.id when present (the standard
-- show → season → episode chain) and falls back to parent.id for
-- the rare flat-layout episode that hangs directly off a show
-- without a season row.
WITH rows AS (
    SELECT m.id, m.library_id, m.type, m.title, m.sort_title,
           m.original_title, m.year, m.summary, m.tagline, m.rating, m.audience_rating,
           m.content_rating, m.duration_ms, m.genres, m.tags, m.tmdb_id, m.tvdb_id, m.imdb_id,
           m.musicbrainz_id, m.parent_id, m.index, m.poster_path, m.fanart_path, m.thumb_path,
           m.originally_available_at, m.created_at, m.updated_at, m.deleted_at,
           ws.position_ms AS view_offset,
           ws.duration_ms AS view_duration,
           ws.last_watched_at,
           COALESCE(grandparent.poster_path, parent.poster_path, m.poster_path,
                    grandparent.thumb_path, parent.thumb_path, m.thumb_path) AS fallback_poster,
           CASE
               WHEN m.type = 'episode'
                   THEN COALESCE(grandparent.id, parent.id, m.id)
               ELSE m.id
           END AS show_key
    FROM watch_state ws
    JOIN media_items m ON m.id = ws.media_id
    LEFT JOIN media_items parent ON parent.id = m.parent_id
    LEFT JOIN media_items grandparent ON grandparent.id = parent.parent_id
    WHERE ws.user_id = $1
      AND ws.status = 'in_progress'
      AND m.deleted_at IS NULL
      AND m.type IN ('movie', 'episode')
      AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(m.content_rating) <= sqlc.narg('max_rating_rank'))
),
deduped AS (
    SELECT id, library_id, type, title, sort_title, original_title, year, summary, tagline,
           rating, audience_rating, content_rating, duration_ms, genres, tags,
           tmdb_id, tvdb_id, imdb_id, musicbrainz_id, parent_id, index, poster_path,
           fanart_path, thumb_path, originally_available_at, created_at, updated_at, deleted_at,
           view_offset, view_duration, last_watched_at, fallback_poster
    FROM (
        SELECT *,
               ROW_NUMBER() OVER (PARTITION BY show_key ORDER BY last_watched_at DESC) AS rn
        FROM rows
    ) t
    WHERE t.rn = 1
)
SELECT id, library_id, type, title, sort_title, original_title, year, summary, tagline,
       rating, audience_rating, content_rating, duration_ms, genres, tags,
       tmdb_id, tvdb_id, imdb_id, musicbrainz_id, parent_id, index, poster_path,
       fanart_path, thumb_path, originally_available_at, created_at, updated_at, deleted_at,
       view_offset, view_duration, fallback_poster
FROM deduped
ORDER BY last_watched_at DESC
LIMIT $2;

-- name: ListMediaItemsForSmartPlaylist :many
-- Cross-library filter for smart playlists (collections.type =
-- 'smart_playlist'). Same filter shape as ListMediaItemsFiltered but
-- without the library scope and with the type as an array (so a single
-- smart playlist can mix movies + episodes — an obvious "watch
-- everything from director X" use case otherwise impossible).
--
-- The handler enforces library ACL above this query (passes the
-- user's accessible library_ids as the array). Sort is fixed to
-- title for v2.1 Stage 1 — multi-sort variants land alongside the
-- visual rule builder in v2.2 once the grammar's stable.
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = ANY(sqlc.arg('library_ids')::uuid[])
  AND type = ANY(sqlc.arg('types')::text[])
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY sort_title
LIMIT sqlc.arg('result_limit')::int;

-- name: ListTrending :many
-- Top-N items watched across all users within a rolling window. Counts
-- distinct users per item so one binge-watcher can't dominate the row;
-- ties broken by total event count. Filters mirror ContinueWatching:
-- only playable types (movie / episode), only items still present
-- (deleted_at IS NULL), parental rating ceiling enforced.
--
-- The trending row is global (not per-user) — same content shown to
-- every user. Library access is filtered out in the handler since
-- the query doesn't know the caller's grant set.
--
-- Window is passed as an integer day count (typed via int4) so
-- callers can swap 7 / 30 / 365 without a query rewrite. make_interval
-- gives postgres the typed interval the comparison needs.
SELECT m.id, m.library_id, m.type, m.title,
       m.year, m.poster_path, m.fanart_path, m.thumb_path,
       m.duration_ms, m.updated_at,
       COUNT(DISTINCT we.user_id) AS unique_viewers,
       COUNT(*) AS total_events
FROM media_items m
JOIN watch_events we ON we.media_id = m.id
WHERE m.deleted_at IS NULL
  AND m.type IN ('movie', 'episode')
  AND we.event_type IN ('play', 'scrobble', 'stop')
  AND we.occurred_at >= NOW() - make_interval(days => sqlc.arg('window_days')::int)
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(m.content_rating) <= sqlc.narg('max_rating_rank'))
GROUP BY m.id
ORDER BY unique_viewers DESC, total_events DESC, m.updated_at DESC
LIMIT sqlc.arg('result_limit')::int;

-- ── Media Files ───────────────────────────────────────────────────────────────

-- name: GetMediaFile :one
SELECT id, media_item_id, file_path, file_size, container, video_codec,
       audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
       audio_streams, subtitle_streams, chapters, file_hash,
       status, missing_since, scanned_at, created_at, duration_ms,
       bit_depth, sample_rate, channel_layout, lossless,
       replaygain_track_gain, replaygain_track_peak,
       replaygain_album_gain, replaygain_album_peak
FROM media_files
WHERE id = $1;

-- name: GetMediaFileByPath :one
SELECT id, media_item_id, file_path, file_size, container, video_codec,
       audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
       audio_streams, subtitle_streams, chapters, file_hash,
       status, missing_since, scanned_at, created_at, duration_ms,
       bit_depth, sample_rate, channel_layout, lossless,
       replaygain_track_gain, replaygain_track_peak,
       replaygain_album_gain, replaygain_album_peak
FROM media_files
WHERE file_path = $1;

-- name: GetMediaFileByHash :one
SELECT id, media_item_id, file_path, file_size, container, video_codec,
       audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
       audio_streams, subtitle_streams, chapters, file_hash,
       status, missing_since, scanned_at, created_at, duration_ms,
       bit_depth, sample_rate, channel_layout, lossless,
       replaygain_track_gain, replaygain_track_peak,
       replaygain_album_gain, replaygain_album_peak
FROM media_files
WHERE file_hash = $1 AND status IN ('missing', 'deleted')
ORDER BY created_at DESC
LIMIT 1;

-- name: ListMediaFilesForItem :many
SELECT id, media_item_id, file_path, file_size, container, video_codec,
       audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
       audio_streams, subtitle_streams, chapters, file_hash,
       status, missing_since, scanned_at, created_at, duration_ms,
       bit_depth, sample_rate, channel_layout, lossless,
       replaygain_track_gain, replaygain_track_peak,
       replaygain_album_gain, replaygain_album_peak
FROM media_files
WHERE media_item_id = $1 AND status = 'active'
ORDER BY (resolution_w * resolution_h * COALESCE(bitrate, 0)) DESC;  -- best quality first (ADR-031)

-- name: CreateMediaFile :one
INSERT INTO media_files (
    media_item_id, file_path, file_size, container, video_codec,
    audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
    audio_streams, subtitle_streams, chapters, file_hash, duration_ms,
    bit_depth, sample_rate, channel_layout, lossless,
    replaygain_track_gain, replaygain_track_peak,
    replaygain_album_gain, replaygain_album_peak
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10, $11,
    $12, $13, $14, $15, $16,
    $17, $18, $19, $20,
    $21, $22,
    $23, $24
)
RETURNING id, media_item_id, file_path, file_size, container, video_codec,
          audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
          audio_streams, subtitle_streams, chapters, file_hash,
          status, missing_since, scanned_at, created_at, duration_ms,
          bit_depth, sample_rate, channel_layout, lossless,
          replaygain_track_gain, replaygain_track_peak,
          replaygain_album_gain, replaygain_album_peak;

-- name: UpdateMediaFilePath :exec
UPDATE media_files
SET file_path     = $2,
    status        = 'active',
    missing_since = NULL,
    scanned_at    = NOW()
WHERE id = $1;

-- name: MarkMediaFileMissing :exec
UPDATE media_files
SET status        = 'missing',
    missing_since = NOW()
WHERE id = $1 AND status = 'active';

-- name: MarkMediaFileActive :exec
UPDATE media_files
SET status        = 'active',
    missing_since = NULL,
    scanned_at    = NOW()
WHERE id = $1;

-- name: MarkMediaFileDeleted :exec
UPDATE media_files
SET status = 'deleted'
WHERE id = $1;

-- name: UpdateMediaFileHash :exec
UPDATE media_files
SET file_hash  = $2,
    scanned_at = NOW()
WHERE id = $1;

-- name: UpdateMediaFileItemID :exec
UPDATE media_files
SET media_item_id = $2,
    scanned_at    = NOW()
WHERE id = $1;

-- name: UpdateMediaFileTechnicalMetadata :exec
UPDATE media_files
SET container        = $2,
    video_codec      = $3,
    audio_codec      = $4,
    resolution_w     = $5,
    resolution_h     = $6,
    bitrate          = $7,
    hdr_type         = $8,
    frame_rate       = $9,
    audio_streams    = $10,
    subtitle_streams = $11,
    chapters         = $12,
    duration_ms      = $13,
    scanned_at       = NOW()
WHERE id = $1;

-- name: ListActiveFilesForLibrary :many
SELECT mf.id, mf.media_item_id, mf.file_path, mf.file_size, mf.container, mf.video_codec,
       mf.audio_codec, mf.resolution_w, mf.resolution_h, mf.bitrate, mf.hdr_type, mf.frame_rate,
       mf.audio_streams, mf.subtitle_streams, mf.chapters, mf.file_hash,
       mf.status, mf.missing_since, mf.scanned_at, mf.created_at, mf.duration_ms,
       mf.bit_depth, mf.sample_rate, mf.channel_layout, mf.lossless,
       mf.replaygain_track_gain, mf.replaygain_track_peak,
       mf.replaygain_album_gain, mf.replaygain_album_peak
FROM media_files mf
JOIN media_items mi ON mi.id = mf.media_item_id
WHERE mi.library_id = $1 AND mf.status = 'active';

-- name: DeleteMissingFilesByLibrary :exec
UPDATE media_files
SET status = 'deleted'
WHERE status = 'missing'
  AND media_item_id IN (
      SELECT id FROM media_items WHERE library_id = $1 AND deleted_at IS NULL
  );

-- name: GetMediaItemEnrichAttemptedAt :one
-- Returns the timestamp of the last metadata-enrichment attempt (TMDB/TVDB
-- lookup + artwork fetch), or NULL if never attempted. Used by the scanner
-- to suppress retries for items whose lookup failed recently.
SELECT last_enrich_attempted_at
FROM media_items
WHERE id = $1;

-- name: TouchMediaItemEnrichAttempt :exec
-- Marks the item as having been through an enrichment pass, whether or not
-- anything was found. Call this after every Enrich() attempt so the negative
-- cache ticks forward and we don't hammer TMDB for titles it doesn't have.
UPDATE media_items
SET last_enrich_attempted_at = NOW()
WHERE id = $1;

-- name: HardDeleteSoftDeletedFilesByLibrary :execrows
-- Permanently removes media_files rows with status='deleted' for a library.
-- Runs after CleanupMissingFiles so all no-longer-present files (missing grace
-- period expired) get promoted to deleted and then hard-purged in one scan.
-- watch_events.file_id uses ON DELETE SET NULL, so history is preserved.
DELETE FROM media_files
WHERE status = 'deleted'
  AND media_item_id IN (
      SELECT id FROM media_items WHERE library_id = $1
  );

-- name: SoftDeleteItemsWithNoActiveFiles :exec
-- Soft-delete leaf items (those that own files directly) with no active files.
-- Container types (show, season, artist, album) never own files — they're
-- handled by SoftDeleteEmptyContainerItems instead.
UPDATE media_items
SET deleted_at = NOW(), updated_at = NOW()
WHERE library_id = $1
  AND deleted_at IS NULL
  AND type IN ('movie', 'episode', 'track', 'photo')
  AND NOT EXISTS (
      SELECT 1 FROM media_files
      WHERE media_files.media_item_id = media_items.id AND media_files.status = 'active'
  );

-- name: SoftDeleteEmptyContainerItems :exec
-- Soft-delete container items (show, season, artist, album) whose every
-- child has been soft-deleted. Call twice in sequence to cascade up: the
-- first pass clears empty seasons/albums, the second clears shows/artists
-- whose seasons/albums just died.
UPDATE media_items AS parent
SET deleted_at = NOW(), updated_at = NOW()
WHERE parent.library_id = $1
  AND parent.deleted_at IS NULL
  AND parent.type IN ('show', 'season', 'artist', 'album')
  AND NOT EXISTS (
      SELECT 1 FROM media_items child
      WHERE child.parent_id = parent.id AND child.deleted_at IS NULL
  );

-- name: ListMissingFilesOlderThan :many
SELECT id, media_item_id, file_path, file_size, container, video_codec,
       audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
       audio_streams, subtitle_streams, chapters, file_hash,
       status, missing_since, scanned_at, created_at, duration_ms,
       bit_depth, sample_rate, channel_layout, lossless,
       replaygain_track_gain, replaygain_track_peak,
       replaygain_album_gain, replaygain_album_peak
FROM media_files
WHERE status = 'missing' AND missing_since < $1
LIMIT 5000;

-- name: GetMediaItemLyrics :one
-- Returns the stored lyrics for a track. Empty strings mean "not
-- fetched yet" — callers fall back to LRCLIB and persist via
-- UpdateMediaItemLyrics.
SELECT lyrics_plain, lyrics_synced
FROM media_items
WHERE id = $1;

-- name: UpdateMediaItemLyrics :exec
-- Writes lyrics for a track. Called by the scanner (from ID3 tags) and
-- the lyrics service (from LRCLIB fallback). Either value may be an
-- empty string — callers coalesce sources explicitly before writing.
UPDATE media_items
SET lyrics_plain = $2,
    lyrics_synced = $3,
    updated_at = NOW()
WHERE id = $1;

-- name: GetShowPostersForEpisodes :many
-- Resolves the show ancestor poster for a batch of episode IDs.
-- Episodes have parent_id → season; season has parent_id → show.
-- Used to substitute episode thumbnails with the show poster on
-- browse surfaces (hub / history / search) when the user has the
-- episode_use_show_poster preference enabled. Returns one row per
-- episode whose two-hop ancestor lookup yielded a poster — episodes
-- whose chain breaks (orphan season, missing show, NULL show poster)
-- are simply absent and the caller leaves their existing poster
-- alone.
SELECT
    ep.id          AS episode_id,
    show.poster_path AS show_poster_path
FROM media_items ep
JOIN media_items season ON season.id = ep.parent_id AND season.deleted_at IS NULL
JOIN media_items show   ON show.id   = season.parent_id AND show.deleted_at IS NULL
WHERE ep.id = ANY($1::uuid[])
  AND ep.type = 'episode'
  AND ep.deleted_at IS NULL
  AND show.poster_path IS NOT NULL
  AND show.poster_path <> '';

-- name: SoftDeleteMediaItemSubtree :exec
-- Soft-deletes the item plus every descendant reachable via parent_id.
-- Used by the admin "Remove from library" action when a user wants to
-- retire a duplicate / mismatched container row (e.g. a show that the
-- scanner created from misnamed files and that no longer reflects the
-- real on-disk content). Works for any container type: show → seasons
-- → episodes, artist → albums → tracks, season → episodes alone, etc.
WITH RECURSIVE subtree AS (
    SELECT mi.id FROM media_items mi WHERE mi.id = $1
    UNION
    SELECT m.id
    FROM media_items m
    JOIN subtree s ON m.parent_id = s.id
    WHERE m.deleted_at IS NULL
)
UPDATE media_items
SET deleted_at = NOW(),
    updated_at = NOW()
WHERE media_items.id IN (SELECT subtree.id FROM subtree)
  AND media_items.deleted_at IS NULL;

-- name: SoftDeleteMediaFilesForSubtree :exec
-- Companion to SoftDeleteMediaItemSubtree — also marks every file
-- attached to any item in the subtree as deleted, so the next scan
-- doesn't try to "restore" the soft-deleted item via
-- RestoreMediaItemAncestry when it sees the file still on disk.
-- Without this, a soft-deleted "A Happy Place" comes right back the
-- next time the scanner runs, defeating the user's removal.
WITH RECURSIVE subtree AS (
    SELECT mi.id FROM media_items mi WHERE mi.id = $1
    UNION
    SELECT m.id
    FROM media_items m
    JOIN subtree s ON m.parent_id = s.id
)
UPDATE media_files
SET status = 'deleted'
WHERE media_files.media_item_id IN (SELECT subtree.id FROM subtree)
  AND media_files.status != 'deleted';
