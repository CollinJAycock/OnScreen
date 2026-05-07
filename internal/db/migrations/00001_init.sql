-- +goose Up
-- +goose StatementBegin
-- ────────────────────────────────────────────────────────────────────────────
-- OnScreen consolidated schema, May 2026.
--
-- This file replaces 83 prior migrations (the original 00001_init plus 82
-- iterative additions/repairs). With zero real users yet, we squashed the
-- whole thing into a single source-of-truth init, baking in every design
-- decision through commit 3b672f4 ("delete = hard delete"). Specifically:
--
--   * Every FK column targeted by a cascade DELETE/UPDATE has a covering
--     index. The previous shape accumulated these across migrations 00007,
--     00081, 00082; here they're declared next to the tables they index.
--
--   * media_files.status only has 'active' and 'missing' values. The third
--     value 'deleted' was retired when soft-delete tombstones for files
--     went away — files are either present (active or transient missing)
--     or hard-deleted, no third state. The previous partial UNIQUE on
--     media_files(file_path) WHERE status != 'deleted' (00080's
--     workaround for the orphan-recreate trap) collapses back to a plain
--     UNIQUE here.
--
--   * media_items.type CHECK is consolidated to one declaration listing
--     every supported subtype, instead of being rewritten across 9
--     migrations as new types landed.
--
--   * normalize_dedupe_title() exists as a SQL function instead of a
--     6-deep regexp_replace ladder inlined in two queries. The
--     dedupe queries call the function; a functional index backs the
--     lookup so the dedupe pass doesn't seq-scan every show.
--
-- Down direction is intentionally destructive — uninstall path only.
-- ────────────────────────────────────────────────────────────────────────────

-- Name: pg_trgm; Type: EXTENSION; Schema: -; Owner: -

CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;



-- Name: pgcrypto; Type: EXTENSION; Schema: -; Owner: -

CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;



-- Name: unaccent; Type: EXTENSION; Schema: -; Owner: -

CREATE EXTENSION IF NOT EXISTS unaccent WITH SCHEMA public;



-- Name: content_rating_rank(text); Type: FUNCTION; Schema: public; Owner: -

CREATE FUNCTION public.content_rating_rank(rating text) RETURNS integer
    LANGUAGE plpgsql IMMUTABLE
    AS $$
BEGIN
  IF rating IS NULL OR rating = '' THEN
    RETURN 4;  -- treat unrated as most restrictive
  END IF;
  RETURN CASE rating
    WHEN 'G'     THEN 0  WHEN 'TV-Y'  THEN 0  WHEN 'TV-G' THEN 0
    WHEN 'PG'    THEN 1  WHEN 'TV-Y7' THEN 1  WHEN 'TV-PG' THEN 1
    WHEN 'PG-13' THEN 2  WHEN 'TV-14' THEN 2
    WHEN 'R'     THEN 3
    WHEN 'NC-17' THEN 3  WHEN 'TV-MA' THEN 3
    ELSE 4  -- NR, UNRATED, X, empty, etc.
  END;
END;
$$;


