-- +goose Up
-- Audiophile-grade music support. These columns carry the information an
-- audiophile client needs to decide whether a stream is actually bit-perfect
-- (bit depth / sample rate / channel layout), to apply ReplayGain correctly
-- (track + album gain/peak), and to reconcile tracks against MusicBrainz
-- beyond the single MBID we already store.
--
-- The existing `media_items.musicbrainz_id` column is retained as the
-- "primary MBID for this row's type" — track row = recording MBID, album row
-- = release MBID, artist row = artist MBID. The columns added below are for
-- the *cross-references* a track row needs (which release it came from,
-- which release group, which album-artist) and only occasionally populated
-- on album/artist rows.

-- ── media_files: per-file audio quality (ffprobe-derived) ─────────────────
ALTER TABLE media_files ADD COLUMN IF NOT EXISTS bit_depth            INT;
ALTER TABLE media_files ADD COLUMN IF NOT EXISTS sample_rate          INT;
ALTER TABLE media_files ADD COLUMN IF NOT EXISTS channel_layout       TEXT;
-- lossless is a scanner-derived classification, not a ffprobe field: flac,
-- alac, wav, aiff, dsd, wavpack, ape, tak → true. mp3/aac/opus/vorbis → false.
-- Clients use this to decide whether transcoding is acceptable without user
-- consent (it is not, for lossless sources).
ALTER TABLE media_files ADD COLUMN IF NOT EXISTS lossless             BOOLEAN;
-- ReplayGain tag values. Stored so clients can apply them without re-reading
-- the file; fields are in dB (gain) and linear 0.0–1.0+ (peak). Null means
-- the tag is absent — do NOT default to 0 dB, which would silently make a
-- ReplayGain-aware client apply no correction when the source simply had no
-- tag computed.
ALTER TABLE media_files ADD COLUMN IF NOT EXISTS replaygain_track_gain NUMERIC(6,2);
ALTER TABLE media_files ADD COLUMN IF NOT EXISTS replaygain_track_peak NUMERIC(8,6);
ALTER TABLE media_files ADD COLUMN IF NOT EXISTS replaygain_album_gain NUMERIC(6,2);
ALTER TABLE media_files ADD COLUMN IF NOT EXISTS replaygain_album_peak NUMERIC(8,6);

-- ── media_items: music-specific metadata ──────────────────────────────────
-- MusicBrainz cross-references. On track rows: release_id / release_group_id
-- / artist_id / album_artist_id all describe the track's context. On album
-- rows: release_group_id and album_artist_id are the useful ones. Artist
-- rows won't populate these.
ALTER TABLE media_items ADD COLUMN IF NOT EXISTS musicbrainz_release_id       UUID;
ALTER TABLE media_items ADD COLUMN IF NOT EXISTS musicbrainz_release_group_id UUID;
ALTER TABLE media_items ADD COLUMN IF NOT EXISTS musicbrainz_artist_id        UUID;
ALTER TABLE media_items ADD COLUMN IF NOT EXISTS musicbrainz_album_artist_id  UUID;

-- Disc / track totals (for "3 of 12" display and for detecting incomplete
-- albums). Separate from `index`, which is the track number within a disc.
ALTER TABLE media_items ADD COLUMN IF NOT EXISTS disc_total  INT;
ALTER TABLE media_items ADD COLUMN IF NOT EXISTS track_total INT;

-- Original release year — distinct from `year` when `year` represents this
-- specific edition/reissue. Important for chronological sorting of an
-- artist's discography.
ALTER TABLE media_items ADD COLUMN IF NOT EXISTS original_year INT;

-- Compilation flag: true for various-artists albums, soundtracks, etc.
-- Drives album-artist vs track-artist display, and filters out compilations
-- from "artist's discography" listings.
ALTER TABLE media_items ADD COLUMN IF NOT EXISTS compilation BOOLEAN NOT NULL DEFAULT false;

-- Release type (MusicBrainz vocabulary). Album, Single, EP, Compilation,
-- Live, Soundtrack, Remix, Broadcast, etc. Null on non-album rows.
ALTER TABLE media_items ADD COLUMN IF NOT EXISTS release_type TEXT;

-- MBID lookup indexes. These are low-cardinality-per-value but the
-- reconciliation path (picking up a user-supplied MBID and finding the
-- matching album) needs them to be fast.
CREATE INDEX IF NOT EXISTS idx_media_items_mb_release
    ON media_items(musicbrainz_release_id)
    WHERE musicbrainz_release_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_media_items_mb_release_group
    ON media_items(musicbrainz_release_group_id)
    WHERE musicbrainz_release_group_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_media_items_mb_artist
    ON media_items(musicbrainz_artist_id)
    WHERE musicbrainz_artist_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_media_items_mb_album_artist
    ON media_items(musicbrainz_album_artist_id)
    WHERE musicbrainz_album_artist_id IS NOT NULL AND deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_media_items_mb_album_artist;
DROP INDEX IF EXISTS idx_media_items_mb_artist;
DROP INDEX IF EXISTS idx_media_items_mb_release_group;
DROP INDEX IF EXISTS idx_media_items_mb_release;

ALTER TABLE media_items DROP COLUMN IF EXISTS release_type;
ALTER TABLE media_items DROP COLUMN IF EXISTS compilation;
ALTER TABLE media_items DROP COLUMN IF EXISTS original_year;
ALTER TABLE media_items DROP COLUMN IF EXISTS track_total;
ALTER TABLE media_items DROP COLUMN IF EXISTS disc_total;
ALTER TABLE media_items DROP COLUMN IF EXISTS musicbrainz_album_artist_id;
ALTER TABLE media_items DROP COLUMN IF EXISTS musicbrainz_artist_id;
ALTER TABLE media_items DROP COLUMN IF EXISTS musicbrainz_release_group_id;
ALTER TABLE media_items DROP COLUMN IF EXISTS musicbrainz_release_id;

ALTER TABLE media_files DROP COLUMN IF EXISTS replaygain_album_peak;
ALTER TABLE media_files DROP COLUMN IF EXISTS replaygain_album_gain;
ALTER TABLE media_files DROP COLUMN IF EXISTS replaygain_track_peak;
ALTER TABLE media_files DROP COLUMN IF EXISTS replaygain_track_gain;
ALTER TABLE media_files DROP COLUMN IF EXISTS lossless;
ALTER TABLE media_files DROP COLUMN IF EXISTS channel_layout;
ALTER TABLE media_files DROP COLUMN IF EXISTS sample_rate;
ALTER TABLE media_files DROP COLUMN IF EXISTS bit_depth;
