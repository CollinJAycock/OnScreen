-- +goose Up
-- +goose StatementBegin
-- Music: loudness analysis for files without ReplayGain tags, and featured
-- artist credits.
--
-- replaygain_source says where a file's replaygain_* values came from:
-- 'tags' (the file's own REPLAYGAIN_* tags, read by the scanner) or
-- 'analyzed' (measured by the opt-in loudness_analysis task). NULL when the
-- file has none. Tag values always win: the analysis task only ever writes a
-- file whose source is NULL or 'analyzed', and the scanner only clears an
-- analyzed value when the file's bytes change. Files that already carry
-- ReplayGain values got them from tags, so they are marked 'tags' here.
ALTER TABLE public.media_files ADD COLUMN IF NOT EXISTS replaygain_source text;
ALTER TABLE public.media_files ADD CONSTRAINT media_files_replaygain_source_check
    CHECK (replaygain_source IS NULL OR replaygain_source IN ('tags', 'analyzed'));
UPDATE public.media_files
SET replaygain_source = 'tags'
WHERE replaygain_track_gain IS NOT NULL OR replaygain_track_peak IS NOT NULL
   OR replaygain_album_gain IS NOT NULL OR replaygain_album_peak IS NOT NULL;

-- One EBU R128 measurement per music file. integrated_lufs is NULL when the
-- file is digital silence (nothing above the gate) — it gets no gain and is
-- left out of its album's loudness. file_size + file_hash are the file as it
-- was measured: when either differs from media_files the measurement is
-- stale and the file is analysed again. error is set when ffmpeg could not
-- measure the file; that fingerprint is not retried until the file changes.
CREATE TABLE public.media_file_loudness (
    file_id         uuid NOT NULL,
    integrated_lufs numeric(6,2),
    true_peak       numeric(8,6),
    duration_ms     bigint,
    file_size       bigint NOT NULL,
    file_hash       text,
    error           text,
    analyzed_at     timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT media_file_loudness_pkey PRIMARY KEY (file_id),
    CONSTRAINT media_file_loudness_file_id_fkey FOREIGN KEY (file_id)
        REFERENCES public.media_files(id) ON DELETE CASCADE
);

-- Artists credited on a track besides its album artist (the track's parent's
-- parent): "featured" artists from a feat. clause or a multi-valued
-- ARTISTS tag, and "artist" for the track's own performer on a compilation.
-- Backs the artist page's "Appears on" section. A rescan replaces a track's
-- rows, so the same file scanned twice never duplicates them.
CREATE TABLE public.track_artists (
    track_id  uuid NOT NULL,
    artist_id uuid NOT NULL,
    role      text NOT NULL DEFAULT 'featured',
    position  smallint NOT NULL DEFAULT 0,
    CONSTRAINT track_artists_pkey PRIMARY KEY (track_id, artist_id),
    CONSTRAINT track_artists_role_check CHECK (role IN ('featured', 'artist')),
    CONSTRAINT track_artists_track_id_fkey FOREIGN KEY (track_id)
        REFERENCES public.media_items(id) ON DELETE CASCADE,
    CONSTRAINT track_artists_artist_id_fkey FOREIGN KEY (artist_id)
        REFERENCES public.media_items(id) ON DELETE CASCADE
);
CREATE INDEX idx_track_artists_artist ON public.track_artists USING btree (artist_id);

-- Which music files have had their artist credits read, so a scan reads the
-- tags of an unchanged file once (to heal a library scanned before credits
-- existed) rather than on every scan. A changed file is re-read anyway.
CREATE TABLE public.track_credit_scans (
    file_id    uuid NOT NULL,
    scanned_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT track_credit_scans_pkey PRIMARY KEY (file_id),
    CONSTRAINT track_credit_scans_file_id_fkey FOREIGN KEY (file_id)
        REFERENCES public.media_files(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.track_credit_scans;
DROP TABLE IF EXISTS public.track_artists;
DROP TABLE IF EXISTS public.media_file_loudness;
ALTER TABLE public.media_files DROP CONSTRAINT IF EXISTS media_files_replaygain_source_check;
ALTER TABLE public.media_files DROP COLUMN IF EXISTS replaygain_source;
-- +goose StatementEnd