-- Name: normalize_dedupe_title(text); Type: FUNCTION; Schema: public; Owner: -
--
-- Canonical title-normalisation used by ListDuplicateTopLevelItems
-- and the prefix-dedupe variant. Encapsulates the regexp_replace
-- ladder that previously lived inline in two queries:
--
--   1. Strip leading "[release-group]" prefixes
--   2. Decode &amp; → & and drop apostrophes
--   3. Drop leading articles (the/a/an)
--   4. Drop trailing 4-digit year ("Family Guy 1999")
--   5. Drop trailing season markers ("S2", "Season 2", "2nd Season",
--      "Cour 3") — keeps cours-of-the-same-anime collapsing under one
--      survivor (One Punch Man / OPM S2 / OPM S3)
--   6. Collapse "and"/"&" to a single token
--   7. Strip every non-alphanumeric (whitespace, punctuation, the
--      colon-subtitle of "Code Geass: Lelouch …" — that's deliberate;
--      colon subtitles don't cluster reliably so we don't try)
--
-- IMMUTABLE so it can back a functional index. UNICODE-aware via
-- unaccent() (CJK/diacritics are normalised before the alphanumeric
-- strip, so non-Latin originals don't collapse to empty strings).

CREATE FUNCTION public.normalize_dedupe_title(t text) RETURNS text
    LANGUAGE sql IMMUTABLE
    AS $$
    SELECT lower(
        regexp_replace(
          regexp_replace(
            regexp_replace(
              regexp_replace(
                regexp_replace(
                  regexp_replace(
                    public.unaccent(replace(replace(
                      regexp_replace(t, '^\s*\[[^\]]+\]\s*', '', 'i'),
                      '&amp;', '&'), '''', '')
                    ),
                    '^\s*(the|a|an)\s+', '', 'i'
                  ),
                  '[\s\-]+[\(\[]?(19|20)\d{2}[\)\]]?\s*$', ''
                ),
                '\s+(s\s*\d+|season\s+\d+|\d+(st|nd|rd|th)\s+season|cour\s+\d+)\s*$',
                '', 'gi'
              ),
              '\s+(and|&)\s+', 'and', 'gi'
            ),
            '[^a-zA-Z0-9]+', '', 'g'
          ),
          '', '', 'g'  -- pass-through (kept so the regex chain stays even-numbered)
        )
    );
$$;


-- Name: arr_services; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.arr_services (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    kind text NOT NULL,
    base_url text NOT NULL,
    api_key text NOT NULL,
    default_quality_profile_id integer,
    default_root_folder text,
    default_tags jsonb DEFAULT '[]'::jsonb NOT NULL,
    minimum_availability text,
    series_type text,
    season_folder boolean,
    language_profile_id integer,
    is_default boolean DEFAULT false NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT arr_services_kind_check CHECK ((kind = ANY (ARRAY['radarr'::text, 'sonarr'::text])))
);


-- Name: audit_log; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.audit_log (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid,
    action text NOT NULL,
    target text,
    detail jsonb,
    ip_addr inet,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: channels; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.channels (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tuner_id uuid NOT NULL,
    number text NOT NULL,
    callsign text,
    name text NOT NULL,
    logo_url text,
    enabled boolean DEFAULT true NOT NULL,
    sort_order integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    epg_channel_id text
);


-- Name: collection_items; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.collection_items (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    collection_id uuid NOT NULL,
    media_item_id uuid NOT NULL,
    "position" integer DEFAULT 0 NOT NULL,
    added_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: collections; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.collections (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid,
    name text NOT NULL,
    description text,
    type text NOT NULL,
    genre text,
    poster_path text,
    sort_order integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    rules jsonb,
    library_id uuid,
    CONSTRAINT collections_type_check CHECK ((type = ANY (ARRAY['auto_genre'::text, 'playlist'::text, 'smart_playlist'::text, 'event_folder'::text])))
);


-- Name: epg_programs; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.epg_programs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    channel_id uuid NOT NULL,
    source_program_id text NOT NULL,
    title text NOT NULL,
    subtitle text,
    description text,
    category text[] DEFAULT '{}'::text[] NOT NULL,
    rating text,
    season_num integer,
    episode_num integer,
    original_air_date date,
    starts_at timestamp with time zone NOT NULL,
    ends_at timestamp with time zone NOT NULL,
    raw_data jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: epg_sources; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.epg_sources (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    type text NOT NULL,
    name text NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL,
    refresh_interval_min integer DEFAULT 360 NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    last_pull_at timestamp with time zone,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT epg_sources_type_check CHECK ((type = ANY (ARRAY['schedules_direct'::text, 'xmltv_url'::text, 'xmltv_file'::text])))
);


-- Name: external_subtitles; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.external_subtitles (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    file_id uuid NOT NULL,
    language text NOT NULL,
    title text,
    forced boolean DEFAULT false NOT NULL,
    sdh boolean DEFAULT false NOT NULL,
    source text NOT NULL,
    source_id text,
    storage_path text NOT NULL,
    rating real,
    download_count integer,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);








-- Name: libraries; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.libraries (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    type text NOT NULL,
    scan_paths text[] NOT NULL,
    agent text DEFAULT 'tmdb'::text NOT NULL,
    language text DEFAULT 'en'::text NOT NULL,
    scan_interval interval DEFAULT '1 day'::interval NOT NULL,
    scan_last_completed_at timestamp with time zone,
    metadata_refresh_interval interval DEFAULT '7 days'::interval NOT NULL,
    metadata_last_refreshed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    is_private boolean DEFAULT false NOT NULL,
    auto_grant_new_users boolean DEFAULT false NOT NULL,
    CONSTRAINT libraries_type_check CHECK ((type = ANY (ARRAY['movie'::text, 'show'::text, 'music'::text, 'photo'::text, 'dvr'::text, 'audiobook'::text, 'podcast'::text, 'home_video'::text, 'book'::text, 'anime'::text, 'manga'::text])))
);


-- Name: media_items; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.media_items (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    library_id uuid NOT NULL,
    type text NOT NULL,
    title text NOT NULL,
    sort_title text NOT NULL,
    original_title text,
    year integer,
    summary text,
    tagline text,
    rating numeric(3,1),
    audience_rating numeric(3,1),
    content_rating text,
    duration_ms bigint,
    genres text[],
    tags text[],
    tmdb_id integer,
    tvdb_id integer,
    imdb_id text,
    musicbrainz_id uuid,
    parent_id uuid,
    index integer,
    search_vector tsvector GENERATED ALWAYS AS (to_tsvector('english'::regconfig, ((((COALESCE(title, ''::text) || ' '::text) || COALESCE(original_title, ''::text)) || ' '::text) || COALESCE(summary, ''::text)))) STORED,
    poster_path text,
    fanart_path text,
    thumb_path text,
    originally_available_at date,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    last_enrich_attempted_at timestamp with time zone,
    musicbrainz_release_id uuid,
    musicbrainz_release_group_id uuid,
    musicbrainz_artist_id uuid,
    musicbrainz_album_artist_id uuid,
    disc_total integer,
    track_total integer,
    original_year integer,
    compilation boolean DEFAULT false NOT NULL,
    release_type text,
    lyrics_plain text,
    lyrics_synced text,
    anilist_id integer,
    mal_id integer,
    kind text,
    reading_direction text,
    franchise_id integer,
    CONSTRAINT media_items_reading_direction_check CHECK ((reading_direction = ANY (ARRAY['ltr'::text, 'rtl'::text, 'ttb'::text]))),
    CONSTRAINT media_items_type_check CHECK ((type = ANY (ARRAY['movie'::text, 'show'::text, 'season'::text, 'episode'::text, 'track'::text, 'album'::text, 'artist'::text, 'photo'::text, 'music_video'::text, 'audiobook'::text, 'audiobook_chapter'::text, 'book_author'::text, 'book_series'::text, 'podcast'::text, 'podcast_episode'::text, 'home_video'::text, 'book'::text])))
);


-- Name: hub_recently_added; Type: MATERIALIZED VIEW; Schema: public; Owner: -

CREATE MATERIALIZED VIEW public.hub_recently_added AS
 SELECT l.id AS library_id,
    m.id AS media_id,
    m.type,
    m.title,
    m.year,
    m.rating,
    m.poster_path,
    m.created_at
   FROM (public.media_items m
     JOIN public.libraries l ON ((l.id = m.library_id)))
  WHERE ((m.deleted_at IS NULL) AND (m.type = ANY (ARRAY['movie'::text, 'show'::text])))
  ORDER BY m.created_at DESC
  WITH NO DATA;


-- Name: intro_markers; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.intro_markers (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    media_item_id uuid NOT NULL,
    kind text NOT NULL,
    start_ms bigint NOT NULL,
    end_ms bigint NOT NULL,
    source text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT intro_markers_check CHECK ((end_ms > start_ms)),
    CONSTRAINT intro_markers_kind_check CHECK ((kind = ANY (ARRAY['intro'::text, 'credits'::text]))),
    CONSTRAINT intro_markers_source_check CHECK ((source = ANY (ARRAY['auto'::text, 'manual'::text, 'chapter'::text]))),
    CONSTRAINT intro_markers_start_ms_check CHECK ((start_ms >= 0))
);


-- Name: invite_tokens; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.invite_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    created_by uuid NOT NULL,
    token_hash text NOT NULL,
    email text,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    used_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: item_cooccurrence; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.item_cooccurrence (
    item_a uuid NOT NULL,
    item_b uuid NOT NULL,
    score integer NOT NULL,
    computed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT item_cooccurrence_check CHECK ((item_a < item_b))
);


-- Name: library_access; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.library_access (
    user_id uuid NOT NULL,
    library_id uuid NOT NULL,
    granted_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: media_credits; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.media_credits (
    media_item_id uuid NOT NULL,
    person_id uuid NOT NULL,
    role text NOT NULL,
    "character" text,
    job text DEFAULT ''::text NOT NULL,
    ord integer DEFAULT 0 NOT NULL
);


-- Name: media_files; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.media_files (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    media_item_id uuid NOT NULL,
    file_path text NOT NULL,
    file_size bigint NOT NULL,
    container text,
    video_codec text,
    audio_codec text,
    resolution_w integer,
    resolution_h integer,
    bitrate bigint,
    hdr_type text,
    frame_rate numeric(6,3),
    audio_streams jsonb,
    subtitle_streams jsonb,
    chapters jsonb,
    file_hash text,
    status text DEFAULT 'active'::text NOT NULL,
    missing_since timestamp with time zone,
    scanned_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    duration_ms bigint,
    bit_depth integer,
    sample_rate integer,
    channel_layout text,
    lossless boolean,
    replaygain_track_gain numeric(6,2),
    replaygain_track_peak numeric(8,6),
    replaygain_album_gain numeric(6,2),
    replaygain_album_peak numeric(8,6),
    -- 'active'  — file is on disk now.
    -- 'missing' — file disappeared, in grace period before hard-delete.
    -- ('deleted' was retired when "delete = hard delete" landed; deletion
    --  is a real DELETE, no tombstone state.)
    CONSTRAINT media_files_status_check CHECK ((status = ANY (ARRAY['active'::text, 'missing'::text])))
);


-- Name: media_requests; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.media_requests (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    type text NOT NULL,
    tmdb_id integer NOT NULL,
    title text NOT NULL,
    year integer,
    poster_url text,
    overview text,
    status text DEFAULT 'pending'::text NOT NULL,
    seasons jsonb,
    requested_service_id uuid,
    quality_profile_id integer,
    root_folder text,
    service_id uuid,
    decline_reason text,
    decided_by uuid,
    decided_at timestamp with time zone,
    fulfilled_item_id uuid,
    fulfilled_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT media_requests_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'approved'::text, 'declined'::text, 'downloading'::text, 'available'::text, 'failed'::text]))),
    CONSTRAINT media_requests_type_check CHECK ((type = ANY (ARRAY['movie'::text, 'show'::text])))
);


-- Name: notifications; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.notifications (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    type text NOT NULL,
    title text NOT NULL,
    body text DEFAULT ''::text NOT NULL,
    item_id uuid,
    read boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT notifications_type_check CHECK ((type = ANY (ARRAY['new_content'::text, 'scan_complete'::text, 'system'::text])))
);


-- Name: password_reset_tokens; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.password_reset_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    token_hash text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: people; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.people (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tmdb_id integer,
    name text NOT NULL,
    profile_path text,
    bio text,
    birthday date,
    deathday date,
    place_of_birth text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: photo_metadata; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.photo_metadata (
    item_id uuid NOT NULL,
    taken_at timestamp with time zone,
    camera_make text,
    camera_model text,
    lens_model text,
    focal_length_mm double precision,
    aperture double precision,
    shutter_speed text,
    iso integer,
    flash boolean,
    orientation integer,
    width integer,
    height integer,
    gps_lat double precision,
    gps_lon double precision,
    gps_alt double precision,
    raw_exif jsonb,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: plugins; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.plugins (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    role text NOT NULL,
    transport text DEFAULT 'http'::text NOT NULL,
    endpoint_url text NOT NULL,
    allowed_hosts jsonb DEFAULT '[]'::jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT plugins_role_check CHECK ((role = ANY (ARRAY['notification'::text, 'metadata'::text, 'task'::text]))),
    CONSTRAINT plugins_transport_check CHECK ((transport = 'http'::text))
);


-- Name: recordings; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.recordings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    schedule_id uuid,
    user_id uuid NOT NULL,
    channel_id uuid NOT NULL,
    program_id uuid,
    title text NOT NULL,
    subtitle text,
    season_num integer,
    episode_num integer,
    status text NOT NULL,
    starts_at timestamp with time zone NOT NULL,
    ends_at timestamp with time zone NOT NULL,
    file_path text,
    item_id uuid,
    error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT recordings_status_check CHECK ((status = ANY (ARRAY['scheduled'::text, 'recording'::text, 'completed'::text, 'failed'::text, 'cancelled'::text, 'superseded'::text])))
);


-- Name: scheduled_tasks; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.scheduled_tasks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    task_type text NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL,
    cron_expr text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    last_run_at timestamp with time zone,
    next_run_at timestamp with time zone NOT NULL,
    last_status text DEFAULT ''::text NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: schedules; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.schedules (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    type text NOT NULL,
    program_id uuid,
    channel_id uuid,
    title_match text,
    new_only boolean DEFAULT false NOT NULL,
    time_start text,
    time_end text,
    padding_pre_sec integer DEFAULT 60 NOT NULL,
    padding_post_sec integer DEFAULT 180 NOT NULL,
    priority integer DEFAULT 50 NOT NULL,
    retention_days integer,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT schedules_type_check CHECK ((type = ANY (ARRAY['once'::text, 'series'::text, 'channel_block'::text])))
);


-- Name: server_settings; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.server_settings (
    key text NOT NULL,
    value text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: sessions; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.sessions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    token_hash text NOT NULL,
    client_id text,
    client_name text,
    device_id text,
    platform text,
    ip_addr inet,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    last_seen timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: task_runs; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.task_runs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    task_id uuid NOT NULL,
    started_at timestamp with time zone DEFAULT now() NOT NULL,
    ended_at timestamp with time zone,
    status text DEFAULT 'running'::text NOT NULL,
    output text DEFAULT ''::text NOT NULL,
    error text DEFAULT ''::text NOT NULL
);


-- Name: trickplay_status; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.trickplay_status (
    item_id uuid NOT NULL,
    file_id uuid,
    status text DEFAULT 'pending'::text NOT NULL,
    sprite_count integer DEFAULT 0 NOT NULL,
    interval_sec integer DEFAULT 10 NOT NULL,
    thumb_width integer DEFAULT 320 NOT NULL,
    thumb_height integer DEFAULT 180 NOT NULL,
    grid_cols integer DEFAULT 10 NOT NULL,
    grid_rows integer DEFAULT 10 NOT NULL,
    last_attempted_at timestamp with time zone,
    last_error text,
    generated_at timestamp with time zone,
    CONSTRAINT trickplay_status_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'done'::text, 'failed'::text, 'skipped'::text])))
);


-- Name: tuner_devices; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.tuner_devices (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    type text NOT NULL,
    name text NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL,
    tune_count integer DEFAULT 0 NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    last_seen_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tuner_devices_type_check CHECK ((type = ANY (ARRAY['hdhomerun'::text, 'm3u'::text])))
);


-- Name: user_favorites; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.user_favorites (
    user_id uuid NOT NULL,
    media_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: user_watch_status; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.user_watch_status (
    user_id uuid NOT NULL,
    media_item_id uuid NOT NULL,
    status text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT user_watch_status_status_check CHECK ((status = ANY (ARRAY['plan_to_watch'::text, 'watching'::text, 'completed'::text, 'on_hold'::text, 'dropped'::text])))
);


-- Name: users; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.users (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    username text NOT NULL,
    email text,
    password_hash text,
    is_admin boolean DEFAULT false NOT NULL,
    pin text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    google_id text,
    google_avatar_url text,
    github_id text,
    discord_id text,
    parent_user_id uuid,
    avatar_url text,
    preferred_audio_lang text,
    preferred_subtitle_lang text,
    max_content_rating text,
    oidc_issuer text,
    oidc_subject text,
    ldap_dn text,
    max_video_bitrate_kbps integer,
    max_audio_bitrate_kbps integer,
    max_video_height integer,
    preferred_video_codec text,
    forced_subtitles_only boolean DEFAULT false NOT NULL,
    session_epoch bigint DEFAULT 0 NOT NULL,
    saml_issuer text,
    saml_subject text,
    inherit_library_access boolean DEFAULT true NOT NULL,
    episode_use_show_poster boolean DEFAULT true NOT NULL,
    CONSTRAINT chk_managed_not_admin CHECK (((parent_user_id IS NULL) OR (is_admin = false)))
);


-- Name: watch_events; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.watch_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    media_id uuid NOT NULL,
    file_id uuid,
    session_id uuid,
    event_type text NOT NULL,
    position_ms bigint DEFAULT 0 NOT NULL,
    duration_ms bigint,
    client_id text,
    client_name text,
    client_ip inet,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT watch_events_event_type_check CHECK ((event_type = ANY (ARRAY['play'::text, 'pause'::text, 'resume'::text, 'stop'::text, 'seek'::text, 'scrobble'::text])))
)
PARTITION BY RANGE (occurred_at);


-- Name: watch_events_2026_03; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.watch_events_2026_03 (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    media_id uuid NOT NULL,
    file_id uuid,
    session_id uuid,
    event_type text NOT NULL,
    position_ms bigint DEFAULT 0 NOT NULL,
    duration_ms bigint,
    client_id text,
    client_name text,
    client_ip inet,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT watch_events_event_type_check CHECK ((event_type = ANY (ARRAY['play'::text, 'pause'::text, 'resume'::text, 'stop'::text, 'seek'::text, 'scrobble'::text])))
);


-- Name: watch_events_2026_04; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.watch_events_2026_04 (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    media_id uuid NOT NULL,
    file_id uuid,
    session_id uuid,
    event_type text NOT NULL,
    position_ms bigint DEFAULT 0 NOT NULL,
    duration_ms bigint,
    client_id text,
    client_name text,
    client_ip inet,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT watch_events_event_type_check CHECK ((event_type = ANY (ARRAY['play'::text, 'pause'::text, 'resume'::text, 'stop'::text, 'seek'::text, 'scrobble'::text])))
);


-- Name: watch_events_2026_05; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.watch_events_2026_05 (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    media_id uuid NOT NULL,
    file_id uuid,
    session_id uuid,
    event_type text NOT NULL,
    position_ms bigint DEFAULT 0 NOT NULL,
    duration_ms bigint,
    client_id text,
    client_name text,
    client_ip inet,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT watch_events_event_type_check CHECK ((event_type = ANY (ARRAY['play'::text, 'pause'::text, 'resume'::text, 'stop'::text, 'seek'::text, 'scrobble'::text])))
);


-- Name: watch_events_default; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.watch_events_default (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    media_id uuid NOT NULL,
    file_id uuid,
    session_id uuid,
    event_type text NOT NULL,
    position_ms bigint DEFAULT 0 NOT NULL,
    duration_ms bigint,
    client_id text,
    client_name text,
    client_ip inet,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT watch_events_event_type_check CHECK ((event_type = ANY (ARRAY['play'::text, 'pause'::text, 'resume'::text, 'stop'::text, 'seek'::text, 'scrobble'::text])))
);


-- Name: watch_plays; Type: VIEW; Schema: public; Owner: -

CREATE VIEW public.watch_plays AS
 WITH terminal AS (
         SELECT we.id,
            we.user_id,
            we.media_id,
            we.file_id,
            we.session_id,
            we.event_type,
            we.position_ms,
            we.duration_ms,
            we.client_name,
            we.client_id,
            we.client_ip,
            we.occurred_at,
            lead(we.occurred_at) OVER (PARTITION BY we.user_id, we.media_id ORDER BY we.occurred_at) AS next_at
           FROM public.watch_events we
          WHERE (we.event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]))
        )
 SELECT id,
    user_id,
    media_id,
    file_id,
    session_id,
    event_type,
    position_ms,
    duration_ms,
    client_name,
    client_id,
    client_ip,
    occurred_at
   FROM terminal
  WHERE ((next_at IS NULL) OR ((next_at - occurred_at) > '00:30:00'::interval));


-- Name: watch_state; Type: MATERIALIZED VIEW; Schema: public; Owner: -

CREATE MATERIALIZED VIEW public.watch_state AS
 SELECT DISTINCT ON (user_id, media_id) user_id,
    media_id,
    position_ms,
    duration_ms,
        CASE
            WHEN (((position_ms)::double precision / (NULLIF(duration_ms, 0))::double precision) > (0.9)::double precision) THEN 'watched'::text
            WHEN (position_ms > 0) THEN 'in_progress'::text
            ELSE 'unwatched'::text
        END AS status,
    occurred_at AS last_watched_at,
    client_id AS last_client_id,
    client_name AS last_client_name
   FROM public.watch_events
  WHERE (event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]))
  ORDER BY user_id, media_id, occurred_at DESC
  WITH NO DATA;


-- Name: webhook_endpoints; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.webhook_endpoints (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    url text NOT NULL,
    secret text,
    events text[] NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: webhook_failures; Type: TABLE; Schema: public; Owner: -

CREATE TABLE public.webhook_failures (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    endpoint_id uuid NOT NULL,
    url text NOT NULL,
    payload jsonb NOT NULL,
    last_error text NOT NULL,
    attempt_count integer DEFAULT 3 NOT NULL,
    failed_at timestamp with time zone DEFAULT now() NOT NULL
);


-- Name: watch_events_2026_03; Type: TABLE ATTACH; Schema: public; Owner: -

ALTER TABLE ONLY public.watch_events ATTACH PARTITION public.watch_events_2026_03 FOR VALUES FROM ('2026-03-01 00:00:00+00') TO ('2026-04-01 00:00:00+00');


-- Name: watch_events_2026_04; Type: TABLE ATTACH; Schema: public; Owner: -

ALTER TABLE ONLY public.watch_events ATTACH PARTITION public.watch_events_2026_04 FOR VALUES FROM ('2026-04-01 00:00:00+00') TO ('2026-05-01 00:00:00+00');


-- Name: watch_events_2026_05; Type: TABLE ATTACH; Schema: public; Owner: -

ALTER TABLE ONLY public.watch_events ATTACH PARTITION public.watch_events_2026_05 FOR VALUES FROM ('2026-05-01 00:00:00+00') TO ('2026-06-01 00:00:00+00');


-- Name: watch_events_default; Type: TABLE ATTACH; Schema: public; Owner: -

ALTER TABLE ONLY public.watch_events ATTACH PARTITION public.watch_events_default DEFAULT;


-- Name: arr_services arr_services_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.arr_services
    ADD CONSTRAINT arr_services_pkey PRIMARY KEY (id);


-- Name: audit_log audit_log_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.audit_log
    ADD CONSTRAINT audit_log_pkey PRIMARY KEY (id);


-- Name: channels channels_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.channels
    ADD CONSTRAINT channels_pkey PRIMARY KEY (id);


-- Name: channels channels_tuner_id_number_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.channels
    ADD CONSTRAINT channels_tuner_id_number_key UNIQUE (tuner_id, number);


-- Name: collection_items collection_items_collection_id_media_item_id_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.collection_items
    ADD CONSTRAINT collection_items_collection_id_media_item_id_key UNIQUE (collection_id, media_item_id);


-- Name: collection_items collection_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.collection_items
    ADD CONSTRAINT collection_items_pkey PRIMARY KEY (id);


-- Name: collections collections_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.collections
    ADD CONSTRAINT collections_pkey PRIMARY KEY (id);


-- Name: epg_programs epg_programs_channel_id_source_program_id_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.epg_programs
    ADD CONSTRAINT epg_programs_channel_id_source_program_id_key UNIQUE (channel_id, source_program_id);


-- Name: epg_programs epg_programs_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.epg_programs
    ADD CONSTRAINT epg_programs_pkey PRIMARY KEY (id);


-- Name: epg_sources epg_sources_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.epg_sources
    ADD CONSTRAINT epg_sources_pkey PRIMARY KEY (id);


-- Name: external_subtitles external_subtitles_file_id_source_source_id_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.external_subtitles
    ADD CONSTRAINT external_subtitles_file_id_source_source_id_key UNIQUE (file_id, source, source_id);


-- Name: external_subtitles external_subtitles_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.external_subtitles
    ADD CONSTRAINT external_subtitles_pkey PRIMARY KEY (id);





-- Name: intro_markers intro_markers_media_item_id_kind_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.intro_markers
    ADD CONSTRAINT intro_markers_media_item_id_kind_key UNIQUE (media_item_id, kind);


-- Name: intro_markers intro_markers_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.intro_markers
    ADD CONSTRAINT intro_markers_pkey PRIMARY KEY (id);


-- Name: invite_tokens invite_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.invite_tokens
    ADD CONSTRAINT invite_tokens_pkey PRIMARY KEY (id);


-- Name: invite_tokens invite_tokens_token_hash_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.invite_tokens
    ADD CONSTRAINT invite_tokens_token_hash_key UNIQUE (token_hash);


-- Name: item_cooccurrence item_cooccurrence_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.item_cooccurrence
    ADD CONSTRAINT item_cooccurrence_pkey PRIMARY KEY (item_a, item_b);


-- Name: libraries libraries_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.libraries
    ADD CONSTRAINT libraries_pkey PRIMARY KEY (id);


-- Name: library_access library_access_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.library_access
    ADD CONSTRAINT library_access_pkey PRIMARY KEY (user_id, library_id);


-- Name: media_credits media_credits_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_credits
    ADD CONSTRAINT media_credits_pkey PRIMARY KEY (media_item_id, person_id, role, job);


-- Name: media_files media_files_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_files
    ADD CONSTRAINT media_files_pkey PRIMARY KEY (id);


-- Name: media_items media_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_items
    ADD CONSTRAINT media_items_pkey PRIMARY KEY (id);


-- Name: media_requests media_requests_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_requests
    ADD CONSTRAINT media_requests_pkey PRIMARY KEY (id);


-- Name: notifications notifications_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.notifications
    ADD CONSTRAINT notifications_pkey PRIMARY KEY (id);


-- Name: password_reset_tokens password_reset_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_pkey PRIMARY KEY (id);


-- Name: password_reset_tokens password_reset_tokens_token_hash_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_token_hash_key UNIQUE (token_hash);


-- Name: people people_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.people
    ADD CONSTRAINT people_pkey PRIMARY KEY (id);


-- Name: people people_tmdb_id_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.people
    ADD CONSTRAINT people_tmdb_id_key UNIQUE (tmdb_id);


-- Name: photo_metadata photo_metadata_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.photo_metadata
    ADD CONSTRAINT photo_metadata_pkey PRIMARY KEY (item_id);


-- Name: plugins plugins_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.plugins
    ADD CONSTRAINT plugins_pkey PRIMARY KEY (id);


-- Name: recordings recordings_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.recordings
    ADD CONSTRAINT recordings_pkey PRIMARY KEY (id);


-- Name: recordings recordings_user_id_program_id_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.recordings
    ADD CONSTRAINT recordings_user_id_program_id_key UNIQUE (user_id, program_id);


-- Name: scheduled_tasks scheduled_tasks_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.scheduled_tasks
    ADD CONSTRAINT scheduled_tasks_pkey PRIMARY KEY (id);


-- Name: schedules schedules_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.schedules
    ADD CONSTRAINT schedules_pkey PRIMARY KEY (id);


-- Name: server_settings server_settings_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.server_settings
    ADD CONSTRAINT server_settings_pkey PRIMARY KEY (key);


-- Name: sessions sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_pkey PRIMARY KEY (id);


-- Name: sessions sessions_token_hash_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_token_hash_key UNIQUE (token_hash);


-- Name: task_runs task_runs_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.task_runs
    ADD CONSTRAINT task_runs_pkey PRIMARY KEY (id);


-- Name: trickplay_status trickplay_status_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.trickplay_status
    ADD CONSTRAINT trickplay_status_pkey PRIMARY KEY (item_id);


-- Name: tuner_devices tuner_devices_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.tuner_devices
    ADD CONSTRAINT tuner_devices_pkey PRIMARY KEY (id);


-- Name: user_favorites user_favorites_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.user_favorites
    ADD CONSTRAINT user_favorites_pkey PRIMARY KEY (user_id, media_id);


-- Name: user_watch_status user_watch_status_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.user_watch_status
    ADD CONSTRAINT user_watch_status_pkey PRIMARY KEY (user_id, media_item_id);


-- Name: users users_discord_id_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_discord_id_key UNIQUE (discord_id);


-- Name: users users_email_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_email_key UNIQUE (email);


-- Name: users users_github_id_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_github_id_key UNIQUE (github_id);


-- Name: users users_google_id_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_google_id_key UNIQUE (google_id);


-- Name: users users_ldap_dn_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_ldap_dn_key UNIQUE (ldap_dn);


-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


-- Name: users users_username_key; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_username_key UNIQUE (username);


-- Name: watch_events watch_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.watch_events
    ADD CONSTRAINT watch_events_pkey PRIMARY KEY (id, occurred_at);


-- Name: watch_events_2026_03 watch_events_2026_03_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.watch_events_2026_03
    ADD CONSTRAINT watch_events_2026_03_pkey PRIMARY KEY (id, occurred_at);


-- Name: watch_events_2026_04 watch_events_2026_04_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.watch_events_2026_04
    ADD CONSTRAINT watch_events_2026_04_pkey PRIMARY KEY (id, occurred_at);


-- Name: watch_events_2026_05 watch_events_2026_05_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.watch_events_2026_05
    ADD CONSTRAINT watch_events_2026_05_pkey PRIMARY KEY (id, occurred_at);


-- Name: watch_events_default watch_events_default_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.watch_events_default
    ADD CONSTRAINT watch_events_default_pkey PRIMARY KEY (id, occurred_at);


-- Name: webhook_endpoints webhook_endpoints_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.webhook_endpoints
    ADD CONSTRAINT webhook_endpoints_pkey PRIMARY KEY (id);


-- Name: webhook_failures webhook_failures_pkey; Type: CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.webhook_failures
    ADD CONSTRAINT webhook_failures_pkey PRIMARY KEY (id);


-- Name: arr_services_kind_default; Type: INDEX; Schema: public; Owner: -

CREATE UNIQUE INDEX arr_services_kind_default ON public.arr_services USING btree (kind) WHERE (is_default = true);


-- Name: arr_services_kind_enabled; Type: INDEX; Schema: public; Owner: -

CREATE INDEX arr_services_kind_enabled ON public.arr_services USING btree (kind) WHERE (enabled = true);


-- Name: collections_event_folder_unique; Type: INDEX; Schema: public; Owner: -

CREATE UNIQUE INDEX collections_event_folder_unique ON public.collections USING btree (library_id, name) WHERE (type = 'event_folder'::text);


-- Name: collections_library_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX collections_library_id ON public.collections USING btree (library_id) WHERE (library_id IS NOT NULL);


-- Name: hub_recently_added_library_id_created_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX hub_recently_added_library_id_created_at_idx ON public.hub_recently_added USING btree (library_id, created_at DESC);


-- Name: hub_recently_added_library_id_media_id_idx; Type: INDEX; Schema: public; Owner: -

CREATE UNIQUE INDEX hub_recently_added_library_id_media_id_idx ON public.hub_recently_added USING btree (library_id, media_id);


-- Name: idx_audit_log_created_at; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_audit_log_created_at ON public.audit_log USING btree (created_at DESC);


-- Name: idx_audit_log_user_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_audit_log_user_id ON public.audit_log USING btree (user_id);


-- Name: idx_channels_epg_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_channels_epg_id ON public.channels USING btree (epg_channel_id) WHERE (epg_channel_id IS NOT NULL);


-- Name: idx_channels_tuner; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_channels_tuner ON public.channels USING btree (tuner_id) WHERE (enabled = true);


-- Name: idx_collection_items_collection; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_collection_items_collection ON public.collection_items USING btree (collection_id);


-- Name: idx_collections_type; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_collections_type ON public.collections USING btree (type);


-- Name: idx_collections_user; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_collections_user ON public.collections USING btree (user_id) WHERE (user_id IS NOT NULL);


-- Name: idx_epg_programs_channel_starts; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_epg_programs_channel_starts ON public.epg_programs USING btree (channel_id, starts_at);


-- Name: idx_epg_programs_window; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_epg_programs_window ON public.epg_programs USING btree (starts_at, ends_at);


-- Name: idx_external_subtitles_file; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_external_subtitles_file ON public.external_subtitles USING btree (file_id);


-- Name: idx_external_subtitles_file_lang; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_external_subtitles_file_lang ON public.external_subtitles USING btree (file_id, language);


-- Name: idx_intro_markers_media; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_intro_markers_media ON public.intro_markers USING btree (media_item_id);


-- Name: idx_invite_tokens_created_by; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_invite_tokens_created_by ON public.invite_tokens USING btree (created_by);


-- Name: idx_invite_tokens_hash; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_invite_tokens_hash ON public.invite_tokens USING btree (token_hash) WHERE (used_at IS NULL);


-- Name: idx_invite_tokens_used_by; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_invite_tokens_used_by ON public.invite_tokens USING btree (used_by) WHERE (used_by IS NOT NULL);


-- Name: idx_item_cooccurrence_a_score; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_item_cooccurrence_a_score ON public.item_cooccurrence USING btree (item_a, score DESC);


-- Name: idx_item_cooccurrence_b_score; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_item_cooccurrence_b_score ON public.item_cooccurrence USING btree (item_b, score DESC);


-- Name: idx_library_access_library; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_library_access_library ON public.library_access USING btree (library_id);


-- Name: idx_media_credits_item; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_credits_item ON public.media_credits USING btree (media_item_id, role, ord);


-- Name: idx_media_credits_person; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_credits_person ON public.media_credits USING btree (person_id, role);


-- Name: idx_media_files_active_item; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_files_active_item ON public.media_files USING btree (media_item_id) WHERE (status = 'active'::text);


-- Name: idx_media_files_hash; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_files_hash ON public.media_files USING btree (file_hash) WHERE (file_hash IS NOT NULL);


-- Name: idx_media_files_item; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_files_item ON public.media_files USING btree (media_item_id);


-- Name: idx_media_files_missing; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_files_missing ON public.media_files USING btree (missing_since) WHERE (status = 'missing'::text);


-- Name: idx_media_items_anilist; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_anilist ON public.media_items USING btree (anilist_id) WHERE (anilist_id IS NOT NULL);


-- Name: idx_media_items_created; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_created ON public.media_items USING btree (created_at DESC) WHERE (deleted_at IS NULL);


-- Name: idx_media_items_episodes_lib_recent; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_episodes_lib_recent ON public.media_items USING btree (library_id, created_at DESC) WHERE ((deleted_at IS NULL) AND (type = 'episode'::text));


-- Name: idx_media_items_episodes_recent; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_episodes_recent ON public.media_items USING btree (created_at DESC) WHERE ((deleted_at IS NULL) AND (type = 'episode'::text));


-- Name: idx_media_items_franchise; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_franchise ON public.media_items USING btree (franchise_id) WHERE (franchise_id IS NOT NULL);


-- Name: idx_media_items_kind; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_kind ON public.media_items USING btree (kind) WHERE (kind IS NOT NULL);


-- Name: idx_media_items_library; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_library ON public.media_items USING btree (library_id) WHERE (deleted_at IS NULL);


-- Name: idx_media_items_library_type_title_year; Type: INDEX; Schema: public; Owner: -

CREATE UNIQUE INDEX idx_media_items_library_type_title_year ON public.media_items USING btree (library_id, type, title, COALESCE(year, 0)) WHERE ((parent_id IS NULL) AND (deleted_at IS NULL));


-- Name: idx_media_items_mal; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_mal ON public.media_items USING btree (mal_id) WHERE (mal_id IS NOT NULL);


-- Name: idx_media_items_mb_album_artist; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_mb_album_artist ON public.media_items USING btree (musicbrainz_album_artist_id) WHERE ((musicbrainz_album_artist_id IS NOT NULL) AND (deleted_at IS NULL));


-- Name: idx_media_items_mb_artist; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_mb_artist ON public.media_items USING btree (musicbrainz_artist_id) WHERE ((musicbrainz_artist_id IS NOT NULL) AND (deleted_at IS NULL));


-- Name: idx_media_items_mb_release; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_mb_release ON public.media_items USING btree (musicbrainz_release_id) WHERE ((musicbrainz_release_id IS NOT NULL) AND (deleted_at IS NULL));


-- Name: idx_media_items_mb_release_group; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_mb_release_group ON public.media_items USING btree (musicbrainz_release_group_id) WHERE ((musicbrainz_release_group_id IS NOT NULL) AND (deleted_at IS NULL));


-- Name: idx_media_items_original_title_trgm; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_original_title_trgm ON public.media_items USING gin (original_title public.gin_trgm_ops) WHERE ((deleted_at IS NULL) AND (original_title IS NOT NULL));


-- Name: idx_media_items_parent; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_parent ON public.media_items USING btree (parent_id) WHERE (deleted_at IS NULL);


-- Name: idx_media_items_parent_type_index; Type: INDEX; Schema: public; Owner: -

CREATE UNIQUE INDEX idx_media_items_parent_type_index ON public.media_items USING btree (parent_id, type, index) WHERE ((parent_id IS NOT NULL) AND (index IS NOT NULL) AND (deleted_at IS NULL));


-- Name: idx_media_items_rating; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_rating ON public.media_items USING btree (rating DESC NULLS LAST) WHERE ((deleted_at IS NULL) AND (rating IS NOT NULL));


-- Name: idx_media_items_search; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_search ON public.media_items USING gin (search_vector);


-- Name: idx_media_items_title_trgm; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_title_trgm ON public.media_items USING gin (title public.gin_trgm_ops) WHERE (deleted_at IS NULL);


-- Name: idx_media_items_tmdb; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_tmdb ON public.media_items USING btree (tmdb_id) WHERE (tmdb_id IS NOT NULL);


-- Name: idx_media_items_year; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_items_year ON public.media_items USING btree (year) WHERE ((deleted_at IS NULL) AND (year IS NOT NULL));


-- Name: idx_media_requests_decided_by; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_requests_decided_by ON public.media_requests USING btree (decided_by) WHERE (decided_by IS NOT NULL);


-- Name: idx_media_requests_fulfilled_item_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_requests_fulfilled_item_id ON public.media_requests USING btree (fulfilled_item_id) WHERE (fulfilled_item_id IS NOT NULL);


-- Name: idx_media_requests_requested_service_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_requests_requested_service_id ON public.media_requests USING btree (requested_service_id) WHERE (requested_service_id IS NOT NULL);


-- Name: idx_media_requests_service_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_requests_service_id ON public.media_requests USING btree (service_id) WHERE (service_id IS NOT NULL);


-- Name: idx_media_requests_user_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_media_requests_user_id ON public.media_requests USING btree (user_id);


-- Name: idx_notifications_item_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_notifications_item_id ON public.notifications USING btree (item_id) WHERE (item_id IS NOT NULL);


-- Name: idx_notifications_user_created; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_notifications_user_created ON public.notifications USING btree (user_id, created_at DESC);


-- Name: idx_notifications_user_unread; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_notifications_user_unread ON public.notifications USING btree (user_id, read, created_at DESC) WHERE (read = false);


-- Name: idx_password_reset_tokens_hash; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_password_reset_tokens_hash ON public.password_reset_tokens USING btree (token_hash) WHERE (used_at IS NULL);


-- Name: idx_password_reset_tokens_user_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_password_reset_tokens_user_id ON public.password_reset_tokens USING btree (user_id);


-- Name: idx_people_name; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_people_name ON public.people USING btree (lower(name));


-- Name: idx_people_name_pattern; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_people_name_pattern ON public.people USING btree (lower(name) text_pattern_ops);


-- Name: idx_photo_metadata_camera_make_trgm; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_photo_metadata_camera_make_trgm ON public.photo_metadata USING gin (camera_make public.gin_trgm_ops) WHERE (camera_make IS NOT NULL);


-- Name: idx_photo_metadata_camera_model_trgm; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_photo_metadata_camera_model_trgm ON public.photo_metadata USING gin (camera_model public.gin_trgm_ops) WHERE (camera_model IS NOT NULL);


-- Name: idx_photo_metadata_gps; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_photo_metadata_gps ON public.photo_metadata USING btree (gps_lat, gps_lon) WHERE ((gps_lat IS NOT NULL) AND (gps_lon IS NOT NULL));


-- Name: idx_photo_metadata_lens_model_trgm; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_photo_metadata_lens_model_trgm ON public.photo_metadata USING gin (lens_model public.gin_trgm_ops) WHERE (lens_model IS NOT NULL);


-- Name: idx_photo_metadata_taken_at; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_photo_metadata_taken_at ON public.photo_metadata USING btree (taken_at DESC) WHERE (taken_at IS NOT NULL);


-- Name: idx_photo_metadata_taken_at_gps; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_photo_metadata_taken_at_gps ON public.photo_metadata USING btree (taken_at DESC) WHERE ((gps_lat IS NOT NULL) AND (gps_lon IS NOT NULL) AND (taken_at IS NOT NULL));


-- Name: idx_recordings_item_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_recordings_item_id ON public.recordings USING btree (item_id) WHERE (item_id IS NOT NULL);


-- Name: idx_recordings_program_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_recordings_program_id ON public.recordings USING btree (program_id) WHERE (program_id IS NOT NULL);


-- Name: idx_recordings_schedule; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_recordings_schedule ON public.recordings USING btree (schedule_id) WHERE (schedule_id IS NOT NULL);


-- Name: idx_recordings_status; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_recordings_status ON public.recordings USING btree (status, starts_at);


-- Name: idx_recordings_user; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_recordings_user ON public.recordings USING btree (user_id, starts_at DESC);


-- Name: idx_scheduled_tasks_due; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_scheduled_tasks_due ON public.scheduled_tasks USING btree (next_run_at) WHERE enabled;


-- Name: idx_scheduled_tasks_type; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_scheduled_tasks_type ON public.scheduled_tasks USING btree (task_type);


-- Name: idx_schedules_channel; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_schedules_channel ON public.schedules USING btree (channel_id) WHERE (enabled = true);


-- Name: idx_schedules_program_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_schedules_program_id ON public.schedules USING btree (program_id) WHERE (program_id IS NOT NULL);


-- Name: idx_schedules_user; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_schedules_user ON public.schedules USING btree (user_id);


-- Name: idx_sessions_expires; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_sessions_expires ON public.sessions USING btree (expires_at);


-- Name: idx_sessions_user; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_sessions_user ON public.sessions USING btree (user_id);


-- Name: idx_task_runs_task; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_task_runs_task ON public.task_runs USING btree (task_id, started_at DESC);


-- Name: idx_trickplay_status_file_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_trickplay_status_file_id ON public.trickplay_status USING btree (file_id) WHERE (file_id IS NOT NULL);


-- Name: idx_trickplay_status_status; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_trickplay_status_status ON public.trickplay_status USING btree (status) WHERE (status = ANY (ARRAY['pending'::text, 'failed'::text]));


-- Name: idx_user_favorites_user_created; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_user_favorites_user_created ON public.user_favorites USING btree (user_id, created_at DESC);


-- Name: idx_user_watch_status_user_status; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_user_watch_status_user_status ON public.user_watch_status USING btree (user_id, status);


-- Name: idx_users_discord_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_users_discord_id ON public.users USING btree (discord_id) WHERE (discord_id IS NOT NULL);


-- Name: idx_users_email; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_users_email ON public.users USING btree (email) WHERE (email IS NOT NULL);


-- Name: idx_users_github_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_users_github_id ON public.users USING btree (github_id) WHERE (github_id IS NOT NULL);


-- Name: idx_users_google_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_users_google_id ON public.users USING btree (google_id) WHERE (google_id IS NOT NULL);


-- Name: idx_users_parent; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_users_parent ON public.users USING btree (parent_user_id) WHERE (parent_user_id IS NOT NULL);


-- Name: idx_watch_events_event_occurred; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_watch_events_event_occurred ON ONLY public.watch_events USING btree (event_type, occurred_at DESC) WHERE (event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]));


-- Name: idx_watch_events_file_id; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_watch_events_file_id ON ONLY public.watch_events USING btree (file_id);


-- Name: idx_watch_events_session; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_watch_events_session ON ONLY public.watch_events USING btree (session_id) WHERE (session_id IS NOT NULL);


-- Name: idx_watch_events_user_history; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_watch_events_user_history ON ONLY public.watch_events USING btree (user_id, occurred_at DESC) WHERE (event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]));


-- Name: idx_watch_events_user_media; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_watch_events_user_media ON ONLY public.watch_events USING btree (user_id, media_id, occurred_at DESC);


-- Name: idx_webhook_failures_endpoint; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_webhook_failures_endpoint ON public.webhook_failures USING btree (endpoint_id);


-- Name: idx_webhook_failures_failed; Type: INDEX; Schema: public; Owner: -

CREATE INDEX idx_webhook_failures_failed ON public.webhook_failures USING btree (failed_at DESC);


-- Name: media_files_file_path_key; Type: INDEX; Schema: public; Owner: -
-- Plain UNIQUE on file_path. Replaced the prior partial unique
-- WHERE status != 'deleted' (00080's workaround) when the
-- 'deleted' status went away — there's no longer a third class
-- of rows to exclude from uniqueness.

CREATE UNIQUE INDEX media_files_file_path_key ON public.media_files USING btree (file_path);


-- Name: media_requests_status_created; Type: INDEX; Schema: public; Owner: -

CREATE INDEX media_requests_status_created ON public.media_requests USING btree (status, created_at DESC);


-- Name: media_requests_tmdb_lookup; Type: INDEX; Schema: public; Owner: -

CREATE INDEX media_requests_tmdb_lookup ON public.media_requests USING btree (type, tmdb_id) WHERE (status = ANY (ARRAY['approved'::text, 'downloading'::text]));


-- Name: media_requests_unique_active; Type: INDEX; Schema: public; Owner: -

CREATE UNIQUE INDEX media_requests_unique_active ON public.media_requests USING btree (user_id, type, tmdb_id) WHERE (status = ANY (ARRAY['pending'::text, 'approved'::text, 'downloading'::text]));


-- Name: media_requests_user_created; Type: INDEX; Schema: public; Owner: -

CREATE INDEX media_requests_user_created ON public.media_requests USING btree (user_id, created_at DESC);


-- Name: plugins_role_enabled; Type: INDEX; Schema: public; Owner: -

CREATE INDEX plugins_role_enabled ON public.plugins USING btree (role) WHERE (enabled = true);


-- Name: uq_scheduled_tasks_enabled_type; Type: INDEX; Schema: public; Owner: -

CREATE UNIQUE INDEX uq_scheduled_tasks_enabled_type ON public.scheduled_tasks USING btree (task_type) WHERE enabled;


-- Name: users_oidc_unique; Type: INDEX; Schema: public; Owner: -

CREATE UNIQUE INDEX users_oidc_unique ON public.users USING btree (oidc_issuer, oidc_subject) WHERE ((oidc_issuer IS NOT NULL) AND (oidc_subject IS NOT NULL));


-- Name: users_saml_unique; Type: INDEX; Schema: public; Owner: -

CREATE UNIQUE INDEX users_saml_unique ON public.users USING btree (saml_issuer, saml_subject) WHERE ((saml_issuer IS NOT NULL) AND (saml_subject IS NOT NULL));


-- Name: watch_events_2026_03_event_type_occurred_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_03_event_type_occurred_at_idx ON public.watch_events_2026_03 USING btree (event_type, occurred_at DESC) WHERE (event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]));


-- Name: watch_events_2026_03_file_id_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_03_file_id_idx ON public.watch_events_2026_03 USING btree (file_id);


-- Name: watch_events_2026_03_session_id_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_03_session_id_idx ON public.watch_events_2026_03 USING btree (session_id) WHERE (session_id IS NOT NULL);


-- Name: watch_events_2026_03_user_id_media_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_03_user_id_media_id_occurred_at_idx ON public.watch_events_2026_03 USING btree (user_id, media_id, occurred_at DESC);


-- Name: watch_events_2026_03_user_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_03_user_id_occurred_at_idx ON public.watch_events_2026_03 USING btree (user_id, occurred_at DESC) WHERE (event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]));


-- Name: watch_events_2026_04_event_type_occurred_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_04_event_type_occurred_at_idx ON public.watch_events_2026_04 USING btree (event_type, occurred_at DESC) WHERE (event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]));


-- Name: watch_events_2026_04_file_id_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_04_file_id_idx ON public.watch_events_2026_04 USING btree (file_id);


-- Name: watch_events_2026_04_session_id_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_04_session_id_idx ON public.watch_events_2026_04 USING btree (session_id) WHERE (session_id IS NOT NULL);


-- Name: watch_events_2026_04_user_id_media_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_04_user_id_media_id_occurred_at_idx ON public.watch_events_2026_04 USING btree (user_id, media_id, occurred_at DESC);


-- Name: watch_events_2026_04_user_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_04_user_id_occurred_at_idx ON public.watch_events_2026_04 USING btree (user_id, occurred_at DESC) WHERE (event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]));


-- Name: watch_events_2026_05_event_type_occurred_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_05_event_type_occurred_at_idx ON public.watch_events_2026_05 USING btree (event_type, occurred_at DESC) WHERE (event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]));


-- Name: watch_events_2026_05_file_id_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_05_file_id_idx ON public.watch_events_2026_05 USING btree (file_id);


-- Name: watch_events_2026_05_session_id_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_05_session_id_idx ON public.watch_events_2026_05 USING btree (session_id) WHERE (session_id IS NOT NULL);


-- Name: watch_events_2026_05_user_id_media_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_05_user_id_media_id_occurred_at_idx ON public.watch_events_2026_05 USING btree (user_id, media_id, occurred_at DESC);


-- Name: watch_events_2026_05_user_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_2026_05_user_id_occurred_at_idx ON public.watch_events_2026_05 USING btree (user_id, occurred_at DESC) WHERE (event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]));


-- Name: watch_events_default_event_type_occurred_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_default_event_type_occurred_at_idx ON public.watch_events_default USING btree (event_type, occurred_at DESC) WHERE (event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]));


-- Name: watch_events_default_file_id_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_default_file_id_idx ON public.watch_events_default USING btree (file_id);


-- Name: watch_events_default_session_id_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_default_session_id_idx ON public.watch_events_default USING btree (session_id) WHERE (session_id IS NOT NULL);


-- Name: watch_events_default_user_id_media_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_default_user_id_media_id_occurred_at_idx ON public.watch_events_default USING btree (user_id, media_id, occurred_at DESC);


-- Name: watch_events_default_user_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_events_default_user_id_occurred_at_idx ON public.watch_events_default USING btree (user_id, occurred_at DESC) WHERE (event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]));


-- Name: watch_state_user_id_media_id_idx; Type: INDEX; Schema: public; Owner: -

CREATE UNIQUE INDEX watch_state_user_id_media_id_idx ON public.watch_state USING btree (user_id, media_id);


-- Name: watch_state_user_id_status_idx; Type: INDEX; Schema: public; Owner: -

CREATE INDEX watch_state_user_id_status_idx ON public.watch_state USING btree (user_id, status);


-- Name: watch_events_2026_03_event_type_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_event_occurred ATTACH PARTITION public.watch_events_2026_03_event_type_occurred_at_idx;


-- Name: watch_events_2026_03_file_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_file_id ATTACH PARTITION public.watch_events_2026_03_file_id_idx;


-- Name: watch_events_2026_03_pkey; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.watch_events_pkey ATTACH PARTITION public.watch_events_2026_03_pkey;


-- Name: watch_events_2026_03_session_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_session ATTACH PARTITION public.watch_events_2026_03_session_id_idx;


-- Name: watch_events_2026_03_user_id_media_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_user_media ATTACH PARTITION public.watch_events_2026_03_user_id_media_id_occurred_at_idx;


-- Name: watch_events_2026_03_user_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_user_history ATTACH PARTITION public.watch_events_2026_03_user_id_occurred_at_idx;


-- Name: watch_events_2026_04_event_type_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_event_occurred ATTACH PARTITION public.watch_events_2026_04_event_type_occurred_at_idx;


-- Name: watch_events_2026_04_file_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_file_id ATTACH PARTITION public.watch_events_2026_04_file_id_idx;


-- Name: watch_events_2026_04_pkey; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.watch_events_pkey ATTACH PARTITION public.watch_events_2026_04_pkey;


-- Name: watch_events_2026_04_session_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_session ATTACH PARTITION public.watch_events_2026_04_session_id_idx;


-- Name: watch_events_2026_04_user_id_media_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_user_media ATTACH PARTITION public.watch_events_2026_04_user_id_media_id_occurred_at_idx;


-- Name: watch_events_2026_04_user_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_user_history ATTACH PARTITION public.watch_events_2026_04_user_id_occurred_at_idx;


-- Name: watch_events_2026_05_event_type_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_event_occurred ATTACH PARTITION public.watch_events_2026_05_event_type_occurred_at_idx;


-- Name: watch_events_2026_05_file_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_file_id ATTACH PARTITION public.watch_events_2026_05_file_id_idx;


-- Name: watch_events_2026_05_pkey; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.watch_events_pkey ATTACH PARTITION public.watch_events_2026_05_pkey;


-- Name: watch_events_2026_05_session_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_session ATTACH PARTITION public.watch_events_2026_05_session_id_idx;


-- Name: watch_events_2026_05_user_id_media_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_user_media ATTACH PARTITION public.watch_events_2026_05_user_id_media_id_occurred_at_idx;


-- Name: watch_events_2026_05_user_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_user_history ATTACH PARTITION public.watch_events_2026_05_user_id_occurred_at_idx;


-- Name: watch_events_default_event_type_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_event_occurred ATTACH PARTITION public.watch_events_default_event_type_occurred_at_idx;


-- Name: watch_events_default_file_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_file_id ATTACH PARTITION public.watch_events_default_file_id_idx;


-- Name: watch_events_default_pkey; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.watch_events_pkey ATTACH PARTITION public.watch_events_default_pkey;


-- Name: watch_events_default_session_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_session ATTACH PARTITION public.watch_events_default_session_id_idx;


-- Name: watch_events_default_user_id_media_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_user_media ATTACH PARTITION public.watch_events_default_user_id_media_id_occurred_at_idx;


-- Name: watch_events_default_user_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -

ALTER INDEX public.idx_watch_events_user_history ATTACH PARTITION public.watch_events_default_user_id_occurred_at_idx;


-- Name: audit_log audit_log_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.audit_log
    ADD CONSTRAINT audit_log_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;


-- Name: channels channels_tuner_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.channels
    ADD CONSTRAINT channels_tuner_id_fkey FOREIGN KEY (tuner_id) REFERENCES public.tuner_devices(id) ON DELETE CASCADE;


-- Name: collection_items collection_items_collection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.collection_items
    ADD CONSTRAINT collection_items_collection_id_fkey FOREIGN KEY (collection_id) REFERENCES public.collections(id) ON DELETE CASCADE;


-- Name: collection_items collection_items_media_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.collection_items
    ADD CONSTRAINT collection_items_media_item_id_fkey FOREIGN KEY (media_item_id) REFERENCES public.media_items(id) ON DELETE CASCADE;


-- Name: collections collections_library_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.collections
    ADD CONSTRAINT collections_library_id_fkey FOREIGN KEY (library_id) REFERENCES public.libraries(id) ON DELETE CASCADE;


-- Name: collections collections_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.collections
    ADD CONSTRAINT collections_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: epg_programs epg_programs_channel_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.epg_programs
    ADD CONSTRAINT epg_programs_channel_id_fkey FOREIGN KEY (channel_id) REFERENCES public.channels(id) ON DELETE CASCADE;


-- Name: external_subtitles external_subtitles_file_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.external_subtitles
    ADD CONSTRAINT external_subtitles_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.media_files(id) ON DELETE CASCADE;


-- Name: intro_markers intro_markers_media_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.intro_markers
    ADD CONSTRAINT intro_markers_media_item_id_fkey FOREIGN KEY (media_item_id) REFERENCES public.media_items(id) ON DELETE CASCADE;


-- Name: invite_tokens invite_tokens_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.invite_tokens
    ADD CONSTRAINT invite_tokens_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: invite_tokens invite_tokens_used_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.invite_tokens
    ADD CONSTRAINT invite_tokens_used_by_fkey FOREIGN KEY (used_by) REFERENCES public.users(id) ON DELETE SET NULL;


-- Name: item_cooccurrence item_cooccurrence_item_a_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.item_cooccurrence
    ADD CONSTRAINT item_cooccurrence_item_a_fkey FOREIGN KEY (item_a) REFERENCES public.media_items(id) ON DELETE CASCADE;


-- Name: item_cooccurrence item_cooccurrence_item_b_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.item_cooccurrence
    ADD CONSTRAINT item_cooccurrence_item_b_fkey FOREIGN KEY (item_b) REFERENCES public.media_items(id) ON DELETE CASCADE;


-- Name: library_access library_access_library_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.library_access
    ADD CONSTRAINT library_access_library_id_fkey FOREIGN KEY (library_id) REFERENCES public.libraries(id) ON DELETE CASCADE;


-- Name: library_access library_access_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.library_access
    ADD CONSTRAINT library_access_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: media_credits media_credits_media_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_credits
    ADD CONSTRAINT media_credits_media_item_id_fkey FOREIGN KEY (media_item_id) REFERENCES public.media_items(id) ON DELETE CASCADE;


-- Name: media_credits media_credits_person_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_credits
    ADD CONSTRAINT media_credits_person_id_fkey FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE CASCADE;


-- Name: media_files media_files_media_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_files
    ADD CONSTRAINT media_files_media_item_id_fkey FOREIGN KEY (media_item_id) REFERENCES public.media_items(id) ON DELETE CASCADE;


-- Name: media_items media_items_library_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_items
    ADD CONSTRAINT media_items_library_id_fkey FOREIGN KEY (library_id) REFERENCES public.libraries(id) ON DELETE CASCADE;


-- Name: media_items media_items_parent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_items
    ADD CONSTRAINT media_items_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES public.media_items(id) ON DELETE CASCADE;


-- Name: media_requests media_requests_decided_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_requests
    ADD CONSTRAINT media_requests_decided_by_fkey FOREIGN KEY (decided_by) REFERENCES public.users(id) ON DELETE SET NULL;


-- Name: media_requests media_requests_fulfilled_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_requests
    ADD CONSTRAINT media_requests_fulfilled_item_id_fkey FOREIGN KEY (fulfilled_item_id) REFERENCES public.media_items(id) ON DELETE SET NULL;


-- Name: media_requests media_requests_requested_service_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_requests
    ADD CONSTRAINT media_requests_requested_service_id_fkey FOREIGN KEY (requested_service_id) REFERENCES public.arr_services(id) ON DELETE SET NULL;


-- Name: media_requests media_requests_service_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_requests
    ADD CONSTRAINT media_requests_service_id_fkey FOREIGN KEY (service_id) REFERENCES public.arr_services(id) ON DELETE SET NULL;


-- Name: media_requests media_requests_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.media_requests
    ADD CONSTRAINT media_requests_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: notifications notifications_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.notifications
    ADD CONSTRAINT notifications_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.media_items(id) ON DELETE SET NULL;


-- Name: notifications notifications_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.notifications
    ADD CONSTRAINT notifications_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: password_reset_tokens password_reset_tokens_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: photo_metadata photo_metadata_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.photo_metadata
    ADD CONSTRAINT photo_metadata_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.media_items(id) ON DELETE CASCADE;


-- Name: recordings recordings_channel_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.recordings
    ADD CONSTRAINT recordings_channel_id_fkey FOREIGN KEY (channel_id) REFERENCES public.channels(id) ON DELETE CASCADE;


-- Name: recordings recordings_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.recordings
    ADD CONSTRAINT recordings_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.media_items(id) ON DELETE SET NULL;


-- Name: recordings recordings_program_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.recordings
    ADD CONSTRAINT recordings_program_id_fkey FOREIGN KEY (program_id) REFERENCES public.epg_programs(id) ON DELETE SET NULL;


-- Name: recordings recordings_schedule_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.recordings
    ADD CONSTRAINT recordings_schedule_id_fkey FOREIGN KEY (schedule_id) REFERENCES public.schedules(id) ON DELETE SET NULL;


-- Name: recordings recordings_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.recordings
    ADD CONSTRAINT recordings_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: schedules schedules_channel_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.schedules
    ADD CONSTRAINT schedules_channel_id_fkey FOREIGN KEY (channel_id) REFERENCES public.channels(id) ON DELETE CASCADE;


-- Name: schedules schedules_program_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.schedules
    ADD CONSTRAINT schedules_program_id_fkey FOREIGN KEY (program_id) REFERENCES public.epg_programs(id) ON DELETE SET NULL;


-- Name: schedules schedules_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.schedules
    ADD CONSTRAINT schedules_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: sessions sessions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: task_runs task_runs_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.task_runs
    ADD CONSTRAINT task_runs_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.scheduled_tasks(id) ON DELETE CASCADE;


-- Name: trickplay_status trickplay_status_file_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.trickplay_status
    ADD CONSTRAINT trickplay_status_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.media_files(id) ON DELETE SET NULL;


-- Name: trickplay_status trickplay_status_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.trickplay_status
    ADD CONSTRAINT trickplay_status_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.media_items(id) ON DELETE CASCADE;


-- Name: user_favorites user_favorites_media_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.user_favorites
    ADD CONSTRAINT user_favorites_media_id_fkey FOREIGN KEY (media_id) REFERENCES public.media_items(id) ON DELETE CASCADE;


-- Name: user_favorites user_favorites_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.user_favorites
    ADD CONSTRAINT user_favorites_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: user_watch_status user_watch_status_media_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.user_watch_status
    ADD CONSTRAINT user_watch_status_media_item_id_fkey FOREIGN KEY (media_item_id) REFERENCES public.media_items(id) ON DELETE CASCADE;


-- Name: user_watch_status user_watch_status_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.user_watch_status
    ADD CONSTRAINT user_watch_status_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: users users_parent_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_parent_user_id_fkey FOREIGN KEY (parent_user_id) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: watch_events watch_events_file_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE public.watch_events
    ADD CONSTRAINT watch_events_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.media_files(id) ON DELETE SET NULL;


-- Name: watch_events watch_events_media_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE public.watch_events
    ADD CONSTRAINT watch_events_media_id_fkey FOREIGN KEY (media_id) REFERENCES public.media_items(id) ON DELETE CASCADE;


-- Name: watch_events watch_events_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE public.watch_events
    ADD CONSTRAINT watch_events_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


-- Name: webhook_failures webhook_failures_endpoint_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -

ALTER TABLE ONLY public.webhook_failures
    ADD CONSTRAINT webhook_failures_endpoint_id_fkey FOREIGN KEY (endpoint_id) REFERENCES public.webhook_endpoints(id) ON DELETE CASCADE;


-- PostgreSQL database dump complete


-- pg_dump emits materialized views WITH NO DATA (it never restores
-- matview content). On a fresh DB the unpopulated state breaks
-- REFRESH ... CONCURRENTLY (PG requires the view be populated at least
-- once first), which is what the runtime refresh path uses. Populate
-- both matviews here so the first concurrent refresh from the app
-- succeeds.
REFRESH MATERIALIZED VIEW public.hub_recently_added;
REFRESH MATERIALIZED VIEW public.watch_state;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
-- Destructive uninstall — drops everything in the public schema. There is
-- no graceful "downgrade to N-1" path from the squashed init; if you need
-- to roll back a specific decision, layer a new migration on top instead.
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
-- +goose StatementEnd
